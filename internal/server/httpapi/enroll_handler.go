package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/your-org/pulsemetry/internal/contract"
	"github.com/your-org/pulsemetry/internal/server/enrollment"
	"github.com/your-org/pulsemetry/internal/server/store"
)

// Handler 는 POST /v1/enroll 핸들러를 만든다.
func Handler(svc *enrollment.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req contract.EnrollRequest

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid_request")
			return
		}
		
		env, err := svc.Enroll(req)
		if err != nil {
			switch {
			case errors.Is(err, store.ErrInviteNotFound), errors.Is(err, store.ErrInviteRevoked):
				writeErr(w, http.StatusUnauthorized, "invite_invalid")
			case errors.Is(err, store.ErrInviteExhausted):
				writeErr(w, http.StatusConflict, "invite_exhausted")
			default:
				writeErr(w, http.StatusInternalServerError, "internal_error")
			}
			return
		}
		writeJSON(w, http.StatusOK, env)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, errCode string) {
	writeJSON(w, code, map[string]string{"error": errCode})
}
