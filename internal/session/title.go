package session

import (
	"fmt"
	"strings"
	"unicode"
)

// 캡은 룬 기준이다. 바이트로 자르면 한글 3바이트 중간에서 끊겨 화면에 U+FFFD 가 뜬다.
const (
	TitleRuneCap   = 60
	SummaryRuneCap = 120
)

// summarySentences 는 summary 에 쓸 문장 범위다 — 프롬프트의 2~3번째 문장.
const (
	summaryFirst = 1
	summaryLast  = 3
)

// deriveFromPrompt 는 첫 사용자 프롬프트에서 제목과 요약을 뽑는다 (title_source=prompt_head).
//
// 제목은 첫 문장 60자, 요약은 2~3번째 문장 120자다. 문장이 하나뿐이면 요약은 비운다 —
// 제목과 같은 문장을 요약에 한 번 더 쓰면 화면에 같은 줄이 두 번 보인다.
func deriveFromPrompt(body string) (title, summary string) {
	s := splitSentences(body)
	if len(s) == 0 {
		return "", ""
	}
	title = capRunes(s[0], TitleRuneCap)
	if len(s) > summaryFirst {
		end := min(len(s), summaryLast)
		summary = capRunes(strings.Join(s[summaryFirst:end], " "), SummaryRuneCap)
	}
	return title, summary
}

// filesTitle 은 변경량 내림차순으로 정렬된 파일 목록에서 제목을 만든다 (title_source=files).
// files 가 비었으면 빈 문자열을 돌려주고 호출자가 fallback 으로 내려간다.
func filesTitle(files []File) string {
	if len(files) == 0 || files[0].Name == "" {
		return ""
	}
	// 계획서 형식은 "{basename} 외 N개 수정" 이지만 파일이 하나면 "외 0개" 가 되어 어색하다.
	if len(files) == 1 {
		return capRunes(files[0].Name+" 수정", TitleRuneCap)
	}
	return capRunes(fmt.Sprintf("%s 외 %d개 수정", files[0].Name, len(files)-1), TitleRuneCap)
}

// fallbackTitle 은 원문도 파일도 없을 때의 마지막 수단이다 (title_source=fallback).
// 게이트가 꺼진 설치에서는 세션 대부분이 여기로 떨어진다 (ADR 0003).
func fallbackTitle(vendor string) string {
	if vendor == "" {
		return "세션"
	}
	return vendor + " 세션"
}

// capRunes 는 룬 기준으로 자르고 잘렸으면 말줄임표를 붙인다.
//
// 말줄임표를 붙이는 이유: 표식이 없으면 잘린 제목이 "완결됐지만 뜻이 다른 문장"으로 읽힌다.
// "인증 토큰 검증 및 Collector 전달 프록시 구" 는 오타나 데이터 손상처럼 보이고, 더 나쁜
// 경우는 잘린 위치가 우연히 말이 되어 사용자가 목록에서 다른 세션을 고른다.
//
// 표식도 캡 안에 넣는다(cap-1 룬 + '…'). 밖에 두면 결과가 cap+1 룬이 되어 화면의 열 너비
// 계약을 넘긴다. "..." 대신 '…'(U+2026) 한 룬을 쓰는 것도 본문 두 룬을 더 살리기 위해서다.
func capRunes(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	rs := []rune(s)
	if len(rs) <= limit {
		return s
	}
	if limit == 1 {
		return "…"
	}
	return strings.TrimRight(string(rs[:limit-1]), " ") + "…"
}

// splitSentences 는 프롬프트를 문장으로 나눈다. 결과에는 개행이 없고 연속 공백은 하나로 접힌다.
//
// 한국어를 고려한 두 가지:
//
//   - 개행을 문장 경계로 본다. "개행 제거"를 공백 치환으로 구현하면 목록형 프롬프트
//     ("다음을 구현해줘:\n- A\n- B")가 종결 부호 없는 한 덩어리가 되어 제목이 항목 나열로
//     잘린다. 경계로 보면 "다음을 구현해줘:" 가 제목이 된다. 어느 쪽이든 개행은 남지 않는다.
//   - 종결 부호는 **뒤가 공백이거나 문자열 끝일 때만** 문장을 끊는다. 한국어 프롬프트에는
//     "internal/session/event.go 를 고쳐줘" 처럼 파일명이 그대로 들어오는 일이 잦아서,
//     '.' 만 보고 끊으면 제목이 "internal/session/event." 가 된다. "3.5초" 도 같은 이유다.
//     전각 부호(。！？)는 뒤에 공백을 두지 않는 표기가 흔해 그 자리에서 끊는다.
func splitSentences(body string) []string {
	var (
		out   []string
		cur   strings.Builder
		space bool // 직전에 공백을 이미 넣었는지 — 연속 공백을 하나로 접는다
	)
	flush := func() {
		t := strings.TrimSpace(cur.String())
		cur.Reset()
		space = false
		if t != "" {
			out = append(out, t)
		}
	}

	rs := []rune(body)
	for i, r := range rs {
		switch {
		case r == '\n' || r == '\r':
			flush()
		case unicode.IsSpace(r):
			if cur.Len() > 0 && !space {
				cur.WriteRune(' ')
				space = true
			}
		default:
			cur.WriteRune(r)
			space = false
			if isTerminator(r) && endsSentence(rs, i) {
				flush()
			}
		}
	}
	flush()
	return out
}

func isTerminator(r rune) bool {
	switch r {
	case '.', '!', '?', '。', '！', '？':
		return true
	}
	return false
}

// endsSentence 는 종결 부호 뒤가 문장 끝인지 본다. "..." 이나 "?!" 처럼 부호가 이어지면
// 마지막 부호에서만 끊겨 부호 전체가 문장에 남는다.
func endsSentence(rs []rune, i int) bool {
	switch rs[i] {
	case '。', '！', '？':
		return true
	}
	if i == len(rs)-1 {
		return true
	}
	return unicode.IsSpace(rs[i+1])
}
