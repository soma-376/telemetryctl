# pulsemetry

Codex와 Claude Code의 OpenTelemetry 설정을 조직 단위로 안전하게 적용하는 도구입니다.
직원은 **한 줄 설치**로 초대 코드를 입력하면, 서버가 회사에 맞는 설정을 내려주고 클라이언트가
Claude Code·Codex 설정에 필요한 OTel 키만 병합합니다.

하나의 Go 모듈에 **클라이언트 CLI**와 **enrollment 서버**가 함께 있습니다(모노레포).

## 구조

```text
.
├── cmd/
│   ├── client/                # 클라이언트 CLI (enroll·status·version)
│   └── server/                # enrollment 서버
├── contracts/                 # enrollment manifest JSON Schema
├── docs/                      # 설치 아키텍처·서버 스펙·개발 워크플로
└── internal/
    ├── contract/              # 서버·클라이언트 공유 계약 (enroll 요청/응답, manifest)
    ├── client/                # 클라이언트 전용
    │   ├── enrollment/        #   서버 /v1/enroll 호출
    │   ├── installer/         #   설정 적용 오케스트레이션 + 로컬 상태
    │   ├── config/            #   Claude·Codex 설정 병합·백업
    │   └── hostenv/           #   OS·WSL 감지, 설정 파일 경로
    └── server/                # 서버 전용
        ├── httpapi/           #   HTTP 핸들러 (enroll, 부트스트랩 스크립트, 바이너리 서빙)
        ├── enrollment/        #   등록 도메인 로직
        └── store/             #   저장소 (MVP: 인메모리)
```

**경계 규칙**: `client`와 `server`는 서로를 import 하지 않습니다. 둘이 공유하는 것은 `contract` 뿐입니다.

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
pulsemetry enroll --invite <code> [--server <url>]   # 등록 후 설정 적용
pulsemetry status                                    # 현재 설치 상태 표시
```

`enroll` 은 서버에서 받은 설정 봉투(`{installation_id, installation_token, manifest}`)를 적용해
Claude Code(`~/.claude/settings.json`)·Codex(`~/.codex/config.toml`)에 OTel 키만 병합하고,
적용 내역을 `~/.pulsemetry/state.json` 에 기록합니다. 서버 URL 은 `--server` > `PULSEMETRY_SERVER` >
빌드 기본값(릴리스 시 `-ldflags` 주입) 순으로 결정합니다.

## 로컬 개발

```sh
go run ./cmd/server                                                       # http://localhost:8080 (시드 초대: TEST-1234)
go run ./cmd/client enroll --invite TEST-1234 --server http://localhost:8080
```

서버는 `/bin/` 에서 클라이언트 바이너리를 서빙하므로, 부트스트랩을 로컬에서 시험하려면
`dist/pulsemetry_windows_amd64.exe`(`go build -o … ./cmd/client`)를 미리 빌드해 둡니다
(또는 `PULSEMETRY_DIST_DIR` 로 경로 지정).

## 핵심 원칙

- 기존 설정 파일 전체를 덮어쓰지 않고 필요한 OTel 키만 병합합니다.
- 기존 파일은 최초 변경 전에 `<이름>.pulsemetry-backup.<확장자>`(예: `settings.pulsemetry-backup.json`)로 백업합니다.
- 다른 OTel endpoint 가 있으면 명시적 `--force` 없이는 교체하지 않습니다.
- manifest 의 알 수 없는 필드와 지원하지 않는 버전을 거부합니다.
- 토큰을 로그·로컬 상태 파일에 기록하지 않습니다.

## 개발

요구 사항은 Go 1.23 이상입니다.

```sh
go test ./...
```

자세한 설계는 [설치 아키텍처](docs/installation-architecture.md)와 [Enrollment 서버 스펙](docs/enrollment-server-spec.md),
협업 규칙은 [개발 워크플로](docs/development-workflow.md)를 참고하세요.

## 다음 구현 대상

1. 서버 저장소 Postgres 교체 (현재 인메모리)
2. 관리자 초대 발급 API(`POST /v1/admin/invites`) + 토큰 폐기
3. 토큰 rotation · heartbeat · 설정 재조회(`GET /v1/manifest`)
4. `resource_attributes` → `OTEL_RESOURCE_ATTRIBUTES` 배선 (회사 단위 태깅)
5. 설치 바이너리 PATH 등록, `uninstall`·`repair`
6. Manifest JSON Schema ↔ Go 계약 테스트 (드리프트 방지)
