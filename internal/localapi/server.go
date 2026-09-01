// Package localapi 는 GUI 와 데몬 사이의 로컬 HTTP 계약이다.
//
// 경로·서버·클라이언트가 한 패키지에 있다. 나뉘어 있으면 한쪽만 고쳐도 컴파일이 통과해
// 런타임에 404 로 알게 된다 (internal/contract 와 같은 이유).
//
// 포트·인증·CORS 는 여기 없다. 로컬 네트워크 표면은 receiver 하나이고, 이 핸들러는
// 거기 마운트된다.
package localapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/your-org/pulsemetry/internal/dashboard/tray"
)

const (
	// TrayPath 는 트레이 퀵뷰 한 장을 받는 경로다.
	TrayPath = "/v1/tray"
	// TrayRefreshPath 는 GUI 의 "새로고침" 이 부르는 경로다.
	TrayRefreshPath = "/v1/tray/refresh"
)

// 조회 조건은 쿼리 파라미터로 받는다. GET 이라 본문이 없다.
const (
	paramTZ          = "tz"
	paramRecentLimit = "recent_limit"
	// paramManual 이 "1" 이면 사용자가 새로고침 버튼을 누른 것이다. 창이 열려서 나간 갱신과
	// 억제 등급이 다르다 (ADR 0014). 없으면 자동으로 읽는다 — 덜 세게 읽는 쪽이 안전하다.
	paramManual = "manual"
	// manualValue 는 paramManual 이 수동을 뜻할 때의 값이다.
	manualValue = "1"
)

// LimitRefresher 는 vendorlimit.Refresher가 만족하는 갱신 계약이다. 조회와 SQLite 쓰기까지 끝내고 돌아온다 —
// 그래서 응답이 온 시점에는 뒤이은 스냅샷 조회가 새 값을 읽는다.
//
// 메서드가 둘인 이유는 억제 등급이 둘이기 때문이다 (ADR 0014). 여기서 어느 쪽을 부를지만 고르고,
// 얼마나 억제할지는 전적으로 구현이 정한다 — 이 패키지에 임계값이 없어야 정책이 한 곳에 남는다.
type LimitRefresher interface {
	RefreshAuto(ctx context.Context) error
	RefreshManual(ctx context.Context) error
}

// TraySource 는 데몬의 스냅샷 조립기다 (tray.Builder).
type TraySource interface {
	Snapshot(ctx context.Context, q tray.Query) (tray.Snapshot, error)
}

// NewServer 는 데몬이 receiver 에 넘길 핸들러다. 요청이 여기 닿았다는 것은 receiver 가
// 이미 인증을 통과시켰다는 뜻이다.
func NewServer(refresher LimitRefresher, trays TraySource) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST "+TrayRefreshPath, func(w http.ResponseWriter, r *http.Request) {
		refresh := refresher.RefreshAuto
		if r.URL.Query().Get(paramManual) == manualValue {
			refresh = refresher.RefreshManual
		}
		if err := refresh(r.Context()); err != nil {
			http.Error(w, "vendor limit refresh failed", http.StatusServiceUnavailable)
			return
		}
		// 쿨다운에 걸려 벤더를 두드리지 않았어도 스냅샷은 그대로 내려간다. 호출자가 원하는 것은
		// "갱신했는가" 가 아니라 "지금 무엇을 그려야 하는가" 다 (ADR 0014).
		writeTray(w, r, trays)
	})

	mux.HandleFunc("GET "+TrayPath, func(w http.ResponseWriter, r *http.Request) {
		writeTray(w, r, trays)
	})

	return mux
}

func writeTray(w http.ResponseWriter, r *http.Request, trays TraySource) {
	q := tray.Query{TZ: r.URL.Query().Get(paramTZ)}
	if n, err := strconv.Atoi(r.URL.Query().Get(paramRecentLimit)); err == nil {
		q.RecentLimit = n
	}
	snap, err := trays.Snapshot(r.Context(), q)
	if err != nil {
		// 시간대 오타 같은 호출자 버그가 여기로 온다. 5xx 로 올리면 GUI 가
		// "데몬이 고장났다" 로 읽는다.
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, snap)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// 헤더가 이미 나갔으므로 상태 코드를 바꿀 수 없다. 연결이 끊긴 경우가 대부분이다.
		return
	}
}
