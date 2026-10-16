package config

// 벤더 설정에 지금 적혀 있는 bearer 토큰을 되읽는다.
//
// # 왜 이런 것이 필요한가
//
// `local enable` 은 벤더 설정의 Authorization 헤더를 회사 telemetry token 에서 로컬
// ingest 토큰으로 바꾼다. 그런데 `local disable` 은 **정확히 원래대로** 되돌려야 한다 —
// 그것이 사용자의 탈출구다. 되돌리려면 회사 토큰이 필요한데, 그 토큰은 state.json 에
// 저장되지 않는다 (§4.5: 상태 파일에 비밀 금지). 유일하게 남아 있는 곳이 벤더 설정
// 자신이므로, 재배선 직전에 여기서 읽어 키링으로 옮겨 둔다.
//
// 서버에서 새 토큰을 재발급받는 방법(installer.Reconnect)도 있지만 그것은 네트워크에
// 의존한다. 탈출구가 회사 서버의 가용성에 묶이면 안 된다.
//
// # 실패는 조용하다
//
// 파일이 없거나, 우리 키가 없거나, 형식이 다르면 ("", nil) 이다. 여기서 오류를 만들어
// 봐야 호출자가 할 수 있는 일이 없다 — 토큰을 못 찾았다는 사실 자체가 결과다.
// 파일이 있는데 파싱이 깨진 경우만 오류로 올린다. 그때는 병합 자체가 실패할 것이므로
// 미리 알려 주는 편이 낫다.

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/BurntSushi/toml"
)

const (
	claudeHeadersKey  = "OTEL_EXPORTER_OTLP_HEADERS"
	authorizationName = "Authorization"
	bearerPrefix      = "Bearer "
)

// ReadClaudeToken 은 Claude settings.json 의 env.OTEL_EXPORTER_OTLP_HEADERS 에서
// bearer 토큰을 꺼낸다. 값의 형식은 claudeEnv 가 쓰는 "Authorization=Bearer <token>" 이다.
func ReadClaudeToken(path string) (string, error) {
	raw, existed, err := readFileIfExists(path)
	if err != nil || !existed || len(raw) == 0 {
		return "", err
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return "", fmt.Errorf("parse Claude settings %s: %w", path, err)
	}
	env, _ := root["env"].(map[string]any)
	headers, _ := env[claudeHeadersKey].(string)
	return bearerFromHeaderList(headers), nil
}

// ReadCodexToken 은 Codex config.toml 의 [otel.exporter.<id>.headers] 에서
// Authorization 값을 꺼낸다. exporter id 는 프로토콜에 따라 otlp-http 또는 otlp-grpc 라
// 어느 쪽이든 잡도록 순회한다.
func ReadCodexToken(path string) (string, error) {
	raw, existed, err := readFileIfExists(path)
	if err != nil || !existed || len(raw) == 0 {
		return "", err
	}
	var root map[string]any
	if err := toml.Unmarshal(raw, &root); err != nil {
		return "", fmt.Errorf("parse Codex config %s: %w", path, err)
	}
	otel, _ := root["otel"].(map[string]any)
	exporters, _ := otel["exporter"].(map[string]any)
	for _, entry := range exporters {
		exporter, _ := entry.(map[string]any)
		headers, _ := exporter["headers"].(map[string]any)
		value, _ := headers[authorizationName].(string)
		if token := trimBearer(value); token != "" {
			return token, nil
		}
	}
	return "", nil
}

// bearerFromHeaderList 는 "K=V,K=V" 형태의 OTEL_EXPORTER_OTLP_HEADERS 에서
// Authorization 항목의 bearer 토큰만 떼어 낸다.
func bearerFromHeaderList(list string) string {
	for _, part := range strings.Split(list, ",") {
		name, value, ok := strings.Cut(part, "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(name), authorizationName) {
			continue
		}
		if token := trimBearer(value); token != "" {
			return token
		}
	}
	return ""
}

// trimBearer 는 "Bearer <token>" 에서 토큰만 남긴다. 스킴은 대소문자를 가리지 않는다.
func trimBearer(value string) string {
	v := strings.TrimSpace(value)
	if len(v) < len(bearerPrefix) || !strings.EqualFold(v[:len(bearerPrefix)], bearerPrefix) {
		return ""
	}
	return strings.TrimSpace(v[len(bearerPrefix):])
}
