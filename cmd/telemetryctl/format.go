package main

// 사람용 출력의 정렬과 숫자·시각 표기를 담당한다.
//
// # text/tabwriter 를 쓰지 않는 이유
//
// tabwriter 는 셀 폭을 **룬 개수** 로 잰다. ASCII 표에서는 그것이 곧 터미널 칸 수라
// 문제가 없지만, 한글은 룬 하나가 터미널에서 두 칸을 먹는다. "프로젝트" 는 4룬·8칸이고
// "sessions" 는 8룬·8칸이라, tabwriter 에게는 sessions 가 두 배 넓은 셀이다. 그 결과
// 한글이 섞인 열은 오른쪽 경계가 행마다 들쭉날쭉해진다.
//
// 그래서 폭 계산을 displayWidth 로 직접 하고 표도 직접 그린다. golang.org/x/text/width 를
// 쓰면 정확한 East Asian Width 표를 얻지만 그것은 지금 indirect 의존성이라 직접 의존으로
// 승격시켜야 하고, 이 기능 하나로 go.mod 를 건드릴 이유가 없다. 아래 표는 실제로 쓰이는
// 범위(한글·한자·가나·전각기호·이모지)를 담은 근사이며, 그 밖의 넓은 문자는 한 칸으로
// 세어 열이 좁게 잡힐 수 있다 — 알려진 한계다.

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// wideRanges 는 터미널에서 두 칸을 차지하는 룬 범위다 (East Asian Wide·Fullwidth).
var wideRanges = [][2]rune{
	{0x1100, 0x115F},   // 한글 자모
	{0x2E80, 0x303E},   // CJK 부수 보충 ~ CJK 기호 (0x303F 는 반칸이라 제외)
	{0x3041, 0x33FF},   // 히라가나·가타카나·한글 호환 자모·CJK 호환
	{0x3400, 0x4DBF},   // CJK 확장 A
	{0x4E00, 0x9FFF},   // CJK 통합 한자
	{0xA000, 0xA4CF},   // 이족 음절
	{0xA960, 0xA97F},   // 한글 자모 확장 A
	{0xAC00, 0xD7A3},   // 한글 음절
	{0xF900, 0xFAFF},   // CJK 호환 한자
	{0xFE10, 0xFE19},   // 세로쓰기 형식
	{0xFE30, 0xFE6F},   // CJK 호환 형식
	{0xFF00, 0xFF60},   // 전각 영숫자·기호
	{0xFFE0, 0xFFE6},   // 전각 통화 기호
	{0x1F300, 0x1F64F}, // 이모지
	{0x1F900, 0x1F9FF}, // 보충 이모지
	{0x20000, 0x3FFFD}, // CJK 확장 B 이상
}

// runeWidth 는 룬 하나가 차지하는 칸 수다.
func runeWidth(r rune) int {
	for _, rg := range wideRanges {
		if r >= rg[0] && r <= rg[1] {
			return 2
		}
	}
	return 1
}

// displayWidth 는 문자열이 터미널에서 차지하는 칸 수다.
func displayWidth(s string) int {
	w := 0
	for _, r := range s {
		w += runeWidth(r)
	}
	return w
}

// pad 는 문자열을 표시 폭 기준으로 width 칸에 맞춘다.
func pad(s string, width int, right bool) string {
	gap := width - displayWidth(s)
	if gap <= 0 {
		return s
	}
	fill := strings.Repeat(" ", gap)
	if right {
		return fill + s
	}
	return s + fill
}

// column 은 표의 열 하나다. 수치 열은 오른쪽 정렬해야 자릿수를 눈으로 비교할 수 있다.
type column struct {
	Header string
	Right  bool
}

// columnGap 은 열 사이 간격이다. 한 칸이면 오른쪽 정렬한 숫자와 왼쪽 정렬한 이름이
// 붙어 보인다.
const columnGap = "  "

// writeTable 은 표를 그린다. 구분선은 ASCII '-' 다 — 罫線 문자(─)는 East Asian Ambiguous
// 라 터미널마다 한 칸이거나 두 칸이어서, 그것을 쓰는 순간 정렬이 환경에 좌우된다.
func writeTable(w io.Writer, cols []column, rows [][]string) {
	if len(cols) == 0 {
		return
	}
	widths := make([]int, len(cols))
	for i, c := range cols {
		widths[i] = displayWidth(c.Header)
	}
	for _, row := range rows {
		for i := range cols {
			if i < len(row) && displayWidth(row[i]) > widths[i] {
				widths[i] = displayWidth(row[i])
			}
		}
	}

	header := make([]string, len(cols))
	rule := make([]string, len(cols))
	for i, c := range cols {
		header[i] = pad(c.Header, widths[i], c.Right)
		rule[i] = strings.Repeat("-", widths[i])
	}
	fmt.Fprintln(w, strings.TrimRight(strings.Join(header, columnGap), " "))
	fmt.Fprintln(w, strings.Join(rule, columnGap))

	for _, row := range rows {
		cells := make([]string, len(cols))
		for i, c := range cols {
			v := ""
			if i < len(row) {
				v = row[i]
			}
			cells[i] = pad(v, widths[i], c.Right)
		}
		fmt.Fprintln(w, strings.TrimRight(strings.Join(cells, columnGap), " "))
	}
}

// ── 숫자·시각 표기 ───────────────────────────────────────────────────────────

// formatInt 는 세 자리마다 쉼표를 넣는다. 토큰 수는 여덟 자리를 넘기기 때문에
// 구분자가 없으면 자릿수를 눈으로 셀 수 없다.
func formatInt(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// formatCost 는 비용을 USD 로 쓴다. 소수 넷째 자리까지 두는 이유는 한 시간짜리 세션의
// 비용이 0.0xxx 달러 수준이라 두 자리로는 전부 0.00 이 되기 때문이다.
func formatCost(v float64) string { return strconv.FormatFloat(v, 'f', 4, 64) }

// formatDurationMS 는 소요 시간을 사람이 읽는 형태로 줄인다.
func formatDurationMS(ms int64) string {
	if ms <= 0 {
		return "-"
	}
	d := time.Duration(ms) * time.Millisecond
	switch {
	case d < time.Second:
		return fmt.Sprintf("%dms", ms)
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

// timeLayout 은 사람용 시각 표기다. --since 7d 면 날짜가 넘어가므로 날짜를 함께 쓴다.
const timeLayout = "2006-01-02 15:04"

// formatUnixLocal 은 UTC unix 초를 **로컬 시간대** 로 보여 준다. 화면에 보이는 시각은
// 사용자의 시계와 같아야 한다. 0 은 "없음" 이다.
func formatUnixLocal(sec int64) string {
	if sec <= 0 {
		return "-"
	}
	return time.Unix(sec, 0).In(time.Local).Format(timeLayout)
}

// zoneLabel 은 로컬 시간대 표시다 (예: KST+09:00). time.Local.String() 은 "Local" 이라
// 사람에게 아무것도 알려 주지 않으므로 약어와 오프셋을 직접 만든다.
func zoneLabel(t time.Time) string {
	name, offset := t.In(time.Local).Zone()
	sign := "+"
	if offset < 0 {
		sign = "-"
		offset = -offset
	}
	return fmt.Sprintf("%s%s%02d:%02d", name, sign, offset/3600, (offset%3600)/60)
}

// zoneOffsetSeconds 는 JSON 에 싣는 로컬 시간대 오프셋이다. 기계는 약어("KST")로
// 시각을 복원할 수 없다 — 약어는 중복되는 것이 있다.
func zoneOffsetSeconds(t time.Time) int {
	_, offset := t.In(time.Local).Zone()
	return offset
}

// formatBytes 는 파일 크기를 IEC 단위로 쓴다.
func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit && exp < 3; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGT"[exp])
}
