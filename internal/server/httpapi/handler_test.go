package httpapi

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/your-org/pulsemetry/internal/contract"
	"github.com/your-org/pulsemetry/internal/server/enrollment"
	"github.com/your-org/pulsemetry/internal/server/store"
)

func testHandler() http.HandlerFunc {
	st := store.NewMemory()
	st.Seed()
	return Handler(enrollment.NewService(st))
}

func post(h http.HandlerFunc, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/enroll", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func TestEnrollHappyPath(t *testing.T) {
	rec := post(testHandler(), `{"invite":"TEST-1234","operating_environment":"linux"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 서버 응답이 클라이언트와 동일한 봉투 계약(ParseEnrollment)을 통과해야 한다.
	env, err := contract.ParseEnrollment(rec.Body.Bytes())
	if err != nil {
		t.Fatalf("응답이 봉투 계약 위반: %v", err)
	}
	if env.InstallationID == "" || env.InstallationToken == "" {
		t.Error("installation_id/token 미발급")
	}
	if env.Manifest.OTLP.Endpoint != "https://telemetry.acme.example.com" {
		t.Errorf("manifest endpoint=%q", env.Manifest.OTLP.Endpoint)
	}
}

func TestEnrollInvalidInvite(t *testing.T) {
	rec := post(testHandler(), `{"invite":"NOPE"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("잘못된 초대는 401 이어야 함, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestEnrollExhausted(t *testing.T) {
	st := store.NewMemory()
	st.Seed()
	// 시드 초대(MaxUses=100)를 전부 소진시킨다.
	for i := range 100 {
		if _, err := st.ClaimInvite("TEST-1234"); err != nil {
			t.Fatalf("소진 전 %d회차 실패: %v", i, err)
		}
	}
	rec := post(Handler(enrollment.NewService(st)), `{"invite":"TEST-1234"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("소진된 초대는 409 이어야, got %d (%s)", rec.Code, rec.Body.String())
	}
}
