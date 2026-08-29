# telemetryctl

Codex와 Claude Code의 OpenTelemetry 설정을 조직 단위로 안전하게 적용하는 **클라이언트 데몬**입니다.
직원은 **한 줄 설치**로 초대 코드를 입력하면, enrollment 서버가 회사에 맞는 설정을 내려주고 이
클라이언트가 Claude Code·Codex 설정에 필요한 OTel 키만 병합합니다.

이 저장소는 **클라이언트 CLI/데몬 전용**입니다(Go, 단일 정적 바이너리). enrollment 서버는 **별도
저장소**로 분리돼 있으며, 둘을 잇는 것은 `contracts/` 의 JSON Schema 계약뿐입니다.

## 구조

```text
.
├── cmd/telemetryctl/          # CLI 진입점 (enroll·status·reconnect·daemon·stats·sessions·purge·local·autostart·version)
├── contracts/                 # 서버와의 JSON Schema 계약 사본 (계약 테스트 기준)
│   ├── enrollment-manifest.schema.json
│   └── enrollment-envelope.schema.json
├── docs/                      # 설치 아키텍처·로컬 파이프라인·SQLite 스키마·ADR·개발 워크플로
└── internal/
    ├── contract/              #   enroll 요청/응답·manifest Go 타입 (스키마와 1:1)
    ├── enrollment/            #   서버 /v1/enroll 호출
    ├── installer/             #   설정 적용 오케스트레이션 + 로컬 상태 + local enable/disable
    ├── credential/            #   설치 토큰·ingest 토큰 원본 보관 (OS 키링)
    ├── config/                #   Claude·Codex 설정 병합·백업
    ├── hostenv/               #   OS·WSL 감지, 설정 파일 경로
    │
    │                          # ── 로컬 데이터 파이프라인 (PROJ-36) ──
    ├── event/                 #   정규화 이벤트 타입·DedupKey·경로 해시 (IO 없음)
    ├── otlpdecode/            #   OTLP 디코드 + content 제거 재인코딩 (proto 의존 격리)
    ├── receiver/              #   loopback OTLP/HTTP 수신기
    ├── forward/               #   회사 Collector 전달 (유계 큐·제한된 재시도)
    ├── session/               #   이벤트 → 세션 조립, 제목 휴리스틱 (순수 함수)
    ├── rollup/                #   시간 버킷 집계 (순수 함수)
    ├── store/                 #   SQLite 스키마·쓰기·보존 정책
    ├── dashboard/             #   화면별 조회 API (CLI·GUI 공용, Wails 의존 없음)
    ├── runtimeinfo/           #   runtime.json (비밀 없음: 주소·pid·데이터 경로)
    ├── autostart/             #   로그인 시 데몬 자동 실행 등록 (launchd·systemd user unit)
    └── daemon/                #   위 패키지 배선 + 틱 루프 + graceful shutdown
```

**경계 규칙**: 클라이언트(Go)와 서버는 코드를 공유하지 않습니다. 유일한 계약은 `contracts/` 의
JSON Schema 이며, `internal/contract` 의 Go 타입이 이 스키마와 1:1 로 대응하는지 계약 테스트로
검증합니다. 서버가 스키마를 갱신하면 `contracts/` 사본을 수동으로 동기화합니다.

## 사용법

한 줄 설치. **초대 코드를 URL 에 실어야 합니다** — 서버가 그 코드를 부트스트랩 스크립트에
박아서 내려주고, 스크립트가 바이너리를 받아 `enroll` 까지 실행합니다. 코드가 없으면 서버가
400 `invalid_request` 로 끊습니다.

```powershell
irm "<server>/windows?code=<초대코드>" | iex          # PowerShell
```
```sh
curl -fsSL "<server>/unix?code=<초대코드>" | sh        # bash
```

설치된 바이너리 이름은 **`pulsemetry`** 입니다(`~/.pulsemetry/bin/pulsemetry`). 아래 예시는
저장소 이름을 따라 `telemetryctl` 로 적지만, 한 줄 설치로 받은 환경에서는 `pulsemetry` 로 부릅니다.
`go build ./cmd/telemetryctl` 로 직접 빌드하면 `telemetryctl` 이라는 이름이 나옵니다.

또는 바이너리를 직접 실행:

```sh
telemetryctl enroll --invite <code> [--server <url>]   # 등록 후 설정 적용
telemetryctl reconnect [--server <url>]                # 텔레메트리 토큰 재발급 및 설정 갱신
telemetryctl status                                    # 설치·로컬 파이프라인 상태 표시
telemetryctl daemon [옵션]                             # foreground 데몬 (로컬 수신기 + 집계 + 상위 전달)
telemetryctl local enable|disable [--port 4318]        # 벤더 설정을 로컬 수신기로 재배선/해제
telemetryctl autostart enable|disable|status           # 로그인 시 데몬 자동 실행 등록/해제/조회
telemetryctl stats [--since 7d] [--group vendor]       # 로컬 집계 조회
telemetryctl sessions [--since 7d] [--status running]  # 로컬 세션 목록 조회
telemetryctl purge --content [--before 2026-07-01]     # 보관된 프롬프트·툴 원문 삭제
```

전체 플래그는 `telemetryctl help` 를 보세요.

`enroll` 은 서버에서 받은 설정 봉투(`{installation_id, installation_token, telemetry_token, manifest}`)를 적용해
Claude Code(`~/.claude/settings.json`)·Codex(`~/.codex/config.toml`)에 OTel 키만 병합하고,
적용 내역을 `~/.pulsemetry/state.json` 에 기록합니다. 서버 URL 은 `--server` > `PULSEMETRY_SERVER` >
빌드 기본값(릴리스 시 `-ldflags` 주입) 순으로 결정합니다.

`installation_token`과 회사 `telemetry_token`은 OS 키링에만 저장합니다. 로컬 파이프라인이 배선된
기본 상태에서 Claude·Codex 설정에 기록되는 것은 **로컬 ingest 토큰**뿐이고, 배선이 강등된
(회사 직결 — grpc manifest·키링 불가) 설치에서만 `telemetry_token`이 설정에 실립니다. `reconnect`는 설치 토큰으로
`POST /v1/installations/telemetry-token`을 호출해 새 telemetry token을 발급받습니다.

## 로컬 데이터 파이프라인 (opt-out, 기본 켜짐)

`enroll` 이 벤더 설정을 로컬 수신기로 자동 배선합니다. 데몬이 loopback OTLP 수신기를 띄워 시그널을
직접 받고, 세션 단위로 조립·집계해 로컬 SQLite(`~/.pulsemetry/pulsemetry.db`)에 저장한 뒤 회사
Collector 로도 전달합니다. **끄려면 `local disable` 입니다.**

> **개발 브랜치 주의:** SQLite 스키마 v3는 기존 로컬 도메인 데이터를 삭제하고 새 모델을 만들지만
> 데몬·CLI 런타임은 아직 v3로 전환되지 않았습니다. 후속 구현 전에는 로컬 수집·조회 명령이 실패하며,
> 현재 스키마 계약과 파괴적 전환 범위는 [SQLite 스키마 문서](docs/sqlite-schema/README.md)를 따릅니다.

```sh
telemetryctl enroll --invite <코드>   # 설치 + 로컬 배선 + 자동 실행 등록 (endpoint → http://localhost:4318)
telemetryctl sessions --since 1d
telemetryctl autostart status         # 자동 실행 등록 상태 확인
telemetryctl local disable            # 회사 Collector 직결로 복귀
```

> **데몬이 떠 있지 않으면 텔레메트리가 로컬에도 회사에도 남지 않습니다.** `enroll` 이 로그인 시
> 자동 실행을 등록해(macOS LaunchAgent · 리눅스 systemd user unit) 이 상태를 막습니다. 등록은
> best-effort 라 실패해도 `enroll` 은 성공하고 사실을 알립니다 — 그때는 `telemetryctl daemon` 을
> 직접 띄우면 됩니다. 원치 않으면 `telemetryctl autostart disable` 입니다.
>
> 자동 실행은 **사용자 수준**이라 로그인할 때 시작하고 로그아웃하면 함께 종료합니다.
> 비정상 종료일 때만 재시작하므로 직접 정지시키면 정지 상태를 유지합니다
> ([ADR 0007](docs/adr/0007-데몬은-비정상-종료일-때만-자동-재시작한다.md)).
> Windows(작업 스케줄러)는 후속 티켓이라 `telemetryctl daemon` 을 직접 띄워야 합니다.

로컬 OTel 설정은 회사 manifest 와 무관하게 **고정**입니다 — 시그널 셋을 전부 켜고 원문·tool details
수집도 켭니다(응답 원문 제외). 회사가 수집 범위를 좁혀도 로컬 화면이 비지 않게 하기 위해서입니다.
그래도 **로컬 파이프라인이 배선된 상태에서는 회사로 나가는 데이터가 배선 전후로 동일합니다** —
데몬이 회사 manifest 의 `signals` 로 전달 여부를, `privacy` 로 제거 대상을 판단합니다.
단, 회사 직결로 강등된 설치(grpc 테넌트·키링 실패)에는 데몬이 경로에 없어 벤더 설정 계층만 남습니다
(허브 `contracts/telemetry-ingest.md` §5 M13). 프롬프트 원문을 포함한 모든 로컬 데이터는
400일간 보관됩니다. 원문은 항목당 16KB로 제한되며 `--no-store-content`·`purge --content` 로
끄거나 지울 수 있습니다([ADR 0008](docs/adr/0008-로컬-데이터를-400일간-보존한다.md)).

기존 설치자(이미 `enroll` 을 마친 사용자)는 바이너리를 교체해도 자동 전환되지 않습니다.
`telemetryctl local enable` 로 명시적으로 켜세요.

토폴로지·프라이버시 불변식·GUI 조회 API 계약은 [로컬 파이프라인 문서](docs/local-pipeline.md)에,
DDL과 테이블별 계약은 [SQLite 스키마 문서](docs/sqlite-schema/README.md)에, 설계 결정 배경은
[ADR](docs/adr/)에 있습니다.

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
- 토큰을 로그·상태 파일(`state.json`·`runtime.json`)에 기록하지 않습니다. 토큰 원본은 **OS 키링**
  (Windows Credential Manager·macOS Keychain·Linux Secret Service)에만 두고,
  Claude·Codex 설정의 Authorization 헤더는 여기서 파생되는 사본으로 취급합니다.
- 로컬 저장소에도 전체 작업 경로·`user.*`·`organization.id` 를 남기지 않습니다. 속성은 allowlist
  컬럼으로만 받고 catch-all 컬럼을 두지 않습니다.
- 텔레메트리 손실은 허용하되 개발 도구 지연은 허용하지 않습니다. 수신기는 큐가 차면 `429` 가 아니라
  `200` + `PartialSuccess` 로 답하고, 상위 전달은 절대 수신을 막지 않습니다.

## 개발

클라이언트는 Go 1.25 이상이 필요합니다. SQLite 드라이버는 순수 Go 라 **CGO 가 필요 없습니다.**

```sh
go build ./...
go vet ./...
go test -race -cover ./...
CGO_ENABLED=0 go build ./...   # 배포 바이너리가 C 툴체인을 요구하지 않는지 (ADR 0002)
```

자세한 설계는 [설치 아키텍처](docs/installation-architecture.md),
[로컬 파이프라인](docs/local-pipeline.md), [SQLite 스키마](docs/sqlite-schema/README.md)를 참고하세요.
결정 배경은 [ADR](docs/adr/), 협업 규칙은 [개발 워크플로](docs/development-workflow.md)에 있습니다.
enrollment 서버 스펙은 서버 저장소를 참조하세요.

## 다음 구현 대상

1. **SQLite v3 런타임 전환** — 새 세션·턴·이벤트·LLM 호출·도구 호출 모델에 맞춰 쓰기, 조회,
   보존 로직을 교체하고 전체 테스트를 다시 통과시킵니다
2. **GUI 데스크탑 앱** (PROJ-35) — `gui/` 에 별도 `go.mod` 로 Wails v3 앱을 두고
   `internal/dashboard` 를 감쌉니다. 계약은 [로컬 파이프라인 문서](docs/local-pipeline.md) 6절
3. **데몬 자동 실행 등록 — Windows** (PROJ-56, 작업 스케줄러). macOS·리눅스는 PROJ-55 에서
   구현했습니다 (`internal/autostart`, [ADR 0007](docs/adr/0007-데몬은-비정상-종료일-때만-자동-재시작한다.md)).
   Windows 에서는 `autostart` 명령이 미지원임을 알리고 `telemetryctl daemon` 을 직접 띄워야 합니다
4. 토큰 rotation · heartbeat · 설정 재조회(`GET /v1/manifest`)
5. `resource_attributes` → `OTEL_RESOURCE_ATTRIBUTES` 배선 (회사 단위 태깅)
6. 설치 바이너리 PATH 등록, `uninstall`·`repair` (자격증명 파일에서 헤더 재주입)
7. Codex 텔레메트리 인증 배선 (현재 Codex 설정에는 토큰이 들어가지 않음)
