package vendorlimit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const (
	// defaultTimeout 은 사용 한도 조회 하나에 허용하는 시간이다.
	//
	// 이 조회는 부가 정보다. 상대가 느리다고 화면 전체가 멈추면 안 되고, 데몬 안에서
	// 주기적으로 부를 때는 다음 주기와 겹치지 않아야 한다. 짧게 잡고 실패는 unavailable 로 둔다.
	defaultTimeout = 10 * time.Second

	// maxResponseBytes 는 응답 본문 상한이다. 상대가 무엇을 보내든 우리 메모리는 우리가 정한다.
	maxResponseBytes = 1 << 20
)

// 전송 계층이 내는 오류의 종류. 어댑터는 문자열이 아니라 이 값으로 Reason 을 고른다.
var (
	errNetwork      = errors.New("요청이 상대에 닿지 못함")
	errUnauthorized = errors.New("상위가 인증을 거부함")
	errStatus       = errors.New("상위가 2xx 가 아닌 응답을 줌")
	errUnrecognized = errors.New("상위 응답이 아는 모양이 아님")
)

// newHTTPClient 는 이 패키지 기본 클라이언트다. **타임아웃 없는 클라이언트를 쓰지 않는다** —
// 상대가 응답하지 않으면 호출 고루틴이 영원히 잠긴다.
func newHTTPClient() *http.Client {
	return &http.Client{Timeout: defaultTimeout}
}

// transportReason 은 전송 오류를 화면이 분기할 Reason 으로 옮긴다.
//
// 401·403 을 ReasonTokenExpired 로 보내는 것이 중요하다. 우리는 토큰 만료를 파일에서
// 확실히 알 수 없고(Codex 는 만료 시각을 안 쓴다), 사용자가 할 일은 어느 쪽이든 같다 —
// 벤더 CLI 로 다시 로그인하는 것. 우리가 갱신하지 않기로 한 결정의 자연스러운 귀결이다.
func transportReason(err error) Reason {
	switch {
	case errors.Is(err, errUnauthorized):
		return ReasonTokenExpired
	case errors.Is(err, errNetwork):
		return ReasonNetwork
	case errors.Is(err, errStatus):
		return ReasonUpstreamStatus
	case errors.Is(err, errUnrecognized):
		return ReasonResponseUnrecognized
	default:
		return ReasonInternal
	}
}

// getJSON 은 Bearer 인증으로 GET 하고 본문을 out 에 디코드한다.
//
// # 토큰이 지나가는 유일한 통로
//
// 토큰이 원값으로 등장하는 곳은 아래 Authorization 헤더 조립 한 줄뿐이다. 그 아래로는
// 어떤 오류에도 토큰이 실리지 않게 한다.
//   - 전송 오류(*url.Error)는 URL 을 통째로 담아 오므로 벗겨서 원인만 남긴다.
//   - 상태 코드 오류에는 **응답 본문을 싣지 않는다.** 상위가 요청을 되비추는 구현이면
//     본문에 우리 헤더가 그대로 들어 있을 수 있다.
//   - 마지막으로 stripSecret 을 한 번 더 걸어 둔다.
func getJSON(ctx context.Context, client *http.Client, endpoint string, tok Token, headers map[string]string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("%w: 요청을 만들지 못함", errNetwork)
	}
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		if v != "" {
			req.Header.Set(k, v)
		}
	}
	req.Header.Set("Authorization", "Bearer "+tok.reveal())

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", errNetwork, stripSecret(sanitizeErr(err), tok.reveal()))
	}
	defer func() {
		// 본문을 끝까지 비워 줘야 연결이 재사용된다. 상한은 걸어 둔 채로 버린다.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes))
		resp.Body.Close()
	}()

	switch {
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%w (HTTP %d)", errUnauthorized, resp.StatusCode)
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return fmt.Errorf("%w (HTTP %d)", errStatus, resp.StatusCode)
	}

	dec := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes))
	if err := dec.Decode(out); err != nil {
		// 디코드 오류 메시지는 실패 지점의 본문 조각을 담는다. 본문은 신뢰할 수 없으므로 버린다.
		return fmt.Errorf("%w: 본문 디코드 실패", errUnrecognized)
	}
	return nil
}

// sanitizeErr 는 *url.Error 를 벗겨 URL 을 버린다. URL 에는 질의 문자열이나 userinfo 형태로
// 비밀이 실릴 수 있고, %v 한 번이면 그대로 로그에 남는다 (internal/forward 와 같은 이유).
func sanitizeErr(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%s 실패: %w", ue.Op, ue.Err)
	}
	return err
}
