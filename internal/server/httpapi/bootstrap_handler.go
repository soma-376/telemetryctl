// Package bootstrap 은 한 줄 설치용 스크립트를 내려주는 핸들러다.
//
//	PowerShell: irm <server>/windows | iex
//	sh:         curl -fsSL <server>/unix | sh
//
// 스크립트 흐름:
//  1. 임시 폴더에 pulsemetry 바이너리 다운로드
//  2. 초대 코드를 (env 폴백 후) 프롬프트로 받아 enroll 실행
//  3. enroll 이 초대 검증 + 설정 적용
//  4. 성공하면 임시 바이너리를 정식 설치 경로로 이동
//  5. 실패하면 임시 바이너리를 삭제
//
// 보안: irm|iex / curl|sh 는 원격 코드 즉시 실행이라 PoC 편의 기능이다 (설계 §5.9).
// 제품화 시 서명된 설치 패키지로 대체한다.
package httpapi

import (
	"fmt"
	"net/http"
)

// %[1]s = 서버 base URL. 여러 번 재사용하므로 인덱스 지정 verb 를 쓴다.
const psScript = `$server = "%[1]s"
if ($env:PULSEMETRY_INVITE) { $code = $env:PULSEMETRY_INVITE } else { $code = Read-Host "pulsemetry 초대 코드" }

# 1) 임시 폴더에 다운로드
$tmp = Join-Path $env:TEMP ("pulsemetry_" + [guid]::NewGuid().ToString("N") + ".exe")
$prevProgress = $ProgressPreference
$ProgressPreference = "SilentlyContinue"   # PS 5.1: 진행률 바가 다운로드를 크게 느리게 함
try {
  Write-Host "[1/3] 실행 파일 다운로드 중..."
  # 백틱 줄바꿈은 Go raw string 을 끊으므로 splatting 으로 인자를 전달한다.
  $params = @{
    Uri             = "%[1]s/bin/pulsemetry_windows_amd64.exe"
    OutFile         = $tmp
    UseBasicParsing = $true
  }
  Invoke-WebRequest @params
} catch {
  if (Test-Path $tmp) { Remove-Item $tmp -Force -ErrorAction SilentlyContinue }
  throw "다운로드 실패: $_"
} finally {
  $ProgressPreference = $prevProgress
}

# 2) enroll: 초대 코드 확인 + 설정 적용
Write-Host "[2/3] 초대 코드 확인 및 설정 적용 중..."
& $tmp enroll --invite $code --server "%[1]s" --quiet

if ($LASTEXITCODE -eq 0) {
  # 성공 → 정식 설치 경로로 이동
  $dest = Join-Path $env:LOCALAPPDATA "pulsemetry"
  New-Item -ItemType Directory -Path $dest -Force | Out-Null
  $exe = Join-Path $dest "pulsemetry.exe"
  Move-Item -Path $tmp -Destination $exe -Force
  Write-Host "[3/3] 설치 완료: $exe"
} else {
  # 실패 → 임시 exe 삭제
  Remove-Item $tmp -Force -ErrorAction SilentlyContinue
  throw "enroll 실패 — 임시 파일을 삭제했습니다."
}
`

const shScript = `#!/bin/sh
set -e
SERVER="%[1]s"
if [ -n "$PULSEMETRY_INVITE" ]; then CODE="$PULSEMETRY_INVITE"; else printf "pulsemetry 초대 코드: "; read CODE </dev/tty; fi

# 1) 임시 폴더에 다운로드
TMP="$(mktemp)"
echo "[1/3] 실행 파일 다운로드 중..."
if ! curl -fsSL "%[1]s/bin/pulsemetry_linux_amd64" -o "$TMP"; then
  rm -f "$TMP"; echo "다운로드 실패" >&2; exit 1
fi
chmod +x "$TMP"

# 2) enroll: 초대 코드 확인 + 설정 적용
echo "[2/3] 초대 코드 확인 및 설정 적용 중..."
if "$TMP" enroll --invite "$CODE" --server "%[1]s" --quiet; then
  # 성공 → 정식 설치 경로로 이동
  DEST="$HOME/.local/bin"
  mkdir -p "$DEST"
  mv -f "$TMP" "$DEST/pulsemetry"
  echo "[3/3] 설치 완료: $DEST/pulsemetry"
else
  # 실패 → 임시 파일 삭제
  rm -f "$TMP"
  echo "enroll 실패 — 임시 파일을 삭제했습니다." >&2
  exit 1
fi
`

// WindowsHandler 는 irm <server>/windows | iex 용 PowerShell 스크립트를 내려준다.
func WindowsHandler() http.HandlerFunc {
	return script(psScript)
}

// UnixHandler 는 curl -fsSL <server>/unix | sh 용 sh 스크립트를 내려준다.
func UnixHandler() http.HandlerFunc {
	return script(shScript)
}

func script(tmpl string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintf(w, tmpl, baseURL(r))
	}
}

// baseURL 은 요청으로부터 서버의 외부 base URL 을 추정한다(프록시 뒤면 X-Forwarded-Proto 존중).
func baseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
