package vendorlimit

import (
	"context"
	"net/http"
	"time"

	"github.com/your-org/pulsemetry/internal/codexapp"
	"github.com/your-org/pulsemetry/internal/hostenv"
)

// Options 는 Collector의 구성이다. 영값이 운영 기본값이다.
type Options struct {
	// HomeDir 는 Claude 자격증명 파일을 찾을 홈 디렉터리다. 비어 있으면 hostenv로 판별한다.
	HomeDir string
	// Client 는 Claude 조회에 쓸 HTTP 클라이언트다. 비어 있으면 제한 시간이 있는 기본값이다.
	Client *http.Client
	// CodexClient 는 Codex App Server 조회 seam이다. 비어 있으면 기본 클라이언트를 만든다.
	CodexClient codexapp.RateLimitsReader

	// 아래 두 필드는 테스트가 시각과 Claude 모의 서버를 고정하는 비공개 seam이다.
	now           func() time.Time
	claudeBaseURL string
}

// VendorCollector 는 갱신 정책이 의존하는 최소 조회 계약이다.
type VendorCollector interface {
	CollectVendor(context.Context, Vendor) Result
}

// Collector 는 벤더 조회 자원을 소유해 여러 갱신에서 재사용한다.
type Collector struct {
	opts  Options
	codex *codexapp.Client
}

// NewCollector 는 장기 실행 수집기를 만든다.
func NewCollector(opts Options) *Collector {
	c := &Collector{opts: opts}
	if opts.CodexClient == nil {
		c.codex = codexapp.New(codexapp.Options{})
		c.opts.CodexClient = c.codex
	}
	return c
}

// Close 는 수집기가 소유한 Codex App Server를 종료한다.
func (c *Collector) Close() error {
	if c == nil || c.codex == nil {
		return nil
	}
	return c.codex.Close()
}

// CollectVendor 는 한 벤더만 조회한다. 벤더별 실패는 unavailable Result로 격리한다.
func (c *Collector) CollectVendor(ctx context.Context, vendor Vendor) Result {
	now := c.opts.now
	if now == nil {
		now = time.Now
	}
	a, ok := adapterFor(vendor, c.opts.claudeBaseURL)
	if !ok {
		return unavailable(vendor, ReasonInternal, "지원하지 않는 벤더다", now())
	}
	home, err := resolveHome(c.opts.HomeDir)
	if err != nil {
		return unavailable(vendor, ReasonInternal, "홈 디렉터리를 판별하지 못했다", now())
	}
	client := c.opts.Client
	if client == nil {
		client = newHTTPClient()
	}
	env := probeEnv{home: home, client: client, codex: c.opts.CodexClient, now: now}
	return safeProbe(ctx, a, env)
}

func adapterFor(vendor Vendor, claudeBaseURL string) (adapter, bool) {
	switch vendor {
	case VendorClaudeCode:
		return claudeAdapter{baseURL: claudeBaseURL}, true
	case VendorCodex:
		return codexAdapter{}, true
	default:
		return nil, false
	}
}

func resolveHome(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	env, err := hostenv.Detect()
	if err != nil {
		return "", err
	}
	return env.HomeDir, nil
}

// safeProbe 는 어댑터 패닉을 해당 벤더의 실패로 가둔다. 패닉 값은 비밀을 포함할 수 있어 버린다.
func safeProbe(ctx context.Context, a adapter, env probeEnv) (res Result) {
	defer func() {
		if recover() != nil {
			res = unavailable(a.vendor(), ReasonInternal, "조회 중 예기치 않게 실패했다", env.now())
		}
	}()
	return a.probe(ctx, env)
}
