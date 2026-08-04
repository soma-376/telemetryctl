package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/your-org/pulsemetry/internal/contract"
	"github.com/your-org/pulsemetry/internal/server/enrollment"
)

func NewEnrollHandler(svc *enrollment.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req contract.EnrollRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid_request")
			return
		}

		code := req.Code
		if code == "" {
			code = req.Invite
		}
		platform := req.Platform
		if platform == "" {
			platform = req.OperatingEnvironment
		}
		clientVersion := req.ClientVersion
		if clientVersion == "" {
			clientVersion = req.InstallerVersion
		}
		if code == "" || platform == "" || req.Architecture == "" {
			writeErr(w, http.StatusBadRequest, "invalid_request")
			return
		}

		result, err := svc.Enroll(r.Context(), enrollment.EnrollCommand{
			Code:          code,
			Platform:      platform,
			Architecture:  req.Architecture,
			Hostname:      req.Hostname,
			ClientVersion: clientVersion,
		})
		if err != nil {
			writeEnrollError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, result)
	}
}

func writeEnrollError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, enrollment.ErrInvalidPlatform):
		writeErr(w, http.StatusBadRequest, "invalid_platform")
	case errors.Is(err, enrollment.ErrCodeNotFound), errors.Is(err, enrollment.ErrCodeRevoked):
		writeErr(w, http.StatusUnauthorized, "invite_invalid")
	case errors.Is(err, enrollment.ErrCodeExpired):
		writeErr(w, http.StatusGone, "invite_expired")
	case errors.Is(err, enrollment.ErrCodeExhausted):
		writeErr(w, http.StatusConflict, "invite_already_used")
	case errors.Is(err, enrollment.ErrManifestNotFound):
		writeErr(w, http.StatusConflict, "active_manifest_not_found")
	default:
		writeErr(w, http.StatusInternalServerError, "internal_error")
	}
}
