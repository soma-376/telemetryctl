package httpapi

import (
	"net/http"

	"github.com/your-org/pulsemetry/internal/server/enrollment"
)

// NewRouter wires the server's HTTP endpoints and dependencies.
func NewRouter(svc *enrollment.Service, distDir string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/healthz", healthHandler)
	mux.HandleFunc("POST /v1/enroll", NewEnrollHandler(svc))
	mux.Handle("GET /windows", RequireEnrollmentCode(svc, NewWindowsHandler()))
	mux.Handle("GET /unix", RequireEnrollmentCode(svc, NewUnixHandler()))
	mux.Handle("GET /bin/", http.StripPrefix("/bin/", http.FileServer(http.Dir(distDir))))
	return mux
}
