package event

import (
	"crypto/sha256"
	"encoding/hex"
	"path"
	"strings"
)

// Path 는 경로를 저장 가능한 형태로 줄인 결과다. 전체 경로는 어느 필드에도 남지 않는다 —
// 이 타입이 project_hash+project_name, file_path_hash+file_name, target_hash+target_name 쌍의
// 유일한 생산자다 (ADR 0003).
type Path struct {
	Hash string // sha256(정규화 경로) 의 소문자 hex. 경로가 없으면 빈 문자열
	Name string // basename. 구분자를 포함하지 않는다
	Ext  string // 소문자 확장자, 앞의 점 없음. 없으면 빈 문자열
}

// NormalizePath 는 벤더가 준 경로를 해시·basename·확장자로 정규화한다.
//
// 해시는 표기 차이를 흡수해야 한다 — 같은 파일이 C:\x\y.go 와 C:/x/y.go 로, 또는 ./a 와 a 로
// 들어오면 session_files 가 두 행으로 갈라진다. 그래서 구분자 통일 → path.Clean → 드라이브
// 문자 대문자화까지만 하고, 홈 확장이나 심볼릭 링크 해석처럼 파일시스템·환경을 읽어야 하는
// 정규화는 하지 않는다. 이 패키지는 순수 함수만 둔다.
//
// 알려진 한계: ~/a.go 와 /Users/jy/a.go 는 서로 다른 해시가 된다(홈을 모르므로).
// POSIX 파일명에 들어간 리터럴 백슬래시는 구분자로 취급된다.
func NormalizePath(raw string) Path {
	cleaned := cleanPath(raw)
	if cleaned == "" {
		return Path{}
	}
	sum := sha256.Sum256([]byte(cleaned))
	p := Path{Hash: hex.EncodeToString(sum[:])}

	base := cleaned[strings.LastIndexByte(cleaned, '/')+1:]
	if base == "" || base == ".." {
		// "/" 나 "../.." 처럼 이름이라 부를 것이 없는 경우다. 해시만 남긴다.
		return p
	}
	p.Name = base
	p.Ext = extOf(base)
	return p
}

func cleanPath(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	s = strings.ReplaceAll(s, `\`, "/")
	s = path.Clean(s) // 중복 슬래시·후행 슬래시·"."·".." 정리
	if s == "." {
		return ""
	}
	return upperDrive(s)
}

// upperDrive 는 c:/x 와 C:/x 가 다른 해시가 되지 않게 드라이브 문자만 대문자로 맞춘다.
// 나머지 구간은 건드리지 않는다 — POSIX 는 대소문자를 구분하고 우리는 어느 쪽인지 모른다.
func upperDrive(s string) string {
	if len(s) < 2 || s[1] != ':' {
		return s
	}
	if c := s[0]; c >= 'a' && c <= 'z' {
		return string(c-'a'+'A') + s[1:]
	}
	return s
}

// extOf 는 basename 의 확장자를 소문자로 돌려준다. .gitignore 처럼 점으로 시작하는 이름과
// "a." 처럼 점으로 끝나는 이름은 확장자가 없는 것으로 본다.
func extOf(base string) string {
	i := strings.LastIndexByte(base, '.')
	if i <= 0 || i == len(base)-1 {
		return ""
	}
	return strings.ToLower(base[i+1:])
}
