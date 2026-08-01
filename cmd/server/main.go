// Command pulsemetry-server 는 초대 코드 기반 enrollment 서버다 (MVP, 인메모리 저장소).
// 로컬: `go run ./cmd/pulsemetry-server` → http://localhost:8080. 시드 초대 코드는 TEST-1234.
package main

import (
	"log"
	"net/http"
	"os"

	"github.com/your-org/pulsemetry/internal/server/enrollment"
	"github.com/your-org/pulsemetry/internal/server/httpapi"
	"github.com/your-org/pulsemetry/internal/server/store"
)

func main() {
	st := store.NewMemory()
	st.Seed()
	svc := enrollment.NewService(st)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/enroll", httpapi.Handler(svc))
	mux.HandleFunc("GET /v1/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// 한 줄 설치 부트스트랩:  irm <server>/windows | iex  /  curl -fsSL <server>/unix | sh
	mux.HandleFunc("GET /windows", httpapi.WindowsHandler())
	mux.HandleFunc("GET /unix", httpapi.UnixHandler())
	// 바이너리 서빙: 부트스트랩이 /bin/pulsemetry_<os>_<arch> 를 내려받는다.
	mux.Handle("GET /bin/", http.StripPrefix("/bin/", http.FileServer(http.Dir(distDir()))))

	addr := ":" + port()
	log.Printf("server is running on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

// port 는 PORT 환경변수(Cloud Run 등)를 우선하고, 없으면 8080 을 쓴다.
func port() string {
	if p := os.Getenv("PORT"); p != "" {
		return p
	}
	return "8080"
}

// distDir 은 /bin/ 에서 서빙할 바이너리 디렉터리다. PULSEMETRY_DIST_DIR 우선, 없으면 ./dist.
func distDir() string {
	if d := os.Getenv("PULSEMETRY_DIST_DIR"); d != "" {
		return d
	}
	return "dist"
}
