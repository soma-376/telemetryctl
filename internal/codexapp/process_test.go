package codexapp

import (
	"testing"
	"time"
)

func TestProcessShutdownJoinsReader(t *testing.T) {
	for _, mode := range []string{"normal", "flood"} {
		for _, force := range []bool{false, true} {
			name := mode + "/close"
			if force {
				name = mode + "/kill"
			}
			t.Run(name, func(t *testing.T) {
				for range 3 {
					p, err := startProcess(helperCommand(mode))
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(p.kill)
					if mode == "flood" {
						deadline := time.Now().Add(time.Second)
						for len(p.lines) < cap(p.lines) && time.Now().Before(deadline) {
							time.Sleep(time.Millisecond)
						}
						if len(p.lines) != cap(p.lines) {
							t.Fatal("출력 채널이 차지 않음")
						}
					}
					stopped := make(chan struct{})
					go func() {
						if force {
							p.kill()
						} else {
							_ = p.close()
						}
						close(stopped)
					}()
					select {
					case <-stopped:
					case <-time.After(3 * time.Second):
						t.Fatal("프로세스 종료 시간 초과")
					}
					select {
					case <-p.readDone:
					default:
						t.Fatal("종료 후 출력 읽기 고루틴이 남음")
					}
				}
			})
		}
	}
}
