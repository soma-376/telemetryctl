# telemetryctl

Codex와 Claude Code의 OpenTelemetry 설정을 조직 단위로 안전하게 적용하는 **클라이언트 데몬**입니다.
직원은 **한 줄 설치**로 초대 코드를 입력하면, enrollment 서버가 회사에 맞는 설정을 내려주고 이
클라이언트가 Claude Code·Codex 설정에 필요한 OTel 키만 병합합니다.

이 저장소는 **클라이언트 CLI/데몬 전용**입니다(Go, 단일 정적 바이너리). enrollment 서버는 **별도
저장소**로 분리돼 있으며, 둘을 잇는 것은 `contracts/` 의 JSON Schema 계약뿐입니다.

## 구조

```text
.
├── cmd/telemetryctl/          # CLI 진입점 (enroll·status·daemon·version)
├── contracts/                 # 서버와의 JSON Schema 계약 사본 (계약 테스트 기준)
│   ├── enrollment-manifest.schema.json
│   └── enrollment-envelope.schema.json
├── docs/                      # 설치 아키텍처·개발 워크플로
└── internal/
    ├── contract/              #   enroll 요청/응답·manifest Go 타입 (스키마와 1:1)
    ├── enrollment/            #   서버 /v1/enroll 호출
    ├── installer/             #   설정 적용 오케스트레이션 + 로컬 상태
    ├── credential/            #   설치 토큰 원본 보관 (OS 키링)
    ├── config/                #   Claude·Codex 설정 병합·백업
    ├── hostenv/               #   OS·WSL 감지, 설정 파일 경로
    └── daemon/                #   토큰 rotation·heartbeat·설정 재조회 (예정)
```

**경계 규칙**: 클라이언트(Go)와 서버는 코드를 공유하지 않습니다. 유일한 계약은 `contracts/` 의
JSON Schema 이며, `internal/contract` 의 Go 타입이 이 스키마와 1:1 로 대응하는지 계약 테스트로
검증합니다. 서버가 스키마를 갱신하면 `contracts/` 사본을 수동으로 동기화합니다.

## 사용법

한 줄 설치 (서버가 부트스트랩 스크립트를 내려줌 → 초대 코드 입력):

```powershell
irm <server>/windows | iex          # PowerShell
```
```sh
curl -fsSL <server>/unix | sh        # bash
```

또는 바이너리를 직접 실행:

```sh
telemetryctl enroll --invite <code> [--server <url>]   # 등록 후 설정 적용
telemetryctl reconnect [--server <url>]                # 텔레메트리 토큰 재발급 및 설정 갱신
telemetryctl status                                    # 현재 설치 상태 표시
```

`enroll` 은 서버에서 받은 설정 봉투(`{installation_id, installation_token, telemetry_token, manifest}`)를 적용해
Claude Code(`~/.claude/settings.json`)·Codex(`~/.codex/config.toml`)에 OTel 키만 병합하고,
적용 내역을 `~/.pulsemetry/state.json` 에 기록합니다. 서버 URL 은 `--server` > `PULSEMETRY_SERVER` >
빌드 기본값(릴리스 시 `-ldflags` 주입) 순으로 결정합니다.

`installation_token`은 OS 키링에만 저장하고 Claude·Codex 설정에는 교체 가능한
`telemetry_token`만 기록합니다. `reconnect`는 설치 토큰으로
`POST /v1/installations/telemetry-token`을 호출해 새 telemetry token을 발급받습니다.

## 로컬 개발

enrollment 서버는 별도 저장소에서 띄우고(로컬 URL 예: `http://localhost:8088`), 클라이언트는 그
URL 로 붙습니다.

```sh
go build -o dist/telemetryctl ./cmd/telemetryctl
go run ./cmd/telemetryctl enroll --invite TEST-1234 --server http://localhost:8088
```

릴리스 빌드는 서버 기본값을 주입합니다:

```sh
go build -ldflags "-X main.defaultServer=https://get.your-service.com" ./cmd/telemetryctl
```

## 핵심 원칙

- 기존 설정 파일 전체를 덮어쓰지 않고 필요한 OTel 키만 병합합니다.
- 기존 파일은 최초 변경 전에 `<이름>.pulsemetry-backup.<확장자>`(예: `settings.pulsemetry-backup.json`)로 백업합니다.
- 다른 OTel endpoint 가 있으면 명시적 `--force` 없이는 교체하지 않습니다.
- manifest 의 알 수 없는 필드와 지원하지 않는 버전을 거부합니다.
- 토큰을 로그·상태 파일(`state.json`)에 기록하지 않습니다. 토큰 원본은 **OS 키링**
  (Windows Credential Manager·macOS Keychain·Linux Secret Service)에만 두고,
  Claude·Codex 설정의 Authorization 헤더는 여기서 파생되는 사본으로 취급합니다.

## 개발

클라이언트는 Go 1.25 이상이 필요합니다.

```sh
go build ./...
go test ./...
```

자세한 설계는 [설치 아키텍처](docs/installation-architecture.md), 협업 규칙은
[개발 워크플로](docs/development-workflow.md)를 참고하세요. enrollment 서버 스펙은 서버 저장소를
참조하세요.

## 다음 구현 대상

1. 토큰 rotation · heartbeat · 설정 재조회(`GET /v1/manifest`) — daemon 의 첫 실제 임무
2. `resource_attributes` → `OTEL_RESOURCE_ATTRIBUTES` 배선 (회사 단위 태깅)
3. 설치 바이너리 PATH 등록, `uninstall`·`repair` (자격증명 파일에서 헤더 재주입)
4. JSON Schema ↔ Go `internal/contract` 계약 테스트
5. Codex 텔레메트리 인증 배선 (현재 Codex 설정에는 토큰이 들어가지 않음)
