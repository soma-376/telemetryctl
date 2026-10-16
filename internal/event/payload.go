package event

// MaxPayloadBytes는 이벤트별 수신 JSON의 로컬 저장 상한이다 (ADR 0025).
const MaxPayloadBytes = 64 << 10
