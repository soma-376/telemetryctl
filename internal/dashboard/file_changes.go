package dashboard

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sort"
	"strings"
)

// file_changes.operation 의 어휘다. v3 의 CHECK 제약과 같은 네 값이고 이 목록 밖의 값은
// 저장될 수 없다 (docs/sqlite-schema/file-changes.md).
const (
	FileOpCreate = "create"
	FileOpModify = "modify"
	FileOpDelete = "delete"
	FileOpRename = "rename"
)

// maxFileTimeline 은 파일 하나가 돌려주는 시간순 변경 목록의 상한이다.
//
// 합계에는 상한이 없다 — 상한을 넘긴 변경도 전부 세고 더한다. 잘리는 것은 화면에 실어
// 보내는 목록뿐이다. 그래야 "화면 합계가 원본 file_changes 합계와 일치한다" 는 성질이
// 세션 길이와 무관하게 성립한다. 잘렸다는 사실은 TimelineTruncated 가 알린다.
const maxFileTimeline = 200

// FileOperationCounts 는 변경 종류별 건수다.
//
// 네 종류를 따로 세는 이유는 합계 하나로는 화면이 구분할 수 없기 때문이다 — 5회 수정된
// 파일과 만들어졌다 지워진 파일은 "변경 5건" 과 "변경 2건" 이 아니라 서로 다른 사건이다.
type FileOperationCounts struct {
	Create int64 `json:"create"`
	Modify int64 `json:"modify"`
	Delete int64 `json:"delete"`
	Rename int64 `json:"rename"`
}

// FileChangeEntry 는 file_changes 한 행이다. 집계하기 전의 원본 한 건이다.
type FileChangeEntry struct {
	// ChangeID 는 file_changes.id, ToolCallID 는 그 변경을 만든 tool_calls.id 다.
	// 화면이 파일 변경에서 도구 호출 타임라인으로 건너뛸 수 있어야 한다.
	ChangeID   int64 `json:"change_id"`
	ToolCallID int64 `json:"tool_call_id"`
	// TS 는 tool_calls.called_at 이다 (UTC unix 초). file_changes 에는 시각 컬럼이 없어
	// 도구 호출의 시각이 곧 변경 시각이다. 모르면 0 이다.
	TS       int64  `json:"ts"`
	ToolName string `json:"tool_name"`

	// Operation 은 create|modify|delete|rename 중 하나다.
	Operation string `json:"operation"`
	// FilePath 는 원경로다. rename 이면 **새 경로**다 (ADR 0010, 스키마 문서).
	FilePath string `json:"file_path"`
	// RenamedFrom 은 rename 이전 경로다. rename 이 아니면 빈 문자열이다.
	// 스키마의 CHECK 가 rename 에는 이 값이 반드시 있게 강제한다.
	RenamedFrom string `json:"renamed_from"`

	// Additions·Deletions 는 미관측이면 null 이다. 0 과 다르다 (LineCount).
	Additions LineCount `json:"additions"`
	Deletions LineCount `json:"deletions"`

	OldHash string `json:"old_hash"`
	NewHash string `json:"new_hash"`
}

// FileChangeSummary 는 경로 하나에 일어난 변경 전부의 화면용 합계다.
//
// 묶는 키는 `file_path` 다. rename 은 새 경로 쪽으로 묶이고 이전 경로는 RenamedFrom 에
// 남는다 — 둘 중 하나를 버리면 "이 파일이 어디서 왔는가" 를 화면이 답할 수 없다.
//
// 같은 경로가 지워졌다가 다시 만들어지면 그것도 **한 줄**이다. Timeline 이 그 순서를
// 그대로 갖고 있고 LastOperation 이 마지막 상태를 알려준다. 재생성을 별도 줄로 쪼개면
// 파일 목록이 시간축이 되어 "어떤 파일을 얼마나 고쳤나" 를 읽을 수 없게 된다.
type FileChangeSummary struct {
	// FilePath 는 원경로다. FileName 은 basename, FileExt 는 점을 뗀 확장자다.
	FilePath string `json:"file_path"`
	FileName string `json:"file_name"`
	FileExt  string `json:"file_ext"`

	// Changes 는 이 경로의 file_changes 행 수다. Operations 네 값의 합과 같다.
	Changes    int64               `json:"changes"`
	Operations FileOperationCounts `json:"operations"`
	// LastOperation 은 시간순 마지막 변경의 종류다. delete 면 지금은 없는 파일이고,
	// delete 뒤에 create 가 있었다면 create 다.
	LastOperation string `json:"last_operation"`
	// RenamedFrom 은 이 경로로 들어온 이전 경로들이다. 처음 관측한 시간순이고 중복은 없다.
	// 한 파일이 두 번 이상 옮겨 왔으면 여러 개다. rename 이 없었으면 빈 슬라이스다.
	RenamedFrom []string `json:"renamed_from"`

	// Additions·Deletions 는 **관측된 값만** 더한 합이다. 한 건도 관측되지 않았으면
	// null 이다 — 0 이 아니다.
	Additions LineCount `json:"additions"`
	Deletions LineCount `json:"deletions"`
	// UnobservedAdditions·UnobservedDeletions 는 줄 수가 NULL 이던 변경 건수다.
	// 합계가 몇 건을 근거로 만들어졌는지 화면이 알아야 "12줄 추가 (3건 미관측)" 을 그린다.
	UnobservedAdditions int64 `json:"unobserved_additions"`
	UnobservedDeletions int64 `json:"unobserved_deletions"`

	// FirstTS·LastTS 는 이 경로의 첫·마지막 변경 시각이다 (UTC unix 초).
	FirstTS int64 `json:"first_ts"`
	LastTS  int64 `json:"last_ts"`

	// Timeline 은 시간순 변경 목록이다. 합계만으로는 사라지는 순서를 여기서 보존한다.
	Timeline []FileChangeEntry `json:"timeline"`
	// TimelineTruncated 는 목록이 maxFileTimeline 에서 잘렸다는 뜻이다.
	// 잘려도 위의 합계·건수는 전부 반영된 값이다.
	TimelineTruncated bool `json:"timeline_truncated"`
}

// FileChangeTotals 는 세션 전체의 파일 변경 합계다.
//
// **이 값은 언제나 원본 file_changes 의 합계와 같다.** Files 목록이나 Timeline 이
// 화면 사정으로 잘려도 여기는 잘리지 않는다.
type FileChangeTotals struct {
	// Files 는 변경이 있었던 서로 다른 경로 수다.
	Files      int64               `json:"files"`
	Changes    int64               `json:"changes"`
	Operations FileOperationCounts `json:"operations"`

	Additions           LineCount `json:"additions"`
	Deletions           LineCount `json:"deletions"`
	UnobservedAdditions int64     `json:"unobserved_additions"`
	UnobservedDeletions int64     `json:"unobserved_deletions"`
}

// SessionFileChanges 는 세션 하나의 파일 변경 화면 한 장이다.
type SessionFileChanges struct {
	// Found 가 false 면 그 id 의 세션이 없다는 뜻이다. 에러가 아니다 — 보존 정책이 지운
	// 세션의 id 를 화면이 아직 들고 있는 것은 정상이다 (SessionDetail.Found 와 같은 규약).
	Found     bool  `json:"found"`
	SessionID int64 `json:"session_id"`

	// Files 는 변경량 내림차순이다. 동률은 경로 오름차순으로 갈린다 (sortFileSummaries).
	Files  []FileChangeSummary `json:"files"`
	Totals FileChangeTotals    `json:"totals"`
}

// sessionFileChangesSQL 은 세션에 속한 file_changes 를 시간순으로 전부 읽는다.
//
// # 왜 GROUP BY 가 아니라 원본 행인가
//
// 티켓이 요구하는 것이 둘이다 — 경로별 합계와 **시간 순서**. GROUP BY 는 순서를 지우므로
// 어차피 원본 행이 필요하고, 그러면 합계를 SQL 과 Go 양쪽에서 만들 이유가 없다.
// 한 곳에서 접으면 "화면 합계 = 원본 합계" 가 계산 방식으로 보장된다.
//
// # 왜 llm_calls 를 함께 조인하지 않는가
//
// 세션 하나에 llm_calls 와 file_changes 를 한 질의로 조인하면 행이 곱해져 SUM 이 부풀어
// 오른다. 출처별로 따로 집계하고 Go 에서 합치는 것이 이 패키지의 방식이다 (sessions.go).
//
// 정렬 2순위·3순위가 id 인 이유는 called_at 이 **초** 단위라 같은 값이 흔하기 때문이다.
// 같은 초에 일어난 편집들의 순서는 저장 순서(= 도착 순서)로 정해야 화면이 흔들리지 않는다.
const sessionFileChangesSQL = `SELECT f.id, f.tool_call_id,
  f.file_path, f.operation, COALESCE(f.renamed_from,''),
  f.additions, f.deletions,
  COALESCE(f.old_hash,''), COALESCE(f.new_hash,''),
  COALESCE(c.called_at,0), COALESCE(c.tool_name,'')
FROM file_changes f
JOIN tool_calls c ON c.id = f.tool_call_id
WHERE c.turn_id IN (` + sessionTurns + `)
ORDER BY COALESCE(c.called_at,0) ASC, c.id ASC, f.id ASC`

const sessionExistsSQL = `SELECT 1 FROM sessions WHERE id = ?`

// FileChanges 는 세션 하나의 파일별 변경 집계다 (계획서 「파일 변경」).
//
// 조인 경로는 sessions → turns → tool_calls → file_changes 다. schema.go 의
// ix_turns_session · ix_tool_calls_turn 과 v3 의 ix_fc_tool 이 이 경로를 받친다.
//
// DB 가 없거나 id 가 없으면 에러가 아니라 Found=false 인 빈 결과다.
//
// SessionDetail.Files 와의 차이는 이것이다 — 그쪽은 목록 화면이 쓰는 경로별 한 줄 요약이고
// 줄 수를 0 으로 눕힌다. 여기는 변경 종류·rename 이력·시간 순서·미관측 여부를 모두 보존한다.
func (r *Reader) FileChanges(ctx context.Context, sessionID int64) (SessionFileChanges, error) {
	const op = "파일 변경 집계 조회"

	out := SessionFileChanges{SessionID: sessionID, Files: []FileChangeSummary{}}
	db, ok := r.db()
	if !ok || sessionID <= 0 {
		return out, nil
	}

	var exists int
	err := db.QueryRowContext(ctx, sessionExistsSQL, sessionID).Scan(&exists)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return out, nil
	case err != nil:
		return SessionFileChanges{}, queryErr(op, err)
	}
	out.Found = true

	entries, err := fileChangeEntries(ctx, db, sessionID)
	if err != nil {
		return SessionFileChanges{}, err
	}
	out.Files, out.Totals = foldFileChanges(entries)
	return out, nil
}

// fileChangeEntries 는 세션의 file_changes 를 시간순 원본 그대로 읽는다.
func fileChangeEntries(ctx context.Context, db sqlQuerier, sessionID int64) (entries []FileChangeEntry, err error) {
	const op = "파일 변경 조회"

	rows, err := db.QueryContext(ctx, sessionFileChangesSQL, sessionID)
	if err != nil {
		return nil, queryErr(op, err)
	}
	defer closeRows(rows, op, &err)

	entries = []FileChangeEntry{}
	for rows.Next() {
		var (
			e         FileChangeEntry
			additions sql.NullInt64
			deletions sql.NullInt64
		)
		if serr := rows.Scan(&e.ChangeID, &e.ToolCallID,
			&e.FilePath, &e.Operation, &e.RenamedFrom,
			&additions, &deletions, &e.OldHash, &e.NewHash,
			&e.TS, &e.ToolName); serr != nil {
			return nil, queryErr(op, serr)
		}
		// NULL 은 여기서 0 이 되지 않는다. 이 한 줄이 이 티켓의 경계다.
		e.Additions = scanLines(additions)
		e.Deletions = scanLines(deletions)
		entries = append(entries, e)
	}
	return entries, nil
}

// scanLines 는 NULL 을 미관측으로 옮긴다. dashboard.go 의 nullInt64 와 같은 판단이고
// 표현만 포인터 대신 LineCount 다.
func scanLines(n sql.NullInt64) LineCount {
	if !n.Valid {
		return LineCount{}
	}
	return ObservedLines(n.Int64)
}

// foldFileChanges 는 시간순 변경 목록을 경로별 합계로 접는다.
//
// 순수 함수다 — DB 없이 테이블 주도로 검증할 수 있어야 집계 규칙(미관측 처리·rename 보존·
// 재생성 처리)을 픽스처 없이도 못박을 수 있다.
//
// entries 는 시간 오름차순이라고 가정한다. 그 가정이 깨져도 합계는 옳고 FirstTS·LastTS 는
// 방어적으로 min/max 를 취한다. 다만 Timeline 과 LastOperation 은 받은 순서를 따른다 —
// 여기서 다시 정렬하면 SQL 이 정한 동률 규칙(도착 순서)을 덮어쓰게 된다.
func foldFileChanges(entries []FileChangeEntry) ([]FileChangeSummary, FileChangeTotals) {
	files := []FileChangeSummary{}
	index := make(map[string]int, len(entries))
	var totals FileChangeTotals

	for _, e := range entries {
		i, seen := index[e.FilePath]
		if !seen {
			i = len(files)
			index[e.FilePath] = i
			files = append(files, newFileSummary(e))
		}
		f := &files[i]

		f.Changes++
		f.LastOperation = e.Operation
		if e.TS < f.FirstTS {
			f.FirstTS = e.TS
		}
		if e.TS > f.LastTS {
			f.LastTS = e.TS
		}
		countOperation(&f.Operations, e.Operation)
		if e.Operation == FileOpRename && e.RenamedFrom != "" &&
			!containsPath(f.RenamedFrom, e.RenamedFrom) {
			f.RenamedFrom = append(f.RenamedFrom, e.RenamedFrom)
		}
		f.Additions = accumulate(f.Additions, e.Additions, &f.UnobservedAdditions)
		f.Deletions = accumulate(f.Deletions, e.Deletions, &f.UnobservedDeletions)
		if len(f.Timeline) < maxFileTimeline {
			f.Timeline = append(f.Timeline, e)
		} else {
			f.TimelineTruncated = true
		}

		totals.Changes++
		countOperation(&totals.Operations, e.Operation)
		totals.Additions = accumulate(totals.Additions, e.Additions, &totals.UnobservedAdditions)
		totals.Deletions = accumulate(totals.Deletions, e.Deletions, &totals.UnobservedDeletions)
	}

	totals.Files = int64(len(files))
	sortFileSummaries(files)
	return files, totals
}

func newFileSummary(e FileChangeEntry) FileChangeSummary {
	name := baseName(e.FilePath)
	return FileChangeSummary{
		FilePath:    e.FilePath,
		FileName:    name,
		FileExt:     strings.TrimPrefix(filepath.Ext(name), "."),
		FirstTS:     e.TS,
		LastTS:      e.TS,
		RenamedFrom: []string{},
		Timeline:    []FileChangeEntry{},
	}
}

// accumulate 는 관측된 줄 수만 더하고 미관측은 건수로만 센다.
//
// NULL 을 0 으로 더하는 것과 결과가 같아 보이지만 다르다 — 전부 미관측이면 합계도 미관측
// (null) 이고, 화면은 "0줄" 이 아니라 "—" 를 그린다.
func accumulate(sum, v LineCount, unobserved *int64) LineCount {
	n, ok := v.Get()
	if !ok {
		*unobserved++
		return sum
	}
	return sum.plus(n)
}

// countOperation 은 어휘 밖의 값을 조용히 어느 칸에도 넣지 않는다. v3 의 CHECK 가 네 값을
// 강제하므로 도달할 수 없지만, 도달한다면 Changes 합계와 네 칸의 합이 어긋나는 것이
// 잘못된 칸에 섞여 보이지 않게 되는 것보다 낫다.
func countOperation(c *FileOperationCounts, op string) {
	switch op {
	case FileOpCreate:
		c.Create++
	case FileOpModify:
		c.Modify++
	case FileOpDelete:
		c.Delete++
	case FileOpRename:
		c.Rename++
	}
}

func containsPath(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// sortFileSummaries 는 변경량 내림차순으로 정렬한다.
//
// # 동률 규칙
//
// 2순위는 **경로 오름차순** 이다. 경로는 묶음의 키라 파일 하나마다 정확히 하나뿐이고,
// 따라서 이 두 키로 전순서가 결정된다 — 같은 입력이면 언제나 같은 목록이 나온다.
//
// 시각을 2순위로 두지 않은 이유는 called_at 이 초 단위라 그 자체가 동률투성이이기 때문이다.
// 동률을 깨려고 고른 키가 다시 동률이면 순서는 그대로 불안정하다.
//
// 미관측 파일은 변경량이 0 으로 취급돼 0줄 파일들과 같은 자리에 온다. 그 무리 안에서도
// 경로 순이라 순서는 결정적이다.
func sortFileSummaries(files []FileChangeSummary) {
	sort.Slice(files, func(i, j int) bool {
		ci, cj := fileChurn(files[i]), fileChurn(files[j])
		if ci != cj {
			return ci > cj
		}
		return files[i].FilePath < files[j].FilePath
	})
}

// fileChurn 은 정렬 1순위인 변경량이다. 미관측을 0 으로 보는 유일한 자리이고 — 정렬 키라
// 항등원이 필요하다 — 이 값은 화면에 나가지 않는다.
func fileChurn(f FileChangeSummary) int64 {
	return f.Additions.Or(0) + f.Deletions.Or(0)
}

// FileChanges 는 세션 하나의 파일별 변경 집계다. Reader.FileChanges 를 그대로 감싼다.
//
// service.go 가 아니라 여기 있는 이유는 이 메서드가 이 파일이 소유한 질의의 일부이기
// 때문이다. Go 는 같은 패키지 안이면 어느 파일에서든 메서드를 붙일 수 있다.
func (s *Service) FileChanges(ctx context.Context, sessionID int64) (SessionFileChanges, error) {
	s.reconnect()
	return s.reader.FileChanges(ctx, sessionID)
}
