package vendorlimit

import (
	"context"
	"net/http"
	"time"
)

// probeEnv 는 어댑터 하나가 조회에 쓰는 바깥 자원 전부다.
//
// 어댑터가 os.UserHomeDir·http.DefaultClient·time.Now 를 직접 부르지 않게 해서, 테스트가
// 실제 홈 디렉터리나 실제 벤더 엔드포인트에 닿을 길을 아예 없앤다.
type probeEnv struct {
	home   string
	client *http.Client
	now    func() time.Time
}

// adapter 는 벤더 하나의 조회 절차다.
//
// **probe 는 error 를 반환하지 않는다.** 실패도 Result 로 표현하는 것이 이 패키지의 계약이라,
// 어댑터가 실수로 오류를 위로 던져 집계 전체를 중단시킬 길을 타입 수준에서 막는다.
type adapter interface {
	vendor() Vendor
	probe(ctx context.Context, env probeEnv) Result
}

// resolveResetTimes 는 창의 초기화 시각을 절대·상대 양쪽으로 채운다.
//
// 벤더는 한쪽만 준다 — Claude 는 절대 시각을, Codex 는 남은 초를. 화면이 벤더마다 분기하지
// 않도록 여기서 나머지 한쪽을 파생시킨다. **파생값은 우리 시계에 의존한다.** 사용자 시계가
// 틀어져 있으면 파생값도 같이 틀어지므로, 벤더가 준 쪽은 절대 덮어쓰지 않는다.
//
// RFC3339 로 못 읽는 시각은 버린다. 화면에 그대로 흘려보내면 파싱은 화면의 몫이 되고,
// 거기서 실패하면 원인이 우리에게서 멀어진다.
func resolveResetTimes(w *Window, now time.Time) {
	if w.ResetsAt != "" {
		ts, err := time.Parse(time.RFC3339, w.ResetsAt)
		if err != nil {
			w.ResetsAt = ""
		} else {
			w.ResetsAt = formatTime(ts)
			if w.ResetsInSeconds == 0 {
				if remain := int64(ts.Sub(now).Seconds()); remain > 0 {
					w.ResetsInSeconds = remain
				}
			}
		}
	}
	if w.ResetsAt == "" && w.ResetsInSeconds > 0 {
		w.ResetsAt = formatTime(now.Add(time.Duration(w.ResetsInSeconds) * time.Second))
	}
	if w.ResetsInSeconds < 0 {
		w.ResetsInSeconds = 0
	}
}
