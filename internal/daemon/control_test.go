package daemon

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/credential"
	"github.com/your-org/pulsemetry/internal/instancelock"
	"github.com/your-org/pulsemetry/internal/localapi"
	"github.com/your-org/pulsemetry/internal/runtimeinfo"
	"github.com/zalando/go-keyring"
)

func TestControlShutdownDrainsAndReleasesDirectory(t *testing.T) {
	keyring.MockInit()
	if err := credential.Set(credential.AccountLocalControl, "control-test-only"); err != nil {
		t.Fatal(err)
	}
	h := start(t, harnessOptions{daemon: func(o *Options) { o.ControlToken = "control-test-only" }})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := localapi.StopDaemon(ctx, h.dataDir); err != nil {
		t.Fatal(err)
	}
	h.stop()
	if _, err := os.Stat(runtimeinfo.PathIn(h.dataDir)); !os.IsNotExist(err) {
		t.Fatal("종료 후 runtime 파일이 남았다")
	}
	l, err := instancelock.Acquire(h.dataDir)
	if err != nil {
		t.Fatal("종료 후 데이터 잠금 미해제:", err)
	}
	_ = l.Close()
}
