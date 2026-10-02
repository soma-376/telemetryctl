// Package trayicon 은 트레이 아이콘 PNG 를 런타임에 그린다.
//
// 그림은 디자인 핸드오프의 tray-ring·tray-alert·tray-offline SVG(16×16 뷰박스)를 옮긴
// 것이다. 미리 그려 둔 PNG 를 임베드하지 않는 이유는 사용률 눈금과 테마 수만큼 파일이
// 곱으로 늘기 때문이다. 도형이 원·호·선분뿐이라 각 도형까지의 거리(SDF)로 픽셀의 덮임
// 정도를 구하면 래스터라이저 없이 표준 라이브러리만으로 안티앨리어싱된 그림이 나온다.
package trayicon

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"

	"github.com/your-org/pulsemetry/internal/dashboard/tray"
)

// Theme 은 아이콘을 칠할 배경 맥락이다.
type Theme int

const (
	// Template 은 macOS 메뉴바용이다. 색은 OS 가 칠하므로 알파만 의미가 있다.
	Template Theme = iota
	// Light 는 밝은 작업 표시줄·패널용이다 (Windows·리눅스).
	Light
	// Dark 는 어두운 작업 표시줄·패널용이다 (Windows·리눅스).
	Dark
)

// 뷰박스 16 기준의 기하. 핸드오프 SVG 의 숫자를 그대로 쓴다.
const (
	center      = 8.0
	ringRadius  = 5.6
	ringHalf    = 1.1 // stroke-width 2.2
	trackAlpha  = 0.3
	dotRadius   = 1.4
	alertSweep  = 33.075 / 35.186 // 경고 아이콘의 링은 94% 로 고정이다
	offlineHalf = 0.8             // stroke-width 1.6
	offlineDash = 2.2
	offlineGap  = 1.8
	offlineOp   = 0.55
)

type palette struct{ fg, alert color.NRGBA }

var palettes = map[Theme]palette{
	Template: {fg: rgb(0x000000), alert: rgb(0x000000)},
	Light:    {fg: rgb(0x1F1F1F), alert: rgb(0xC77A10)},
	Dark:     {fg: rgb(0xFFFFFF), alert: rgb(0xF0A93A)},
}

func rgb(v uint32) color.NRGBA {
	return color.NRGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 0xFF}
}

// Render 는 아이콘 한 장을 PNG 로 그린다.
//
// macOS 는 Wails 가 이미지를 메뉴바 두께(22pt)로 늘리므로, 16pt 글리프 둘레에 3pt 씩 여백을
// 둔 22pt 캔버스를 @2x(44px)로 그린다. 그래야 다른 메뉴바 아이콘과 크기·선명도가 맞는다.
// Windows·리눅스는 OS 가 트레이 크기로 줄이므로 여백 없이 32px 로 그린다.
func Render(icon tray.Icon, theme Theme) []byte {
	size, pad := 32, 0.0
	if theme == Template {
		size, pad = 44, 3.0
	}
	img := draw(icon, theme, size, pad)
	var buf bytes.Buffer
	// 메모리 버퍼에 쓰는 PNG 인코딩은 실패할 경로가 없다.
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func draw(icon tray.Icon, theme Theme, size int, pad float64) *image.NRGBA {
	pal := palettes[theme]
	ink := pal.fg
	if icon.Kind == tray.IconAlert {
		ink = pal.alert
	}
	units := 16 + 2*pad
	scale := float64(size) / units // 뷰박스 1 단위가 몇 픽셀인가

	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			// 픽셀 중심을 뷰박스 좌표로 옮긴다.
			x := (float64(px)+0.5)/scale - pad
			y := (float64(py)+0.5)/scale - pad
			a := alphaAt(icon, x, y, scale)
			if a <= 0 {
				continue
			}
			c := ink
			c.A = uint8(math.Round(a * 255))
			img.SetNRGBA(px, py, c)
		}
	}
	return img
}

// alphaAt 은 한 점의 불투명도다. 모든 층이 한 색이라 알파만 합성하면 된다.
func alphaAt(icon tray.Icon, x, y, scale float64) float64 {
	cover := func(sd float64) float64 { return clamp01(0.5 - sd*scale) }

	switch icon.Kind {
	case tray.IconOffline:
		// 점선 링과 사선을 합친 뒤 그룹 불투명도를 한 번만 곱한다 (SVG 의 <g opacity>).
		sd := math.Min(dashedRingSD(x, y), segmentSD(x, y, 3.6, 12.4, 12.4, 3.6)-offlineHalf)
		return cover(sd) * offlineOp

	case tray.IconAlert:
		a := over(trackAlpha*cover(annulusSD(x, y)), cover(arcSD(x, y, alertSweep)))
		mark := math.Min(
			segmentSD(x, y, 8, 5.7, 8, 8.0)-0.8, // 느낌표 막대 (rect 7.2,4.9 1.6×3.9 rx .8)
			math.Hypot(x-8, y-10.7)-0.95,        // 느낌표 점
		)
		return over(a, cover(mark))

	default:
		a := trackAlpha * cover(annulusSD(x, y))
		if icon.Percent > 0 {
			a = over(a, cover(arcSD(x, y, float64(min(icon.Percent, 100))/100)))
		}
		return over(a, cover(math.Hypot(x-center, y-center)-dotRadius))
	}
}

// annulusSD 는 링 트랙까지의 부호 거리다 (안쪽이 음수).
func annulusSD(x, y float64) float64 {
	return math.Abs(math.Hypot(x-center, y-center)-ringRadius) - ringHalf
}

// arcSD 는 12시에서 시계 방향으로 sweep(0~1) 만큼 그린 호까지의 부호 거리다.
// 호 밖의 각도에서는 양 끝점까지의 거리를 쓰므로 round 캡이 저절로 생긴다.
func arcSD(x, y, sweep float64) float64 {
	if sweep >= 1 {
		return annulusSD(x, y)
	}
	span := sweep * 2 * math.Pi
	if fromTop(x, y) <= span {
		return annulusSD(x, y)
	}
	// 화면 좌표는 y 가 아래로 자라서 각이 커지면 시계 방향이다. 12시는 -π/2 다.
	sx, sy := center, center-ringRadius
	ex := center + ringRadius*math.Cos(span-math.Pi/2)
	ey := center + ringRadius*math.Sin(span-math.Pi/2)
	return math.Min(math.Hypot(x-sx, y-sy), math.Hypot(x-ex, y-ey)) - ringHalf
}

// dashedRingSD 는 tray-offline 의 점선 링이다. SVG 의 기본대로 3시에서 시계 방향으로
// 대시를 놓고 끝은 butt 캡이다.
func dashedRingSD(x, y float64) float64 {
	radial := math.Abs(math.Hypot(x-center, y-center)-ringRadius) - offlineHalf
	s := mod(math.Atan2(y-center, x-center), 2*math.Pi) * ringRadius // 원주 위 위치
	t := mod(s, offlineDash+offlineGap)
	// 대시 구간 [0, dash] 까지 원주 방향 부호 거리. 경계가 가까운 쪽으로 잰다.
	along := math.Max(-t, t-offlineDash)
	if t > offlineDash {
		along = math.Min(t-offlineDash, offlineDash+offlineGap-t)
	}
	return math.Max(radial, along)
}

func segmentSD(x, y, ax, ay, bx, by float64) float64 {
	dx, dy := bx-ax, by-ay
	h := clamp01(((x-ax)*dx + (y-ay)*dy) / (dx*dx + dy*dy))
	return math.Hypot(x-(ax+h*dx), y-(ay+h*dy))
}

// fromTop 은 12시에서 시계 방향으로 잰 각(0~2π)이다.
func fromTop(x, y float64) float64 {
	return mod(math.Atan2(y-center, x-center)+math.Pi/2, 2*math.Pi)
}

// over 는 같은 색 두 층의 알파 합성이다 (top 이 위).
func over(bottom, top float64) float64 { return top + bottom*(1-top) }

func mod(a, m float64) float64 { return a - m*math.Floor(a/m) }

func clamp01(v float64) float64 { return math.Max(0, math.Min(1, v)) }
