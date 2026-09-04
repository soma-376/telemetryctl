package codexapp

import "errors"

var (
	ErrUnavailable      = errors.New("Codex App Server를 사용할 수 없음")
	ErrProtocol         = errors.New("Codex App Server 프로토콜 오류")
	ErrClosed           = errors.New("Codex App Server 클라이언트가 닫힘")
	ErrThreadIDRequired = errors.New("Codex 스레드 ID가 필요함")
)
