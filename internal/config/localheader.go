package config

import "net/url"

// 로컬 수신기로 재배선된 벤더 설정에 붙는 두 번째 인증 헤더.
//
// # 왜 Authorization 만으로 부족한가
//
// 로컬 수신기의 인증은 3중이다 (internal/receiver/auth.go, ADR 0001). bearer 토큰이
// 맞아도 `X-Pulsemetry-Local: 1` 이 없으면 401 이다. 커스텀 헤더를 요구하는 목적은
// 브라우저의 simple request 를 배제하는 것이다 — 토큰은 벤더 설정 파일에 평문으로
// 들어가므로 같은 PC 에서 도는 웹 페이지가 읽어 갈 수 있고, 커스텀 헤더는 preflight 를
// 강제하는데 수신기는 OPTIONS 에 405 로 답한다.
//
// 그래서 **설정을 쓰는 쪽도 이 헤더를 함께 적어야 한다.** 이것을 빠뜨리면 Claude Code 와
// Codex 가 보내는 모든 배치가 조용히 401 로 사라진다.
//
// # 왜 receiver 의 상수를 쓰지 않고 복제하는가
//
// config 가 receiver 를 import 하면 enroll·status 경로까지 protobuf 디코더가 딸려
// 들어온다. installer 가 DefaultLocalPort 를 복제한 것과 같은 이유다
// (internal/installer/local.go). 대신 두 값이 어긋나면 localheader_test.go 의
// TestLocalIngestHeaderMatchesReceiver 가 잡는다 — 테스트에서만 receiver 를 import 한다.
const (
	// LocalIngestHeader 는 receiver.LocalHeader 와 같아야 한다.
	LocalIngestHeader = "X-Pulsemetry-Local"
	// LocalIngestHeaderValue 는 receiver.LocalHeaderValue 와 같아야 한다.
	LocalIngestHeaderValue = "1"
)

// isLocalEndpoint 는 endpoint 가 우리 로컬 수신기를 가리키는지 판단한다.
//
// # 왜 endpoint 에서 파생하는가
//
// "로컬인가" 를 별도 플래그로 받으면 진실원이 둘이 되고, 둘은 언젠가 어긋난다 —
// endpoint 는 로컬인데 헤더는 안 붙는 상태가 정확히 이 함수가 고치고 있는 버그다.
// endpoint 하나에서 파생시키면 그 상태를 만들 자리가 없어진다.
//
// 규칙이 성립하는 근거는 계약에 있다. contract.validOTLPEndpoint 는 http 를 리터럴
// 호스트 localhost 에만 허용하고 (internal/contract/manifest.go), 재배선은 언제나
// installer.LocalEndpoint 가 만든 http://localhost:<port> 를 쓴다. 따라서
// http + localhost ⟺ 로컬 수신기다.
//
// https://localhost 는 로컬로 보지 않는다. 우리 수신기는 평문 http 로만 뜨므로
// 그것은 회사가 운영하는 다른 무언가다.
//
// # 오탐과 미탐의 비용이 다르다
//
// 회사가 http://localhost 로 로컬 Collector 를 운영해서 헤더가 잘못 붙으면, 값 1 은
// 비밀이 아니고 OTLP 수신기는 모르는 헤더를 무시하므로 대가는 헤더 한 줄이다.
// 반대로 로컬인데 안 붙이면 텔레메트리가 로컬에도 회사에도 남지 않는다.
func isLocalEndpoint(endpoint string) bool {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return false
	}
	return parsed.Scheme == "http" && parsed.Hostname() == "localhost"
}
