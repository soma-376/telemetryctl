package httpapi

import (
	"fmt"
	"net/http"
)

const psScript = `$server = "%[1]s"
$code = '%[2]s'

# /windows 요청이 이 스크립트를 반환했다면 enrollment code 검증은 완료된 상태다.
$tmp = Join-Path $env:TEMP ("pulsemetry_" + [guid]::NewGuid().ToString("N") + ".exe")
$prevProgress = $ProgressPreference
$ProgressPreference = "SilentlyContinue"

try {
  Write-Host "[1/3] 실행 파일 다운로드 중..."
  $params = @{
    Uri             = "$server/bin/pulsemetry_windows_amd64.exe"
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

Write-Host "[2/3] 서버에 enrollment 요청 보내는 중..."
& $tmp enroll --invite $code --server $server --quiet
if ($LASTEXITCODE -ne 0) {
  Remove-Item $tmp -Force -ErrorAction SilentlyContinue
  throw "enrollment 요청 실패"
}
Write-Host "enrollment 요청 성공"

try {
	Write-Host "[3/3] 설치 중..."
  $dest = Join-Path $env:LOCALAPPDATA "pulsemetry"
  New-Item -ItemType Directory -Path $dest -Force | Out-Null
  $exe = Join-Path $dest "pulsemetry.exe"
  Move-Item -Path $tmp -Destination $exe -Force
  Write-Host "설치 완료: $exe"
} catch {
  if (Test-Path $tmp) { Remove-Item $tmp -Force -ErrorAction SilentlyContinue }
  throw "설치 실패: $_"
}
`

const shScript = `#!/bin/sh
set -eu
SERVER="%[1]s"
CODE='%[2]s'

# /unix 요청이 이 스크립트를 반환했다면 enrollment code 검증은 완료된 상태다.
TMP="$(mktemp)"
trap 'rm -f "$TMP"' EXIT

echo "[1/3] 실행 파일 다운로드 중..."
curl -fsSL "$SERVER/bin/pulsemetry_linux_amd64" -o "$TMP"
chmod +x "$TMP"

echo "[2/3] 서버에 enrollment 요청 보내는 중..."
if ! "$TMP" enroll --invite "$CODE" --server "$SERVER" --quiet; then
  echo "enrollment 요청 실패" >&2
  exit 1
fi
echo "enrollment 요청 성공"

echo "[3/3] 설치 중..."
DEST="$HOME/.local/bin"
mkdir -p "$DEST"
mv -f "$TMP" "$DEST/pulsemetry"
trap - EXIT
echo "설치 완료: $DEST/pulsemetry"
`

func NewWindowsHandler() http.HandlerFunc {
	return script(psScript)
}

func NewUnixHandler() http.HandlerFunc {
	return script(shScript)
}

func script(tmpl string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = fmt.Fprintf(w, tmpl, baseURL(r), r.URL.Query().Get("code"))
	}
}

func baseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
