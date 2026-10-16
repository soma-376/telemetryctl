package dashboard

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/event"
	"github.com/your-org/pulsemetry/internal/session"
	"github.com/your-org/pulsemetry/internal/store"
)

// ── 픽스처 보조 ─────────────────────────────────────────────────────────────
//
// helper_test.go 의 fileChange 는 관측된 줄 수를 가진 modify 하나만 만든다. 이 티켓은
// 네 종류의 operation 과 **미관측** 을 구분해야 하므로 여기서 따로 만든다.

// fcModify 는 줄 수가 관측된 수정이다.
func fcModify(path string, add, del int64) session.FileChange {
	return session.FileChange{
		Path:      path,
		Operation: session.OperationModify,
		Additions: event.Some(add),
		Deletions: event.Some(del),
	}
}

// fcUnobserved 는 줄 수를 관측하지 못한 수정이다 — additions·deletions 가 NULL 로 들어간다.
// session.FileChangeOf 가 만드는 기본 모양이기도 하다 (줄 수를 채우지 않는다).
func fcUnobserved(path string) session.FileChange {
	return session.FileChange{Path: path, Operation: session.OperationModify}
}

// fcCreate 는 파일 생성이다.
func fcCreate(path string, add int64) session.FileChange {
	return session.FileChange{
		Path:      path,
		Operation: session.OperationCreate,
		Additions: event.Some(add),
		Deletions: event.Some[int64](0),
	}
}

// fcDelete 는 파일 삭제다. 추가된 줄이 없는 것은 **관측된 0** 이지 미관측이 아니다.
func fcDelete(path string, del int64) session.FileChange {
	return session.FileChange{
		Path:      path,
		Operation: session.OperationDelete,
		Additions: event.Some[int64](0),
		Deletions: event.Some(del),
	}
}

// fcRename 은 이름 변경이다. Path 가 새 경로, RenamedFrom 이 이전 경로다.
// 스키마의 CHECK 가 renamed_from 없는 rename 을 거부한다.
func fcRename(from, to string, add, del int64) session.FileChange {
	return session.FileChange{
		Path:        to,
		Operation:   session.OperationRename,
		RenamedFrom: from,
		Additions:   event.Some(add),
		Deletions:   event.Some(del),
	}
}

// fcSeeder 는 한 세션에 파일 변경을 시간순으로 쌓는다. callKey 는 전역 UNIQUE 라
// 자동으로 번호를 붙인다.
type fcSeeder struct {
	key   string
	start time.Time
	seq   int
	recs  []store.EventRecord
}

func newFCSeeder(sessionKey string, start time.Time) *fcSeeder {
	return &fcSeeder{key: sessionKey, start: start, seq: 1}
}

// add 는 offset 초 뒤에 일어난 도구 호출 하나와 그 파일 변경을 넣는다.
func (s *fcSeeder) add(offset time.Duration, tool string, f session.FileChange) *fcSeeder {
	s.recs = append(s.recs, toolRecord(s.key, "turn-1",
		fmt.Sprintf("%s-call-%03d", s.key, s.seq), s.start.Add(offset), s.seq,
		toolSpec{ToolName: tool, Success: event.Some(true), Target: f.Path, File: f}))
	s.seq++
	return s
}

// seed 는 세션과 이벤트를 한 배치로 쓰고 sessions.id 를 돌려준다.
func (s *fcSeeder) seed(f *fixture) int64 {
	f.t.Helper()
	f.write(store.Batch{
		Sessions: []session.Session{newSession(s.key, s.start)},
		Events:   s.recs,
	})
	return f.sessionID(vendorClaude, s.key)
}

// summaryFor 는 경로 하나의 요약을 찾는다.
func summaryFor(t *testing.T, got SessionFileChanges, path string) FileChangeSummary {
	t.Helper()
	for _, f := range got.Files {
		if f.FilePath == path {
			return f
		}
	}
	t.Fatalf("경로 %q 의 요약이 없다 (있는 것: %v)", path, filePaths(got))
	return FileChangeSummary{}
}

func filePaths(got SessionFileChanges) []string {
	out := make([]string, len(got.Files))
	for i, f := range got.Files {
		out[i] = f.FilePath
	}
	return out
}

// rawFileTotals 는 dashboard 를 거치지 않고 file_changes 를 직접 집계한다.
// 인수조건 「화면 합계가 원본 file_changes 합계와 일치한다」의 기준값이다.
type rawTotals struct {
	changes   int64
	files     int64
	additions sql.NullInt64
	deletions sql.NullInt64
	nullAdds  int64
	nullDels  int64
	creates   int64
	modifies  int64
	deletes   int64
	renames   int64
}

func rawFileTotals(t *testing.T, f *fixture, sessionID int64) rawTotals {
	t.Helper()
	const q = `SELECT COUNT(*), COUNT(DISTINCT f.file_path),
	  SUM(f.additions), SUM(f.deletions),
	  SUM(CASE WHEN f.additions IS NULL THEN 1 ELSE 0 END),
	  SUM(CASE WHEN f.deletions IS NULL THEN 1 ELSE 0 END),
	  SUM(CASE WHEN f.operation = 'create' THEN 1 ELSE 0 END),
	  SUM(CASE WHEN f.operation = 'modify' THEN 1 ELSE 0 END),
	  SUM(CASE WHEN f.operation = 'delete' THEN 1 ELSE 0 END),
	  SUM(CASE WHEN f.operation = 'rename' THEN 1 ELSE 0 END)
	FROM file_changes f
	JOIN tool_calls c ON c.id = f.tool_call_id
	JOIN turns t ON t.id = c.turn_id
	WHERE t.session_id = ?`

	var r rawTotals
	err := f.db.SQL().QueryRowContext(context.Background(), q, sessionID).Scan(
		&r.changes, &r.files, &r.additions, &r.deletions,
		&r.nullAdds, &r.nullDels,
		&r.creates, &r.modifies, &r.deletes, &r.renames)
	if err != nil {
		t.Fatalf("원본 합계 조회: %v", err)
	}
	return r
}

// ── 순수 집계 함수 ──────────────────────────────────────────────────────────

// entry 는 foldFileChanges 단위 테스트용 최소 항목이다.
func entry(ts int64, path, op string, add, del LineCount) FileChangeEntry {
	return FileChangeEntry{
		ChangeID: ts, ToolCallID: ts, TS: ts,
		FilePath: path, Operation: op, Additions: add, Deletions: del,
	}
}

func TestFoldFileChangesLineCounts(t *testing.T) {
	const p = "/w/a.go"

	tests := []struct {
		name           string
		entries        []FileChangeEntry
		wantAdditions  LineCount
		wantUnobserved int64
	}{
		{
			name:           "변경이 없으면 합계도 미관측",
			entries:        nil,
			wantAdditions:  LineCount{},
			wantUnobserved: 0,
		},
		{
			// 이 티켓의 핵심. 전부 NULL 인데 0 을 돌려주면 화면이 "0줄 바꿨다" 고 단정한다.
			name: "전부 미관측이면 합계도 미관측",
			entries: []FileChangeEntry{
				entry(1, p, FileOpModify, LineCount{}, LineCount{}),
				entry(2, p, FileOpModify, LineCount{}, LineCount{}),
			},
			wantAdditions:  LineCount{},
			wantUnobserved: 2,
		},
		{
			name: "관측된 0 은 관측된 값이다",
			entries: []FileChangeEntry{
				entry(1, p, FileOpModify, ObservedLines(0), ObservedLines(0)),
			},
			wantAdditions:  ObservedLines(0),
			wantUnobserved: 0,
		},
		{
			// 섞여 있으면 관측된 몫만 더하고 못 본 건수는 따로 센다.
			name: "일부만 관측되면 관측된 몫만 더한다",
			entries: []FileChangeEntry{
				entry(1, p, FileOpModify, ObservedLines(7), ObservedLines(1)),
				entry(2, p, FileOpModify, LineCount{}, LineCount{}),
				entry(3, p, FileOpModify, ObservedLines(5), ObservedLines(2)),
			},
			wantAdditions:  ObservedLines(12),
			wantUnobserved: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files, totals := foldFileChanges(tc.entries)

			if totals.Additions != tc.wantAdditions {
				n, ok := totals.Additions.Get()
				t.Fatalf("합계 additions = (%d, %v), want %+v", n, ok, tc.wantAdditions)
			}
			if totals.UnobservedAdditions != tc.wantUnobserved {
				t.Fatalf("미관측 건수 = %d, want %d", totals.UnobservedAdditions, tc.wantUnobserved)
			}
			if len(tc.entries) == 0 {
				if len(files) != 0 {
					t.Fatalf("빈 입력에 파일 %d개", len(files))
				}
				return
			}
			if len(files) != 1 {
				t.Fatalf("파일 = %d개, want 1", len(files))
			}
			if files[0].Additions != tc.wantAdditions {
				t.Fatalf("파일 additions = %+v, want %+v", files[0].Additions, tc.wantAdditions)
			}
		})
	}
}

// 미관측 합계는 JSON 에서 null 이어야 한다. Go 쪽만 지켜도 화면에는 0 이 보이면 소용이 없다.
func TestFoldFileChangesUnobservedMarshalsAsNull(t *testing.T) {
	_, totals := foldFileChanges([]FileChangeEntry{
		entry(1, "/w/a.go", FileOpModify, LineCount{}, LineCount{}),
	})
	b, err := totals.Additions.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	if string(b) != "null" {
		t.Fatalf("미관측 합계 JSON = %s, want null", b)
	}
}

// ── 정렬 안정성 ─────────────────────────────────────────────────────────────

// 인수조건: 변경량 동률에서도 정렬이 안정적이다.
// 2순위 키는 경로 오름차순이고, 경로는 묶음의 키라 전순서가 결정된다.
func TestFileChangesTieBreaksByPath(t *testing.T) {
	// 세 파일의 변경량이 전부 10 이다. 이 상태에서 순서를 정하는 것은 경로뿐이다.
	entries := []FileChangeEntry{
		entry(1, "/w/zebra.go", FileOpModify, ObservedLines(10), ObservedLines(0)),
		entry(2, "/w/alpha.go", FileOpModify, ObservedLines(5), ObservedLines(5)),
		entry(3, "/w/mango.go", FileOpModify, ObservedLines(0), ObservedLines(10)),
		// 변경량이 큰 파일은 동률 무리보다 앞이어야 한다.
		entry(4, "/w/zzz-big.go", FileOpModify, ObservedLines(99), ObservedLines(0)),
		// 미관측은 변경량 0 으로 취급돼 뒤로 가지만, 그 안에서도 경로 순이다.
		entry(5, "/w/beta.go", FileOpModify, LineCount{}, LineCount{}),
		entry(6, "/w/aardvark.go", FileOpModify, LineCount{}, LineCount{}),
	}
	want := []string{
		"/w/zzz-big.go",
		"/w/alpha.go", "/w/mango.go", "/w/zebra.go",
		"/w/aardvark.go", "/w/beta.go",
	}

	// 같은 입력을 여러 번 접어도 같은 순서가 나와야 한다. map 순회가 결과에 새면
	// 여기서 걸린다.
	for round := range 20 {
		files, _ := foldFileChanges(entries)
		got := make([]string, len(files))
		for i, f := range files {
			got[i] = f.FilePath
		}
		if len(got) != len(want) {
			t.Fatalf("round %d: 파일 = %v, want %v", round, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("round %d: 파일 = %v, want %v", round, got, want)
			}
		}
	}
}

// ── DB 왕복 ─────────────────────────────────────────────────────────────────

// seedFileChangeSession 은 네 종류의 operation·미관측·rename·재생성을 한 세션에 담는다.
func seedFileChangeSession(f *fixture) int64 {
	at := testNow.Add(-2 * time.Hour)
	return newFCSeeder("s-files", at).
		// runner.go: 수정 두 번. 한 번은 줄 수를 못 봤다.
		add(1*time.Second, "Edit", fcModify(workspaceA+"/runner.go", 30, 6)).
		add(2*time.Second, "Edit", fcUnobserved(workspaceA+"/runner.go")).
		// old.go → new.go 로 이름이 바뀐 뒤 새 경로에서 한 번 더 수정됐다.
		add(3*time.Second, "Bash", fcRename(workspaceA+"/old.go", workspaceA+"/new.go", 2, 1)).
		add(4*time.Second, "Edit", fcModify(workspaceA+"/new.go", 4, 0)).
		// gone.go: 만들어졌다가 지워졌다.
		add(5*time.Second, "Write", fcCreate(workspaceA+"/gone.go", 12)).
		add(6*time.Second, "Bash", fcDelete(workspaceA+"/gone.go", 12)).
		// reborn.go: 지워졌다가 **같은 경로로** 다시 만들어졌다 (ADR 0010 이 원경로를
		// 저장하기로 했기에 성립하는 픽스처다 — basename 이면 구분 자체가 없다).
		add(7*time.Second, "Write", fcCreate(workspaceA+"/reborn.go", 8)).
		add(8*time.Second, "Bash", fcDelete(workspaceA+"/reborn.go", 8)).
		add(9*time.Second, "Write", fcCreate(workspaceA+"/reborn.go", 20)).
		seed(f)
}

// 인수조건: 화면 합계가 원본 file_changes 합계와 일치한다.
func TestFileChangesTotalsMatchRawTable(t *testing.T) {
	f := newFixture(t)
	id := seedFileChangeSession(f)

	got, err := f.reader.FileChanges(context.Background(), id)
	if err != nil {
		t.Fatalf("FileChanges: %v", err)
	}
	if !got.Found {
		t.Fatal("Found = false")
	}
	raw := rawFileTotals(t, f, id)

	if got.Totals.Changes != raw.changes {
		t.Errorf("변경 건수 = %d, want %d (원본)", got.Totals.Changes, raw.changes)
	}
	if got.Totals.Files != raw.files {
		t.Errorf("파일 수 = %d, want %d (원본)", got.Totals.Files, raw.files)
	}
	if n, ok := got.Totals.Additions.Get(); !ok || !raw.additions.Valid || n != raw.additions.Int64 {
		t.Errorf("additions 합계 = (%d, %v), want %v", n, ok, raw.additions)
	}
	if n, ok := got.Totals.Deletions.Get(); !ok || !raw.deletions.Valid || n != raw.deletions.Int64 {
		t.Errorf("deletions 합계 = (%d, %v), want %v", n, ok, raw.deletions)
	}
	if got.Totals.UnobservedAdditions != raw.nullAdds {
		t.Errorf("additions 미관측 = %d, want %d", got.Totals.UnobservedAdditions, raw.nullAdds)
	}
	if got.Totals.UnobservedDeletions != raw.nullDels {
		t.Errorf("deletions 미관측 = %d, want %d", got.Totals.UnobservedDeletions, raw.nullDels)
	}

	wantOps := FileOperationCounts{
		Create: raw.creates, Modify: raw.modifies,
		Delete: raw.deletes, Rename: raw.renames,
	}
	if got.Totals.Operations != wantOps {
		t.Errorf("operation 집계 = %+v, want %+v (원본)", got.Totals.Operations, wantOps)
	}

	// 파일별 합계를 다시 더한 값도 총합과 같아야 한다. 목록과 총합이 갈리면 화면의
	// 두 숫자가 서로를 부정한다.
	var sumAdd, sumDel, sumChanges, sumUnobsAdd int64
	for _, file := range got.Files {
		sumAdd += file.Additions.Or(0)
		sumDel += file.Deletions.Or(0)
		sumChanges += file.Changes
		sumUnobsAdd += file.UnobservedAdditions
	}
	if sumAdd != got.Totals.Additions.Or(0) || sumDel != got.Totals.Deletions.Or(0) {
		t.Errorf("파일별 합 = (%d, %d), 총합 = (%d, %d)",
			sumAdd, sumDel, got.Totals.Additions.Or(0), got.Totals.Deletions.Or(0))
	}
	if sumChanges != got.Totals.Changes {
		t.Errorf("파일별 건수 합 = %d, 총합 = %d", sumChanges, got.Totals.Changes)
	}
	if sumUnobsAdd != got.Totals.UnobservedAdditions {
		t.Errorf("파일별 미관측 합 = %d, 총합 = %d", sumUnobsAdd, got.Totals.UnobservedAdditions)
	}
}

// 인수조건: rename 픽스처가 통과한다. renamed_from 과 새 file_path 를 모두 보존한다.
func TestFileChangesPreservesRename(t *testing.T) {
	f := newFixture(t)
	id := seedFileChangeSession(f)

	got, err := f.reader.FileChanges(context.Background(), id)
	if err != nil {
		t.Fatalf("FileChanges: %v", err)
	}

	newPath := workspaceA + "/new.go"
	oldPath := workspaceA + "/old.go"

	renamed := summaryFor(t, got, newPath)
	if renamed.Operations.Rename != 1 {
		t.Errorf("rename 건수 = %d, want 1", renamed.Operations.Rename)
	}
	if len(renamed.RenamedFrom) != 1 || renamed.RenamedFrom[0] != oldPath {
		t.Errorf("RenamedFrom = %v, want [%s]", renamed.RenamedFrom, oldPath)
	}
	// 새 경로에서 이어진 수정도 같은 줄에 합쳐진다.
	if n, ok := renamed.Additions.Get(); !ok || n != 6 {
		t.Errorf("새 경로 additions = (%d, %v), want (6, true)", n, ok)
	}
	// 타임라인의 rename 항목은 양쪽 경로를 다 갖고 있어야 한다.
	if len(renamed.Timeline) != 2 {
		t.Fatalf("타임라인 = %d건, want 2", len(renamed.Timeline))
	}
	first := renamed.Timeline[0]
	if first.Operation != FileOpRename || first.FilePath != newPath || first.RenamedFrom != oldPath {
		t.Errorf("rename 항목 = op=%q path=%q from=%q, want rename/%s/%s",
			first.Operation, first.FilePath, first.RenamedFrom, newPath, oldPath)
	}

	// 이전 경로는 자체 변경 행이 없으므로 별도 파일로 나타나지 않는다.
	for _, file := range got.Files {
		if file.FilePath == oldPath {
			t.Errorf("이전 경로가 별도 파일로 나왔다: %v", filePaths(got))
		}
	}
}

// 인수조건: 삭제 픽스처가 통과한다.
func TestFileChangesPreservesDelete(t *testing.T) {
	f := newFixture(t)
	id := seedFileChangeSession(f)

	got, err := f.reader.FileChanges(context.Background(), id)
	if err != nil {
		t.Fatalf("FileChanges: %v", err)
	}

	gone := summaryFor(t, got, workspaceA+"/gone.go")
	want := FileOperationCounts{Create: 1, Delete: 1}
	if gone.Operations != want {
		t.Errorf("operation 집계 = %+v, want %+v", gone.Operations, want)
	}
	if gone.LastOperation != FileOpDelete {
		t.Errorf("LastOperation = %q, want %q — 지금은 없는 파일이다", gone.LastOperation, FileOpDelete)
	}
	// 삭제된 파일의 additions 0 은 **관측된 0** 이다. 미관측과 다르다.
	if n, ok := gone.Additions.Get(); !ok || n != 12 {
		t.Errorf("additions = (%d, %v), want (12, true)", n, ok)
	}
	if n, ok := gone.Deletions.Get(); !ok || n != 12 {
		t.Errorf("deletions = (%d, %v), want (12, true)", n, ok)
	}
}

// 인수조건: 동일 경로 재생성 픽스처가 통과한다.
//
// create → delete → create 가 한 줄로 묶이되 시간 순서와 마지막 상태가 남아야 한다.
func TestFileChangesSamePathRecreated(t *testing.T) {
	f := newFixture(t)
	id := seedFileChangeSession(f)

	got, err := f.reader.FileChanges(context.Background(), id)
	if err != nil {
		t.Fatalf("FileChanges: %v", err)
	}

	reborn := summaryFor(t, got, workspaceA+"/reborn.go")
	if reborn.Changes != 3 {
		t.Fatalf("변경 건수 = %d, want 3", reborn.Changes)
	}
	want := FileOperationCounts{Create: 2, Delete: 1}
	if reborn.Operations != want {
		t.Errorf("operation 집계 = %+v, want %+v", reborn.Operations, want)
	}
	if reborn.LastOperation != FileOpCreate {
		t.Errorf("LastOperation = %q, want %q — 다시 만들어진 파일이다",
			reborn.LastOperation, FileOpCreate)
	}

	// 시간 순서가 보존돼야 한다. 합계만으로는 create 2 · delete 1 이 "지워졌다 다시 생겼다"
	// 인지 "생겼다 지워졌다 또 생겼다" 인지 구분되지 않는다.
	wantOps := []string{FileOpCreate, FileOpDelete, FileOpCreate}
	if len(reborn.Timeline) != len(wantOps) {
		t.Fatalf("타임라인 = %d건, want %d", len(reborn.Timeline), len(wantOps))
	}
	for i, op := range wantOps {
		if reborn.Timeline[i].Operation != op {
			gotOps := make([]string, len(reborn.Timeline))
			for j, e := range reborn.Timeline {
				gotOps[j] = e.Operation
			}
			t.Fatalf("타임라인 순서 = %v, want %v", gotOps, wantOps)
		}
		if i > 0 && reborn.Timeline[i-1].TS > reborn.Timeline[i].TS {
			t.Fatalf("타임라인이 시간 오름차순이 아니다: %d > %d",
				reborn.Timeline[i-1].TS, reborn.Timeline[i].TS)
		}
	}
	if reborn.FirstTS >= reborn.LastTS {
		t.Errorf("FirstTS(%d) 가 LastTS(%d) 보다 작지 않다", reborn.FirstTS, reborn.LastTS)
	}
	// 세 번의 생성·삭제 줄 수가 전부 관측됐다: 8 + 20 추가, 8 삭제.
	if n, ok := reborn.Additions.Get(); !ok || n != 28 {
		t.Errorf("additions = (%d, %v), want (28, true)", n, ok)
	}
}

// 줄 수가 NULL 인 변경은 0 이 아니라 미관측으로 나와야 한다 — 이 티켓의 구현 경계다.
func TestFileChangesKeepsNullDistinctFromZero(t *testing.T) {
	f := newFixture(t)
	at := testNow.Add(-time.Hour)
	id := newFCSeeder("s-null", at).
		// blind.go 는 두 번 고쳤는데 두 번 다 줄 수를 못 봤다.
		add(1*time.Second, "Edit", fcUnobserved(workspaceA+"/blind.go")).
		add(2*time.Second, "Edit", fcUnobserved(workspaceA+"/blind.go")).
		// zero.go 는 고쳤고 0줄이 바뀐 것을 **관측했다.**
		add(3*time.Second, "Edit", fcModify(workspaceA+"/zero.go", 0, 0)).
		seed(f)

	got, err := f.reader.FileChanges(context.Background(), id)
	if err != nil {
		t.Fatalf("FileChanges: %v", err)
	}

	blind := summaryFor(t, got, workspaceA+"/blind.go")
	if blind.Additions.Observed() || blind.Deletions.Observed() {
		t.Errorf("미관측 파일의 줄 수가 관측됨으로 나왔다: %+v / %+v",
			blind.Additions, blind.Deletions)
	}
	if blind.UnobservedAdditions != 2 || blind.UnobservedDeletions != 2 {
		t.Errorf("미관측 건수 = (%d, %d), want (2, 2)",
			blind.UnobservedAdditions, blind.UnobservedDeletions)
	}

	zero := summaryFor(t, got, workspaceA+"/zero.go")
	n, ok := zero.Additions.Get()
	if !ok || n != 0 {
		t.Errorf("관측된 0 = (%d, %v), want (0, true)", n, ok)
	}
	if zero.UnobservedAdditions != 0 {
		t.Errorf("관측된 변경이 미관측으로 세어졌다: %d", zero.UnobservedAdditions)
	}

	// 둘이 같은 값이면 이 타입이 존재할 이유가 없다.
	if blind.Additions == zero.Additions {
		t.Error("미관측과 관측된 0 이 같은 값이다")
	}

	// 세션 합계는 관측된 몫(0)만 반영하고 못 본 4건을 따로 센다.
	if n, ok := got.Totals.Additions.Get(); !ok || n != 0 {
		t.Errorf("합계 additions = (%d, %v), want (0, true)", n, ok)
	}
	if got.Totals.UnobservedAdditions != 2 {
		t.Errorf("합계 미관측 = %d, want 2", got.Totals.UnobservedAdditions)
	}
}

// 변경이 하나도 없는 세션과 없는 세션은 다르다. 둘 다 에러는 아니다.
func TestFileChangesEmptyAndMissing(t *testing.T) {
	f := newFixture(t)
	at := testNow.Add(-time.Hour)
	f.write(store.Batch{Sessions: []session.Session{newSession("s-bare", at)}})
	bareID := f.sessionID(vendorClaude, "s-bare")

	tests := []struct {
		name      string
		id        int64
		wantFound bool
	}{
		{name: "변경이 없는 세션은 Found=true 에 빈 목록", id: bareID, wantFound: true},
		{name: "없는 세션은 Found=false", id: bareID + 9999, wantFound: false},
		{name: "0 은 조회하지 않는다", id: 0, wantFound: false},
		{name: "음수도 조회하지 않는다", id: -1, wantFound: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := f.reader.FileChanges(context.Background(), tc.id)
			if err != nil {
				t.Fatalf("FileChanges: %v", err)
			}
			if got.Found != tc.wantFound {
				t.Fatalf("Found = %v, want %v", got.Found, tc.wantFound)
			}
			if got.Files == nil {
				t.Fatal("Files 가 nil 이다 — JSON 에서 null 이 되어 화면의 .map 이 터진다")
			}
			if len(got.Files) != 0 {
				t.Fatalf("파일 = %v, want 빈 목록", filePaths(got))
			}
			if got.Totals.Changes != 0 {
				t.Fatalf("합계 = %d, want 0", got.Totals.Changes)
			}
			// 변경이 없으면 합계도 미관측이다. 0 이라고 말할 근거가 없다.
			if got.Totals.Additions.Observed() {
				t.Fatal("변경이 없는데 줄 수가 관측됨으로 나왔다")
			}
		})
	}
}

// DB 부재에서의 동작은 absent_test.go 의 표가 소유한다 (ADR 0004의 「DB 부재 계약」).
// 여기서 다시 쓰지 않는다 — 같은 계약을 두 곳에서 단언하면 한쪽만 고쳐질 때 갈린다.

// 타임라인 상한을 넘겨도 합계는 잘리지 않는다. 잘리는 것은 목록뿐이다.
func TestFileChangesTimelineTruncationKeepsTotals(t *testing.T) {
	f := newFixture(t)
	at := testNow.Add(-3 * time.Hour)

	const changes = maxFileTimeline + 25
	s := newFCSeeder("s-many", at)
	for i := range changes {
		s.add(time.Duration(i)*time.Second, "Edit", fcModify(workspaceA+"/hot.go", 1, 1))
	}
	id := s.seed(f)

	got, err := f.reader.FileChanges(context.Background(), id)
	if err != nil {
		t.Fatalf("FileChanges: %v", err)
	}
	hot := summaryFor(t, got, workspaceA+"/hot.go")

	if !hot.TimelineTruncated {
		t.Error("TimelineTruncated = false — 조용히 잘렸다")
	}
	if len(hot.Timeline) != maxFileTimeline {
		t.Errorf("타임라인 = %d건, want %d", len(hot.Timeline), maxFileTimeline)
	}
	if hot.Changes != changes {
		t.Errorf("변경 건수 = %d, want %d — 상한이 건수까지 잘랐다", hot.Changes, changes)
	}
	if n, ok := hot.Additions.Get(); !ok || n != changes {
		t.Errorf("additions = (%d, %v), want (%d, true) — 상한이 합계까지 잘랐다", n, ok, changes)
	}

	raw := rawFileTotals(t, f, id)
	if got.Totals.Changes != raw.changes || got.Totals.Additions.Or(-1) != raw.additions.Int64 {
		t.Errorf("잘린 뒤 합계가 원본과 다르다: %+v vs %+v", got.Totals, raw)
	}
}

// 세션 경계를 넘지 않아야 한다. 다른 세션의 파일 변경이 섞이면 합계가 조용히 부풀어 오른다.
func TestFileChangesScopedToSession(t *testing.T) {
	f := newFixture(t)
	at := testNow.Add(-4 * time.Hour)

	idA := newFCSeeder("s-a", at).
		add(time.Second, "Edit", fcModify(workspaceA+"/shared.go", 10, 0)).
		seed(f)
	idB := newFCSeeder("s-b", at.Add(time.Hour)).
		add(time.Second, "Edit", fcModify(workspaceA+"/shared.go", 3, 0)).
		add(2*time.Second, "Edit", fcModify(workspaceA+"/only-b.go", 1, 0)).
		seed(f)

	gotA, err := f.reader.FileChanges(context.Background(), idA)
	if err != nil {
		t.Fatalf("FileChanges(A): %v", err)
	}
	if gotA.Totals.Files != 1 || gotA.Totals.Additions.Or(-1) != 10 {
		t.Errorf("세션 A = %+v, want 파일 1개 additions 10", gotA.Totals)
	}

	gotB, err := f.reader.FileChanges(context.Background(), idB)
	if err != nil {
		t.Fatalf("FileChanges(B): %v", err)
	}
	if gotB.Totals.Files != 2 || gotB.Totals.Additions.Or(-1) != 4 {
		t.Errorf("세션 B = %+v, want 파일 2개 additions 4", gotB.Totals)
	}
}

// Service 도 같은 결과를 준다. GUI 가 부르는 것은 이쪽이다 (ADR 0004).
func TestServiceFileChanges(t *testing.T) {
	f := newFixture(t)
	id := seedFileChangeSession(f)

	svc := NewService(f.path)
	if err := svc.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = svc.Stop() })

	got, err := svc.FileChanges(context.Background(), id)
	if err != nil {
		t.Fatalf("Service.FileChanges: %v", err)
	}
	want, err := f.reader.FileChanges(context.Background(), id)
	if err != nil {
		t.Fatalf("Reader.FileChanges: %v", err)
	}
	if got.Totals != want.Totals || len(got.Files) != len(want.Files) {
		t.Fatalf("Service 결과가 Reader 와 다르다: %+v vs %+v", got.Totals, want.Totals)
	}
}

// 공개 응답 타입의 json 태그는 전부 snake_case 여야 한다 (ADR 0004).
func TestFileChangeTypesUseSnakeCaseTags(t *testing.T) {
	assertSnakeCaseTags(t, SessionFileChanges{})
	assertSnakeCaseTags(t, FileChangeSummary{})
	assertSnakeCaseTags(t, FileChangeEntry{})
	assertSnakeCaseTags(t, FileChangeTotals{})
	assertSnakeCaseTags(t, FileOperationCounts{})
}
