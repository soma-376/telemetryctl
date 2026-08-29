package dashboard

// 재시도 집계 (PROJ-91).
//
// # 추측하지 않는다
//
// 재시도는 **이벤트 payload 가 명시한 것만** 센다. 같은 도구가 연달아 두 번 나왔다거나,
// 실패한 호출 뒤에 같은 이름의 호출이 있다는 이유로 재시도라고 부르지 않는다. 그런
// 휴리스틱은 반복 작업(같은 파일을 여러 번 Edit)과 진짜 재시도를 구분할 방법이 없고,
// 한번 화면에 뜨면 어느 쪽이었는지 되짚을 근거가 남지 않는다. ADR 0005 가 abandoned 를
// "휴리스틱이라 오판할 수 있으므로 지표로 쓰지 않는다" 고 제한한 것과 같은 이유다.
//
// # 지금은 항상 0 이다
//
// v3 의 쓰기 경로는 events.payload 를 **항상 NULL 로 둔다**(store/resolve.go 의
// insertEventSQL, docs/sqlite-schema/events.md). 원본 OTLP 바이트를 붙들고 있는 경로가
// 없기 때문이다. 그래서 이 집계는 실사용에서 아무것도 세지 못한다.
//
// 그래도 구현해 두는 이유는, payload 를 쓰기 시작하는 날 이 자리를 다시 설계하지 않기
// 위해서다. 계약(어떤 키를 재시도로 읽는가)을 지금 못박아 두면 쓰기 쪽은 그 키를 채우기만
// 하면 된다. 테스트는 payload 를 SQL 로 직접 심어(jsonb(?)) 이 경로를 실제로 통과시킨다.
//
// # 읽는 키 (계약)
//
// 키는 payload 최상위와 attributes 객체 **두 곳**에서만 찾는다. 더 깊은 곳을 뒤지면
// 어디에 무엇을 넣어도 세지는 셈이라 계약이 아니게 된다.
//
//	attempt · attempt_number   시도 번호. n 번째 시도는 재시도 n-1 회다 (1 번째는 0)
//	retry_count · retries      재시도 횟수 그대로
//	retry · is_retry · retried  참이면 재시도 1 회
//
// attempt 계열의 이름은 otlpdecode/attrs.go 가 event.Measures.Attempt 로 받는 속성과
// 같다 — 같은 사실을 두 이름으로 부르지 않기 위해서다.
//
// 여러 키가 함께 있으면 **가장 큰 값**을 쓰고 더하지 않는다. attempt=3 과 retry_count=2 는
// 같은 사실의 두 표기이므로 더하면 정확히 두 배가 된다.

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"

	"github.com/your-org/pulsemetry/internal/pricing"
)

// attemptKeys 는 시도 번호를 담는 키다. n 번째 시도 = 재시도 n-1 회.
var attemptKeys = []string{"attempt", "attempt_number"}

// retryCountKeys 는 재시도 횟수를 그대로 담는 키다.
var retryCountKeys = []string{"retry_count", "retries"}

// retryFlagKeys 는 "재시도인가" 를 참/거짓으로 담는 키다. 참이면 1 회로 센다.
var retryFlagKeys = []string{"retry", "is_retry", "retried"}

// nestedKey 는 최상위 다음으로 들여다보는 유일한 객체다. OTLP 의 속성 묶음이 여기 온다.
const nestedKey = "attributes"

// retryEventsSQL 은 payload 가 있는 이벤트만 읽는다.
//
// json(payload) 는 JSONB 를 텍스트 JSON 으로 되돌린다 — payload 는 BLOB 이고
// CHECK 가 json_valid(payload, 8) 이라 JSONB 로만 저장된다 (events 스키마 문서).
//
// payload IS NOT NULL 이 사실상 모든 행을 걸러 낸다(위 머리말). 쓰기 경로가 payload 를
// 채우기 시작하면 이 질의가 세션의 모든 이벤트를 훑게 되므로, 그때 상한을 다시 봐야 한다.
const retryEventsSQL = `SELECT e.turn_id, json(e.payload)
FROM events e
WHERE e.turn_id IN (` + metricsTurnScope + `) AND e.payload IS NOT NULL`

func collectRetries(ctx context.Context, db sqlQuerier, id int64, _ pricing.Table, index *turnIndex) (err error) {
	const op = "재시도 집계 조회"
	rows, err := db.QueryContext(ctx, retryEventsSQL, id)
	if err != nil {
		return queryErr(op, err)
	}
	defer closeRows(rows, op, &err)

	for rows.Next() {
		var (
			turnID  int64
			payload []byte
		)
		if serr := rows.Scan(&turnID, &payload); serr != nil {
			return queryErr(op, serr)
		}
		if n := retriesInPayload(payload); n > 0 {
			index.at(turnID).Retries += n
		}
	}
	return nil
}

// retriesInPayload 는 payload 한 건이 **명시한** 재시도 횟수다. 명시가 없으면 0 이다.
//
// 순수 함수라 표 주도 테스트로 규칙 전체를 고정할 수 있다.
func retriesInPayload(payload []byte) int64 {
	if len(bytes.TrimSpace(payload)) == 0 {
		return 0
	}
	dec := json.NewDecoder(bytes.NewReader(payload))
	// UseNumber 가 없으면 큰 정수가 float64 를 거치며 마지막 자리를 잃는다.
	dec.UseNumber()

	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		// 읽을 수 없는 payload 는 "재시도 정보가 없다" 로 본다. 저장 시 CHECK 를 통과한
		// JSON 이므로 여기 오는 것은 객체가 아닌 JSON(배열·스칼라)뿐이다.
		return 0
	}
	best := retriesInObject(doc)
	if nested, ok := doc[nestedKey].(map[string]any); ok {
		if n := retriesInObject(nested); n > best {
			best = n
		}
	}
	return best
}

// retriesInObject 는 객체 한 겹에서 재시도 횟수를 읽는다. 여러 키가 있으면 가장 큰 값이다.
func retriesInObject(obj map[string]any) int64 {
	var best int64
	for _, key := range attemptKeys {
		if n, ok := asInt64(obj[key]); ok && n > 1 {
			best = max(best, n-1)
		}
	}
	for _, key := range retryCountKeys {
		if n, ok := asInt64(obj[key]); ok && n > 0 {
			best = max(best, n)
		}
	}
	for _, key := range retryFlagKeys {
		if v, ok := asBool(obj[key]); ok && v {
			best = max(best, 1)
		}
	}
	return best
}

// asInt64 는 JSON 수치를 정수로 옮긴다. 문자열도 받는다 — OTLP/JSON 왕복에서 int64 가
// 문자열이 되는 것이 정상이고, otlpdecode 도 같은 관용을 갖는다.
func asInt64(v any) (int64, bool) {
	switch t := v.(type) {
	case json.Number:
		n, err := t.Int64()
		if err != nil {
			// 3.0 처럼 소수점이 붙은 표기까지는 받는다. 3.5 는 시도 번호일 수 없다.
			f, ferr := t.Float64()
			if ferr != nil || f != float64(int64(f)) {
				return 0, false
			}
			return int64(f), true
		}
		return n, true
	case string:
		n, err := strconv.ParseInt(t, 10, 64)
		if err != nil {
			return 0, false
		}
		return n, true
	default:
		return 0, false
	}
}

// asBool 은 참/거짓 표기를 옮긴다. 수치는 받지 않는다 — retry: 1 이 "참" 인지 "1 회" 인지
// 알 수 없고, 추측하면 이 파일의 첫 문장을 어기게 된다. 횟수는 retry_count 로 온다.
func asBool(v any) (bool, bool) {
	switch t := v.(type) {
	case bool:
		return t, true
	case string:
		b, err := strconv.ParseBool(t)
		if err != nil {
			return false, false
		}
		return b, true
	default:
		return false, false
	}
}
