package httpapi

import (
	"errors"
	"net/http"

	"github.com/your-org/pulsemetry/internal/server/enrollment"
)

// RequireEnrollmentCode allows the next handler to run only for a valid code.
func RequireEnrollmentCode(svc *enrollment.Service, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		if code == "" {
			writeErr(w, http.StatusBadRequest, "code_required")
			return
		}
		if !validEnrollmentCodeFormat(code) {
			http.Error(w, "초대 코드가 유효하지 않습니다.", http.StatusUnauthorized)
			return
		}

		if err := svc.ValidateCode(r.Context(), code); err != nil {
			switch {
			case errors.Is(err, enrollment.ErrCodeNotFound), errors.Is(err, enrollment.ErrCodeRevoked):
				writeErr(w, http.StatusUnauthorized, "code_invalid")
			case errors.Is(err, enrollment.ErrCodeExpired):
				writeErr(w, http.StatusUnauthorized, "code_expired")
			case errors.Is(err, enrollment.ErrCodeExhausted):
				writeErr(w, http.StatusConflict, "code_exhausted")
			default:
				writeErr(w, http.StatusInternalServerError, "internal_error")
			}
			return
		}

		next.ServeHTTP(w, r)
	})
}

func validEnrollmentCodeFormat(code string) bool {
	if len(code) < 8 || len(code) > 128 {
		return false
	}
	for _, ch := range code {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') ||
			(ch >= '0' && ch <= '9') || ch == '-' || ch == '_' {
			continue
		}
		return false
	}
	return true
}
