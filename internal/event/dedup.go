package event

import (
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"io"
	"strconv"
)

// dedupKeyVersion 은 키 구성 규칙의 버전이다. 구성 요소를 바꾸면 올린다 —
// 그래야 규칙이 바뀐 뒤에 들어온 이벤트가 이전 규칙으로 저장된 행과 충돌하지 않고,
// dedup_key 만 보고도 어느 규칙으로 만들어졌는지 재현할 수 있다.
//
// pmdk1 → pmdk2: turn_key 와 call_key 를 키에 넣었다. 두 값이 빠져 있으면 같은 초에 같은
// 속성으로 일어난 서로 다른 도구 호출(같은 툴을 연달아 두 번 부르는 흔한 경우)이 같은
// record_hash 로 접혀 한 건이 조용히 사라진다.
const dedupKeyVersion = "pmdk2"

// DedupKey 는 events.dedup_key 값이다 (§5.5).
//
// §5.5 가 조합하라고 명시한 값 — vendor, installation_id, event_id, trace_id, span_id,
// timestamp, event kind, sequence — 을 전부 넣고, 여기에 session_id 와 속성 allowlist 전체를
// 더한다. 더한 이유는 메트릭 때문이다. 메트릭 데이터포인트에는 event_id 가 없어서 §5.5 의
// 목록만 쓰면 같은 순간에 나온 claude_code.token.usage 의 input·output 두 포인트가 같은 키를
// 갖는다. UNIQUE 제약이 그중 하나를 조용히 버리고 토큰 집계가 절반이 된다. 속성이 allowlist
// 구조체라 순회 순서가 고정돼 있어 이 확장이 결정론을 깨지 않는다.
//
// 수치(Measures)와 Temporality 는 넣지 않는다. 같은 이벤트가 재전송될 때 값은 같으므로
// 변별력이 없고, float 포맷 차이 하나로 같은 이벤트가 두 행이 되는 위험만 생긴다.
//
// 잡는 시나리오: exporter 재시도, Collector 재전송, 프로세스 재시작 후 재전송,
// Windows+WSL 이중 설정으로 같은 페이로드가 두 번 도착하는 경우.
// 못 잡는 시나리오: 상위가 배치를 다르게 잘라 Sequence 가 밀리는 경우.
// §5.5 대로 완전하지 않아도 집계가 크게 왜곡되지 않는 쪽을 택했다 — 확장한 키는 중복을
// 덜 접을 수는 있어도 서로 다른 이벤트를 하나로 접지는 않는다.
//
// 결과는 sha256 hex 다. 원문·경로가 키에 그대로 실리지 않고(속성은 이미 해시+basename 뿐이지만
// 키 자체도 되돌릴 수 없다), 길이가 고정돼 인덱스가 균일해진다.
func (e Event) DedupKey() string {
	h := sha256.New()
	writeField(h, dedupKeyVersion)
	writeField(h, e.Vendor)
	writeField(h, e.InstallationID)
	writeField(h, string(e.Signal))
	writeField(h, e.Name) // §5.5 의 "event kind"
	writeField(h, e.EventID)
	writeField(h, e.TraceID)
	writeField(h, e.SpanID)
	writeField(h, e.SessionID)
	// 턴·호출 식별자는 §5.5 목록에 없지만 변별력이 크다. 벤더가 이 값을 주면 같은 초에
	// 일어난 서로 다른 도구 호출이 확실히 갈린다.
	writeField(h, e.TurnKey)
	writeField(h, e.CallKey)
	writeField(h, strconv.FormatInt(int64(e.TS), 10))
	writeField(h, strconv.Itoa(e.Sequence))
	for _, v := range e.Attr.dedupFields() {
		writeField(h, v)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// writeField 는 길이 접두로 필드 경계를 고정한다. 구분자만 이어 붙이면
// ("a","bc") 와 ("ab","c") 가 같은 키가 돼 서로 다른 이벤트가 하나로 접힌다.
// hash.Hash 의 Write 는 에러를 내지 않는다고 문서가 보장하므로 반환값을 버린다.
func writeField(h hash.Hash, v string) {
	io.WriteString(h, strconv.Itoa(len(v)))
	io.WriteString(h, ":")
	io.WriteString(h, v)
}
