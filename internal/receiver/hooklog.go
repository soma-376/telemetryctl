package receiver

import (
	"net"
	"net/http"
	"os"
	"time"
)

// observeHook 은 인증 전 도착과 응답을 기록한다. 본문·헤더·쿼리는 기록하지 않는다.
// 세션 ID는 인증·파싱을 마친 pipeline의 로그에서 확인한다.
func (rc *Receiver) observeHook(w *http.ResponseWriter, r *http.Request) func() {
	started := time.Now()
	local := ""
	if addr, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
		local = addr.String()
	}
	response := &hookResponse{ResponseWriter: *w}
	*w = response
	rc.logf("세션 훅 HTTP 도착: pid=%d local=%q remote=%q method=%q path=%q",
		os.Getpid(), local, r.RemoteAddr, r.Method, r.URL.Path)
	return func() {
		status := response.status
		if status == 0 {
			status = http.StatusOK
		}
		rc.logf("세션 훅 HTTP 응답: pid=%d local=%q remote=%q path=%q status=%d duration_ms=%d",
			os.Getpid(), local, r.RemoteAddr, r.URL.Path, status, time.Since(started).Milliseconds())
	}
}

type hookResponse struct {
	http.ResponseWriter
	status int
}

func (w *hookResponse) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *hookResponse) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func (w *hookResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }
