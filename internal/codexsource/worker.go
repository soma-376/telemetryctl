package codexsource

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/your-org/pulsemetry/internal/store"
)

const (
	maxJSONLLine        = 4 << 20
	maxBatchBytes       = 4 << 20
	maxBatchRows        = 64
	defaultScanInterval = time.Minute
)

type Worker struct {
	DB           *store.DB
	Root         string
	Epoch        int64
	Notify       <-chan struct{}
	ScanInterval time.Duration
	Now          func() time.Time
	Progress     func(active bool)
	PhaseHook    func(phase string) // 실제 파일·독립 생산자 경계 테스트용
}

func (w Worker) phase(name string) {
	if w.PhaseHook != nil {
		w.PhaseHook(name)
	}
}

func (w Worker) heartbeat(ctx context.Context, active bool) error {
	if err := w.DB.CodexWorkerHeartbeat(ctx, w.Epoch, active); err != nil {
		return err
	}
	if w.Progress != nil {
		w.Progress(active)
	}
	return nil
}

func (w Worker) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

// Run은 하나의 고루틴에서 읽기·판정·저장을 순차 수행한다. 알림 유실은 주기 스캔과
// 영속 pending 재조회로 복구한다. 감독자는 작업 진행 기한을 감시한다.
func (w Worker) Run(ctx context.Context) error {
	if w.DB == nil {
		return errors.New("Codex worker DB missing")
	}
	interval := w.ScanInterval
	if interval <= 0 {
		interval = defaultScanInterval
	}
	if _, err := w.DB.BackfillCodexPending(ctx); err != nil {
		return err
	}
	if err := w.DB.RequeueCodexUnknown(ctx, ClassifierVersion); err != nil {
		return err
	}
	scanTicker := time.NewTicker(interval)
	defer scanTicker.Stop()
	pendingTicker := time.NewTicker(5 * time.Second)
	defer pendingTicker.Stop()
	heartbeat := time.NewTicker(5 * time.Second)
	defer heartbeat.Stop()
	runJob := func(job func(context.Context) error) error {
		if err := w.heartbeat(ctx, true); err != nil {
			return err
		}
		err := job(ctx)
		if err == nil {
			err = w.heartbeat(ctx, false)
		}
		return err
	}
	if err := runJob(w.RunOnce); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	for {
		var err error
		select {
		case <-ctx.Done():
			return nil
		case <-w.Notify:
			err = runJob(w.RunOnce)
		case <-scanTicker.C:
			err = runJob(w.RunOnce)
		case <-pendingTicker.C:
			err = runJob(w.processPending)
		case <-heartbeat.C:
			err = w.heartbeat(ctx, false)
		}
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
	}
}

// RunOnce는 DB에 있는 OTel 턴만 보강한다. JSONL에서 턴·호출·토큰을 생성하지 않는다.
func (w Worker) RunOnce(ctx context.Context) error {
	root := w.Root
	if root == "" {
		return nil
	}
	info, err := os.Stat(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("Codex JSONL root is not a directory")
	}
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		if err := w.heartbeat(ctx, true); err != nil {
			return err
		}
		if err := w.scanFile(ctx, path); err != nil {
			return err
		}
		return w.heartbeat(ctx, false)
	})
	if err != nil {
		return err
	}
	return w.processPending(ctx)
}

func (w Worker) processPending(ctx context.Context) error {
	{
		pending, err := w.DB.ListCodexPending(ctx, w.now().Unix(), 64)
		if err != nil {
			return err
		}
		if len(pending) == 0 {
			return nil
		}
		for _, p := range pending {
			if err := w.heartbeat(ctx, true); err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			// 확정된 라벨은 원문 삭제 뒤에도 유지한다.
			if p.Purged && p.ProcessingState == "finalized" && p.Label != "unknown" {
				if err := w.DB.DropCodexPending(ctx, w.Epoch, p.ID); err != nil {
					return err
				}
				continue
			}
			if p.TooLarge {
				if err := w.DB.CommitCodexDecision(ctx, w.Epoch, store.CodexDecision{TurnID: p.ID, Label: "unknown", ProcessingState: "retrying", LinkState: "unavailable", StructureEvidence: "{}", Reason: "OTel evidence exceeds 256 KiB cache bound", ClassifierVersion: ClassifierVersion, NextCheckAt: w.now().Add(time.Hour).Unix()}); err != nil {
					return err
				}
				if err := w.heartbeat(ctx, false); err != nil {
					return err
				}
				continue
			}
			recs, overflow, err := w.DB.CodexCandidateRecords(ctx, p)
			if err != nil {
				return err
			}
			converted := make([]Record, 0, len(recs))
			for _, r := range recs {
				converted = append(converted, Record{ID: r.ID, FileID: r.FileID, StartOffset: r.StartOffset, EndOffset: r.EndOffset, Owner: p.Owner, Type: r.Type, MessageID: r.MessageID, TurnID: r.TurnID, Body: r.Body, BodyHash: r.BodyHash, Completeness: r.Completeness, CompletenessEvidence: r.CompletenessEvidence, Structure: r.Structure, RepresentationKey: r.RepresentationKey, EventTime: r.EventTime, SessionMetaID: r.SessionMetaID})
			}
			w.phase("during_classification")
			d := Classify(Prompt{ID: p.ID, Owner: p.Owner, MessageID: p.MessageID, TurnID: p.TurnID, Body: p.Body, Completeness: p.Completeness, CompletenessEvidence: p.CompletenessEvidence, Source: p.Source, StartedAt: p.StartedAt, EndedAt: p.EndedAt, NextStartedAt: p.NextStartedAt, SameSecond: p.SameSecond, Conflict: p.Conflict, Purged: p.Purged}, converted, overflow)
			hasErrors, err := w.DB.CodexOwnerHasErrors(ctx, p.Owner)
			if err != nil {
				return err
			}
			ready, err := w.ownerReady(ctx, p.Owner)
			if err != nil {
				return err
			}
			if d.ProcessingState == "pending" && ready && !overflow && !hasErrors {
				d.ProcessingState = "finalized"
				d.Reason += "; available files scanned"
			} else if d.ProcessingState == "pending" && (overflow || hasErrors) {
				d.ProcessingState = "retrying"
			}
			next := int64(0)
			if d.Label == "unknown" || d.ProcessingState != "finalized" {
				next = w.now().Add(time.Hour).Unix()
			}
			if err := w.DB.CommitCodexDecision(ctx, w.Epoch, store.CodexDecision{TurnID: p.ID, LinkedRecordID: d.LinkedRecordID, Label: d.Label, ProcessingState: d.ProcessingState, LinkState: d.LinkState, LinkMethod: d.LinkMethod, StructureEvidence: d.StructureEvidence, Reason: d.Reason, ClassifierVersion: ClassifierVersion, NextCheckAt: next}); err != nil {
				return err
			}
			if err := w.heartbeat(ctx, false); err != nil {
				return err
			}
		}
	}
	return nil
}

func (w Worker) ownerReady(ctx context.Context, owner string) (bool, error) {
	files, err := w.DB.CodexOwnerFiles(ctx, owner)
	if err != nil {
		return false, err
	}
	active := false
	for _, f := range files {
		if f.Status == "quarantined" {
			return false, nil
		}
		if f.Status != "active" {
			continue
		}
		active = true
		info, e := os.Stat(f.Path)
		if e != nil || info.Size() != f.CommittedOffset {
			return false, nil
		}
	}
	return active, nil
}

// prefixHasher는 재개 시 전체 committed prefix를 한 번 검사하고 새 행만 누적한다.
func prefixHasher(ctx context.Context, f *os.File, offset int64) (hash.Hash, error) {
	h := sha256.New()
	buf := make([]byte, 64<<10)
	for at := int64(0); at < offset; {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n := min(int64(len(buf)), offset-at)
		if _, err := f.ReadAt(buf[:n], at); err != nil {
			return nil, err
		}
		_, _ = h.Write(buf[:n])
		at += n
	}
	return h, nil
}

func checkpointHash(ctx context.Context, f *os.File, offset int64) (string, error) {
	h, err := prefixHasher(ctx, f, offset)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func snapshotHash(h hash.Hash) ([]byte, error) { return h.(encoding.BinaryMarshaler).MarshalBinary() }
func restoreHash(h hash.Hash, state []byte) error {
	return h.(encoding.BinaryUnmarshaler).UnmarshalBinary(state)
}

func readCompleteLine(ctx context.Context, r *bufio.Reader, h hash.Hash) ([]byte, int64, bool, error) {
	var line []byte
	var size int64
	oversized := false
	for {
		if err := ctx.Err(); err != nil {
			return nil, size, oversized, err
		}
		fragment, err := r.ReadSlice('\n')
		_, _ = h.Write(fragment)
		size += int64(len(fragment))
		if !oversized && len(line)+len(fragment) <= maxJSONLLine {
			line = append(line, fragment...)
		} else {
			oversized = true
			line = nil
		}
		if err == nil {
			return line, size, oversized, nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(err, io.EOF) {
			return nil, size, oversized, io.EOF
		}
		return nil, size, oversized, err
	}
}

func (w Worker) scanFile(ctx context.Context, path string) error {
	f, err := openJSONL(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	w.phase("after_open")
	id, err := osFileID(f)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		return err
	}
	prior, found, err := w.DB.CodexFile(ctx, path)
	if err != nil {
		return err
	}
	moved := false
	if !found {
		prior, found, err = w.DB.CodexFileByOSID(ctx, id)
		if err != nil {
			return err
		}
		if found {
			if oldInfo, e := os.Stat(prior.Path); e == nil && os.SameFile(oldInfo, info) {
				return nil
			}
			moved = true
		}
	}
	if found && prior.Status == "quarantined" && prior.OSFileID == id && prior.ObservedSize == info.Size() && prior.ObservedModNS == info.ModTime().UnixNano() {
		hash, e := checkpointHash(ctx, f, prior.CommittedOffset)
		if e == nil && hash == prior.PrefixHash {
			return nil
		}
	}
	// 회전된 옛 파일은 새 원경로를 먼저 읽어 status=rotated가 되어도 같은 inode의
	// 체크포인트를 이어받는다. 격리·대체된 세대는 재사용하지 않는다.
	newGeneration := !found || prior.OSFileID != id || info.Size() < prior.CommittedOffset || (prior.Status != "active" && prior.Status != "rotated")
	priorDisposition := "rotated"
	if found && (prior.Status == "quarantined" || prior.OSFileID == id) {
		// 같은 파일의 잘림·수정 및 격리 파일의 복구는 과거 근거를 폐기한다.
		// 다른 OS 파일 ID로의 교체는 기존 파일의 유효한 rotation 근거를 보존한다.
		priorDisposition = "superseded"
	}
	var prefix hash.Hash
	if !newGeneration {
		prefix, err = prefixHasher(ctx, f, prior.CommittedOffset)
		if err != nil {
			return err
		}
		newGeneration = hex.EncodeToString(prefix.Sum(nil)) != prior.PrefixHash
	}
	if newGeneration {
		prefix = sha256.New()
	}
	offset := int64(0)
	owner, parent, source := "", "", ""
	if !newGeneration {
		offset = prior.CommittedOffset
		owner, parent, source = prior.Owner, prior.Parent, prior.OwnerSource
	}
	if _, err = f.Seek(offset, io.SeekStart); err != nil {
		return err
	}
	r := bufio.NewReaderSize(f, 64<<10)
	batch := store.CodexFileBatch{Path: path, OSFileID: id, Owner: owner, Parent: parent, OwnerSource: source, PriorID: prior.ID, PriorOffset: prior.CommittedOffset, PriorDisposition: priorDisposition, NewGeneration: newGeneration, Status: "active", CommittedOffset: offset}
	if !found {
		batch.PriorID = 0
		batch.PriorOffset = 0
	}
	bytesInBatch := 0
	commit := func() error {
		if len(batch.Records) == 0 && len(batch.Errors) == 0 && !moved {
			return nil
		}
		if batch.Owner == "" {
			return nil
		}
		batch.PrefixHash = hex.EncodeToString(prefix.Sum(nil))
		var e error
		current, e := f.Stat()
		if e != nil {
			return e
		}
		batch.ObservedSize = current.Size()
		batch.ObservedModNS = current.ModTime().UnixNano()
		w.phase("waiting_for_db_write")
		saved, e := w.DB.CommitCodexFile(ctx, w.Epoch, batch)
		if e != nil {
			return e
		}
		w.phase("after_db_commit")
		if e = w.heartbeat(ctx, false); e != nil {
			return e
		}
		if e = w.heartbeat(ctx, true); e != nil {
			return e
		}
		batch.PriorID = saved.ID
		batch.PriorOffset = saved.CommittedOffset
		batch.NewGeneration = false
		moved = false
		batch.Records = nil
		batch.Errors = nil
		bytesInBatch = 0
		return nil
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		before, e := snapshotHash(prefix)
		if e != nil {
			return e
		}
		w.phase("during_read")
		line, size, oversized, e := readCompleteLine(ctx, r, prefix)
		if errors.Is(e, io.EOF) {
			if e = restoreHash(prefix, before); e != nil {
				return e
			}
			return commit()
		} // 끝의 부분 행은 다시 읽는다.
		if e != nil {
			return e
		}
		start := offset
		offset += size
		if bytesInBatch > 0 && bytesInBatch+int(size) > maxBatchBytes {
			after, e := snapshotHash(prefix)
			if e != nil {
				return e
			}
			if e = restoreHash(prefix, before); e != nil {
				return e
			}
			if err := commit(); err != nil {
				return err
			}
			if e = restoreHash(prefix, after); e != nil {
				return e
			}
		}
		if batch.Owner == "" {
			if oversized || !utf8.Valid(line) {
				return w.quarantine(ctx, f, prefix, &batch, offset, start, "owner_context", "first row malformed")
			}
			o, p, s, found, e := FirstOwner(bytes.TrimSuffix(line, []byte{'\n'}))
			if e != nil || !found {
				return w.quarantine(ctx, f, prefix, &batch, offset, start, "owner_context", "first session_meta missing or malformed")
			}
			present, e := w.DB.CodexOwnerPresent(ctx, o)
			if e != nil {
				return e
			}
			if !present {
				return nil
			}
			batch.Owner, batch.Parent, batch.OwnerSource = o, p, s
		}
		batch.CommittedOffset = offset
		if oversized {
			batch.Errors = append(batch.Errors, store.CodexLineError{StartOffset: start, EndOffset: offset, Kind: "oversized", Diagnostic: "JSONL row exceeds 4 MiB"})
		} else if !utf8.Valid(line) {
			batch.Errors = append(batch.Errors, store.CodexLineError{StartOffset: start, EndOffset: offset, Kind: "invalid_utf8", Diagnostic: "complete JSONL row has invalid UTF-8"})
		} else {
			record, e := ParseLine(bytes.TrimSuffix(line, []byte{'\n'}), batch.Owner, batch.Parent, batch.OwnerSource)
			if e != nil {
				if bytes.Contains(line, []byte(`"turn_context"`)) {
					return w.quarantine(ctx, f, prefix, &batch, offset, start, "required_context", "turn context malformed")
				}
				kind, diagnostic := "invalid_json", "complete JSONL row could not be parsed"
				if errors.Is(e, ErrJSONLStructureLimit) {
					kind, diagnostic = "structure_limit", "complete JSONL row exceeds parser structure limit"
				}
				batch.Errors = append(batch.Errors, store.CodexLineError{StartOffset: start, EndOffset: offset, Kind: kind, Diagnostic: diagnostic})
			} else if record.Type == "session_meta" && record.SessionMetaID == "" {
				return w.quarantine(ctx, f, prefix, &batch, offset, start, "owner_context", "session_meta id missing")
			} else if record.Type == "session_meta" && record.SessionMetaID != batch.Owner && record.SessionMetaID != batch.Parent && record.Parent != batch.Owner {
				return w.quarantine(ctx, f, prefix, &batch, offset, start, "owner_conflict", "session_meta owner changed")
			} else {
				record.StartOffset = start
				record.EndOffset = offset
				batch.Records = append(batch.Records, store.CodexRecord{StartOffset: record.StartOffset, EndOffset: record.EndOffset, Type: record.Type, EventTime: record.EventTime, MessageID: record.MessageID, TurnID: record.TurnID, Body: record.Body, BodyHash: record.BodyHash, Completeness: record.Completeness, CompletenessEvidence: record.CompletenessEvidence, Structure: record.Structure, RepresentationKey: record.RepresentationKey})
			}
		}
		bytesInBatch += int(size)
		if len(batch.Records)+len(batch.Errors) >= maxBatchRows || bytesInBatch >= maxBatchBytes {
			if err := commit(); err != nil {
				return err
			}
		}
	}
}

func (w Worker) quarantine(ctx context.Context, f *os.File, prefix hash.Hash, b *store.CodexFileBatch, end, start int64, kind, diagnostic string) error {
	b.Status = "quarantined"
	b.CommittedOffset = end
	b.Errors = append(b.Errors, store.CodexLineError{StartOffset: start, EndOffset: end, Kind: kind, Diagnostic: diagnostic})
	b.PrefixHash = hex.EncodeToString(prefix.Sum(nil))
	current, err := f.Stat()
	if err != nil {
		return err
	}
	b.ObservedSize = current.Size()
	b.ObservedModNS = current.ModTime().UnixNano()
	_, err = w.DB.CommitCodexFile(ctx, w.Epoch, *b)
	return err
}
