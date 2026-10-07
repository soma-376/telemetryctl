package trayicon

import (
	"bytes"
	"image"
	"image/png"
	"testing"

	"github.com/your-org/pulsemetry/internal/dashboard/tray"
)

func decode(t *testing.T, b []byte) *image.NRGBA {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("PNG 디코드: %v", err)
	}
	n, ok := img.(*image.NRGBA)
	if !ok {
		t.Fatalf("이미지 형식 %T, want *image.NRGBA", img)
	}
	return n
}

// alphaAtUnit 은 뷰박스 좌표 (x, y) 가 떨어지는 픽셀의 알파(0~1)다. 32px·여백 없음 기준.
func alphaAtUnit(img *image.NRGBA, x, y float64) float64 {
	return float64(img.NRGBAAt(int(x*2), int(y*2)).A) / 255
}

func TestRenderSize(t *testing.T) {
	cases := map[Theme]int{Template: 44, Light: 32, Dark: 32}
	for theme, want := range cases {
		img := decode(t, Render(tray.Icon{Kind: tray.IconRing, Percent: 50}, theme))
		if b := img.Bounds(); b.Dx() != want || b.Dy() != want {
			t.Errorf("theme %d: %v, want %d×%d", theme, b, want, want)
		}
	}
}

// 링은 12시에서 시계 방향으로 찬다. 25% 면 3시 지점은 채워지고 9시 지점은 트랙(30%)만 남는다.
func TestRingFillsClockwiseFromTop(t *testing.T) {
	img := decode(t, Render(tray.Icon{Kind: tray.IconRing, Percent: 25}, Light))

	// 3시 방향 링 위 (8+5.6, 8) 를 피해 호의 중간인 1시 반 쪽을 본다.
	if a := alphaAtUnit(img, 12.0, 4.0); a < 0.9 {
		t.Errorf("채워진 호의 알파 = %.2f, want ≈1", a)
	}
	if a := alphaAtUnit(img, 2.4, 8.0); a < 0.2 || a > 0.4 {
		t.Errorf("빈 트랙의 알파 = %.2f, want ≈0.3", a)
	}
	// 링 바깥은 투명이다.
	if a := alphaAtUnit(img, 0.2, 0.2); a != 0 {
		t.Errorf("모서리 알파 = %.2f, want 0", a)
	}
}

// 길이 0 인 호도 round 캡이면 점으로 찍힌다. 0% 에서 12시 지점은 트랙만이어야 한다.
func TestZeroPercentHasNoCapDot(t *testing.T) {
	for _, kind := range []tray.IconKind{tray.IconRing, tray.IconAlert} {
		img := decode(t, Render(tray.Icon{Kind: kind, Percent: 0}, Light))
		if a := alphaAtUnit(img, 8.0, 2.4); a > 0.4 {
			t.Errorf("%s 0%% 의 12시 알파 = %.2f, want 트랙(≈0.3)", kind, a)
		}
	}
}

// 경고 상태에서도 남은 10% 만 그린다. 고정 94% 링으로 돌아가면 이 검사가 실패한다.
func TestAlertKeepsRemainingRing(t *testing.T) {
	img := decode(t, Render(tray.Icon{Kind: tray.IconAlert, Percent: 10}, Light))
	if a := alphaAtUnit(img, 8.0, 2.4); a < 0.9 {
		t.Errorf("남은 호의 알파 = %.2f, want ≈1", a)
	}
	if a := alphaAtUnit(img, 2.4, 8.0); a < 0.2 || a > 0.4 {
		t.Errorf("사용한 구간의 알파 = %.2f, want ≈0.3", a)
	}
	if a := alphaAtUnit(img, 8.0, 6.0); a < 0.9 {
		t.Errorf("경고 표시의 알파 = %.2f, want ≈1", a)
	}
}

func TestUnknownDiffersFromEmptyFullAndOffline(t *testing.T) {
	unknown := Render(tray.Icon{Kind: tray.IconUnknown}, Light)
	for _, icon := range []tray.Icon{
		{Kind: tray.IconRing, Percent: 0},
		{Kind: tray.IconRing, Percent: 100},
		{Kind: tray.IconOffline},
	} {
		if bytes.Equal(unknown, Render(icon, Light)) {
			t.Errorf("한도 미확인 그림이 %+v 와 같다", icon)
		}
	}
}

func TestThemeColors(t *testing.T) {
	cases := []struct {
		icon  tray.Icon
		theme Theme
		want  [3]uint8
	}{
		{tray.Icon{Kind: tray.IconRing, Percent: 100}, Light, [3]uint8{0x1F, 0x1F, 0x1F}},
		{tray.Icon{Kind: tray.IconRing, Percent: 100}, Dark, [3]uint8{0xFF, 0xFF, 0xFF}},
		{tray.Icon{Kind: tray.IconAlert, Percent: 5}, Light, [3]uint8{0xC7, 0x7A, 0x10}},
		{tray.Icon{Kind: tray.IconAlert, Percent: 5}, Dark, [3]uint8{0xF0, 0xA9, 0x3A}},
		// 템플릿은 OS 가 칠하므로 경고도 검정이다.
		{tray.Icon{Kind: tray.IconAlert, Percent: 5}, Template, [3]uint8{0, 0, 0}},
	}
	for _, tc := range cases {
		img := decode(t, Render(tc.icon, tc.theme))
		// 링의 12시 지점은 두 아이콘 모두 채워진 호 위다. 템플릿은 3단위 여백에 1단위=2px.
		x, y := 8.0*2, 2.4*2
		if tc.theme == Template {
			x, y = (8.0+3)*2, (2.4+3)*2
		}
		c := img.NRGBAAt(int(x), int(y))
		if [3]uint8{c.R, c.G, c.B} != tc.want || c.A == 0 {
			t.Errorf("%+v theme %d: %v, want %v", tc.icon, tc.theme, c, tc.want)
		}
	}
}

// 오프라인은 그룹 불투명도 0.55 를 넘지 않는다 — 진한 아이콘이 데몬 다운을 정상처럼 보이게 하면 안 된다.
func TestOfflineIsDimmed(t *testing.T) {
	img := decode(t, Render(tray.Icon{Kind: tray.IconOffline}, Light))
	var maxA uint8
	for i := 3; i < len(img.Pix); i += 4 {
		maxA = max(maxA, img.Pix[i])
	}
	if maxA == 0 || maxA > 141 { // 0.55 × 255 ≈ 140
		t.Errorf("오프라인 최대 알파 = %d, want 1~140", maxA)
	}
}
