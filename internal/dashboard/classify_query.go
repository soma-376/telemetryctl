package dashboard

import (
	"context"
	"database/sql"
)

// 분류 입력을 v3 에서 읽어 온다 (PROJ-92). 규칙은 classify.go, 단계·비율은
// classify_phase.go 에 있고 이 파일은 **읽기만** 한다.
//
// # 왜 출처마다 질의를 나누는가
//
// aggregate.go 의 머리말과 같은 이유다. 턴 하나에 도구 호출이 5건, 파일 변경이 3건이면
// 한 질의로 JOIN 했을 때 15행이 나오고, 그 행들을 그대로 근거로 세면 같은 파일 변경이
// 다섯 번 센 것이 된다. 근거의 Count 는 실패 메시지에 그대로 나가는 값이라 부풀면
// 사람이 잘못된 결론을 낸다. 그래서 표마다 따로 읽고 Go 에서 id 로 붙인다.
//
// # 가상 턴은 분류하지 않는다
//
// turn_index 가 NULL 인 턴은 세션 수준 이벤트를 담는 가상 턴이다 (store/resolve.go 의
// virtualTurnKey). 사용자의 한 턴이 아니므로 「세션 흐름」의 칸이 될 수 없고, 그 안의
// session.count·mcp.connection 같은 신호를 분류에 섞으면 모든 세션에 같은 잡음이 붙는다.
// aggregate.go 의 프롬프트 수 집계가 같은 기준으로 이 턴을 뺀다.

// classifyIDChunk 는 IN 절 하나에 넣을 세션 수다. SQLite 의 기본 변수 상한
// (SQLITE_MAX_VARIABLE_NUMBER=999) 아래로 잡는다 — store/resolve.go 의 hashChunk 와 같은 이유다.
const classifyIDChunk = 400

// Classifier 는 분류 계산을 Reader 위에 얹는 얇은 계층이다.
//
// # 왜 Reader 의 메서드가 아닌가
//
// Reader 의 메서드는 "화면 한 장 = 질의 하나" 라는 조회 API 다 (ADR 0004). 분류는 그
// 질의들이 읽는 것과 같은 행을 다시 읽어 **파생 값을 계산**하는 일이고, 모든 화면이
// 필요로 하지도 않는다. 타입을 나누면 두 가지가 따라온다.
//
//   - 분류를 쓰지 않는 화면은 이 코드의 존재를 몰라도 된다.
//   - Reader 의 조회 메서드 목록이 그대로 유지된다 — absent_test.go 의 DB 부재 계약 표가
//     리플렉션으로 그 목록을 검사하므로, 조회가 아닌 것을 그 목록에 섞으면 계약이 흐려진다.
//
// DB 부재 계약 자체는 여기서도 같다. 파일이 없으면 에러가 아니라 빈 분류다.
type Classifier struct{ reader *Reader }

// NewClassifier 는 조회 핸들 위의 분류기다. r 이 nil 이면 모든 분류가 빈 결과다 —
// 미설치 상태를 호출자가 분기하지 않아도 되게 한다.
func NewClassifier(r *Reader) *Classifier { return &Classifier{reader: r} }

// Session 은 세션 하나를 조회 시점에 분류한다.
//
// 없는 세션이나 턴이 하나도 없는 세션은 에러가 아니다 — WorkType 이 unknown 이고
// Turns·Phases 가 빈 결과다. 보존 정책이 지운 세션의 id 를 화면이 아직 들고 있는 것은
// 정상 상황이고, 그때 앱이 에러 토스트를 띄울 이유가 없다 (sessions.go 의 Found 와 같은 규칙).
func (c *Classifier) Session(ctx context.Context, sessionID int64) (SessionClassification, error) {
	rows, err := c.Sessions(ctx, []int64{sessionID})
	if err != nil {
		return SessionClassification{}, err
	}
	if len(rows) == 0 {
		return ClassifyTurns(sessionID, nil), nil
	}
	return rows[0], nil
}

// Sessions 는 여러 세션을 한 번에 분류한다.
//
// 결과는 **인자 순서 그대로**다. 맵으로 돌려주면 호출자가 순회할 때 순서가 실행마다 달라져
// 화면의 목록이 흔들린다. 중복 id 는 각각 한 행씩 나온다 — 걸러 내면 호출자의 인덱스와
// 결과의 인덱스가 어긋난다.
func (c *Classifier) Sessions(ctx context.Context, sessionIDs []int64) ([]SessionClassification, error) {
	out := make([]SessionClassification, 0, len(sessionIDs))

	db, ok := c.db()
	if !ok {
		// DB 가 없는 것은 에러가 아니다 (dashboard.go 머리말). 전부 빈 분류다.
		for _, id := range sessionIDs {
			out = append(out, ClassifyTurns(id, nil))
		}
		return out, nil
	}

	bySession, err := loadTurnSignals(ctx, db, sessionIDs)
	if err != nil {
		return nil, err
	}
	for _, id := range sessionIDs {
		out = append(out, ClassifyTurns(id, bySession[id]))
	}
	return out, nil
}

// db 는 조회에 쓸 핸들이다. 분류기가 nil Reader 를 들고 있어도 터지지 않는다.
func (c *Classifier) db() (sqlQuerier, bool) {
	if c == nil || c.reader == nil {
		return nil, false
	}
	handle, ok := c.reader.db()
	if !ok {
		return nil, false
	}
	return handle, true
}

// loadTurnSignals 는 세션들의 턴 신호를 읽는다. 맵을 돌려주지만 호출자는 키로 찾기만
// 하므로 순회 순서에 의존하지 않는다.
func loadTurnSignals(ctx context.Context, db sqlQuerier, sessionIDs []int64) (map[int64][]TurnSignals, error) {
	bySession := map[int64][]TurnSignals{}
	// turnRefs 는 turn_id → 그 턴의 신호다. 뒤따르는 질의들이 여기에 값을 붙인다.
	turnRefs := map[int64]*TurnSignals{}
	// toolRefs 는 tool_call_id → 그 호출의 신호다. 파일 변경이 여기에 붙는다.
	toolRefs := map[int64]*ToolSignal{}

	for start := 0; start < len(sessionIDs); start += classifyIDChunk {
		end := min(start+classifyIDChunk, len(sessionIDs))
		chunk := sessionIDs[start:end]

		if err := loadTurnRows(ctx, db, chunk, bySession, turnRefs); err != nil {
			return nil, err
		}
		if err := loadToolRows(ctx, db, chunk, turnRefs, toolRefs); err != nil {
			return nil, err
		}
		if err := loadFileRows(ctx, db, chunk, toolRefs); err != nil {
			return nil, err
		}
		if err := loadEventRows(ctx, db, chunk, turnRefs); err != nil {
			return nil, err
		}
	}
	return bySession, nil
}

// idArgs 는 세션 id 슬라이스를 바인딩 인자로 옮긴다. 값은 전부 바인딩되고 질의문에
// 결합되는 것은 우리가 만든 자리표시자뿐이다.
func idArgs(ids []int64) []any {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return args
}

// classifyTurnsSQL 은 분류 대상 턴이다.
//
// 마지막 활동 시각을 두 승격 경로에서 함께 본다. events 만 보면 도구 호출이 다른 시각을
// 들고 있는 경우(promote 가 called_at 을 따로 계산하는 경우)를 놓치고, tool_calls 만 보면
// 도구를 안 쓴 턴의 끝을 아예 모른다. 둘 다 없으면 0 이고 길이는 다음 턴이 정한다.
const classifyTurnsSQL = `SELECT t.session_id, t.id, t.turn_index,
  COALESCE(t.started_at, 0), COALESCE(t.ended_at, 0),
  MAX(
    COALESCE((SELECT MAX(e.occurred_at) FROM events e WHERE e.turn_id = t.id), 0),
    COALESCE((SELECT MAX(c.called_at)   FROM tool_calls c WHERE c.turn_id = t.id), 0))
FROM turns t
WHERE t.turn_index IS NOT NULL AND t.session_id IN (`

func loadTurnRows(ctx context.Context, db sqlQuerier, ids []int64,
	bySession map[int64][]TurnSignals, turnRefs map[int64]*TurnSignals) (err error) {
	const op = "턴 분류 입력 조회"

	query := classifyTurnsSQL + placeholders(len(ids)) +
		`) ORDER BY t.session_id ASC, t.turn_index ASC, t.id ASC`
	rows, err := db.QueryContext(ctx, query, idArgs(ids)...)
	if err != nil {
		return queryErr(op, err)
	}
	defer closeRows(rows, op, &err)

	for rows.Next() {
		var (
			sessionID int64
			t         TurnSignals
		)
		if serr := rows.Scan(&sessionID, &t.TurnID, &t.TurnIndex,
			&t.StartedAt, &t.EndedAt, &t.LastSeenAt); serr != nil {
			return queryErr(op, serr)
		}
		// 슬라이스에 담은 뒤 그 원소의 주소를 잡는다. append 가 재할당하면 앞서 잡은
		// 주소가 죽으므로, 포인터는 슬라이스를 다 채운 뒤에 다시 잡는다 (아래 rebind).
		bySession[sessionID] = append(bySession[sessionID], t)
	}
	if rows.Err() != nil {
		return nil // closeRows 가 보고한다
	}
	rebindTurnRefs(bySession, turnRefs)
	return nil
}

// rebindTurnRefs 는 turn_id → 턴 신호 포인터를 다시 잡는다.
// append 로 슬라이스가 재할당돼도 여기서 한 번에 고쳐지므로 죽은 포인터가 남지 않는다.
func rebindTurnRefs(bySession map[int64][]TurnSignals, turnRefs map[int64]*TurnSignals) {
	for _, list := range bySession {
		for i := range list {
			turnRefs[list[i].TurnID] = &list[i]
		}
	}
}

const classifyToolsSQL = `SELECT c.turn_id, c.id,
  COALESCE(c.tool_name,''), COALESCE(c.target,''), c.success,
  COALESCE(c.error_type,''), COALESCE(c.decision,''), COALESCE(c.mcp_server,'')
FROM tool_calls c JOIN turns t ON t.id = c.turn_id
WHERE t.turn_index IS NOT NULL AND t.session_id IN (`

func loadToolRows(ctx context.Context, db sqlQuerier, ids []int64,
	turnRefs map[int64]*TurnSignals, toolRefs map[int64]*ToolSignal) (err error) {
	const op = "도구 호출 분류 입력 조회"

	// called_at 은 초 단위라 같은 초에 여러 행이 흔하다. id 를 2순위로 두어야 저장 순서
	// (= 도착 순서)가 근거 순서에 그대로 남는다 (sessions.go 의 타임라인과 같은 규칙).
	query := classifyToolsSQL + placeholders(len(ids)) +
		`) ORDER BY c.turn_id ASC, COALESCE(c.called_at,0) ASC, c.id ASC`
	rows, err := db.QueryContext(ctx, query, idArgs(ids)...)
	if err != nil {
		return queryErr(op, err)
	}
	defer closeRows(rows, op, &err)

	// 턴별로 도구를 다 모은 뒤에 포인터를 잡는다. 이유는 rebindTurnRefs 와 같다.
	order := []int64{}
	collected := map[int64][]ToolSignal{}
	callTurn := map[int64]int64{}
	callSlot := map[int64]int{}

	for rows.Next() {
		var (
			turnID  int64
			callID  int64
			tool    ToolSignal
			success sql.NullInt64
		)
		if serr := rows.Scan(&turnID, &callID, &tool.ToolName, &tool.Target,
			&success, &tool.ErrorType, &tool.Decision, &tool.MCPServer); serr != nil {
			return queryErr(op, serr)
		}
		tool.Success = nullBool(success)
		if _, seen := collected[turnID]; !seen {
			order = append(order, turnID)
		}
		callTurn[callID] = turnID
		callSlot[callID] = len(collected[turnID])
		collected[turnID] = append(collected[turnID], tool)
	}
	if rows.Err() != nil {
		return nil // closeRows 가 보고한다
	}

	for _, turnID := range order {
		if ref, ok := turnRefs[turnID]; ok {
			ref.Tools = collected[turnID]
		}
	}
	for callID, turnID := range callTurn {
		ref, ok := turnRefs[turnID]
		if !ok {
			continue
		}
		if slot := callSlot[callID]; slot < len(ref.Tools) {
			toolRefs[callID] = &ref.Tools[slot]
		}
	}
	return nil
}

const classifyFilesSQL = `SELECT f.tool_call_id, f.operation, f.file_path,
  COALESCE(f.additions,0), COALESCE(f.deletions,0)
FROM file_changes f
JOIN tool_calls c ON c.id = f.tool_call_id
JOIN turns t ON t.id = c.turn_id
WHERE t.turn_index IS NOT NULL AND t.session_id IN (`

func loadFileRows(ctx context.Context, db sqlQuerier, ids []int64, toolRefs map[int64]*ToolSignal) (err error) {
	const op = "파일 변경 분류 입력 조회"

	query := classifyFilesSQL + placeholders(len(ids)) +
		`) ORDER BY f.tool_call_id ASC, f.id ASC`
	rows, err := db.QueryContext(ctx, query, idArgs(ids)...)
	if err != nil {
		return queryErr(op, err)
	}
	defer closeRows(rows, op, &err)

	for rows.Next() {
		var (
			callID int64
			f      FileSignal
		)
		if serr := rows.Scan(&callID, &f.Operation, &f.FilePath,
			&f.Additions, &f.Deletions); serr != nil {
			return queryErr(op, serr)
		}
		if ref, ok := toolRefs[callID]; ok {
			ref.Files = append(ref.Files, f)
		}
	}
	return nil
}

// classifyEventsSQL 은 턴별 이벤트 이름이다. GROUP BY 로 중복을 없앤다 — 근거로 쓰는 것은
// "오류 이벤트가 있었나" 이지 몇 건이었나가 아니고, 이름을 세면 결과 크기가 이벤트 수에
// 비례해 커진다.
const classifyEventsSQL = `SELECT e.turn_id, e.event_name
FROM events e JOIN turns t ON t.id = e.turn_id
WHERE t.turn_index IS NOT NULL AND t.session_id IN (`

func loadEventRows(ctx context.Context, db sqlQuerier, ids []int64, turnRefs map[int64]*TurnSignals) (err error) {
	const op = "이벤트 분류 입력 조회"

	query := classifyEventsSQL + placeholders(len(ids)) +
		`) GROUP BY e.turn_id, e.event_name ORDER BY e.turn_id ASC, e.event_name ASC`
	rows, err := db.QueryContext(ctx, query, idArgs(ids)...)
	if err != nil {
		return queryErr(op, err)
	}
	defer closeRows(rows, op, &err)

	for rows.Next() {
		var (
			turnID int64
			name   string
		)
		if serr := rows.Scan(&turnID, &name); serr != nil {
			return queryErr(op, serr)
		}
		if ref, found := turnRefs[turnID]; found {
			ref.EventNames = append(ref.EventNames, name)
		}
	}
	return nil
}
