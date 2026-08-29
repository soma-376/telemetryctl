# AGENTS.md — telemetryctl

Pulsemetry는 Claude Code·Codex 등 개발 AI 도구의 사용량과 비용을 조직 → 팀 → 구성원 축으로 모아 보여주는
사내 통합·가시화 플랫폼이다.

- 제품·아키텍처·레포 간 계약의 **단일 출처는 `soma-376/docs`**다. 형제 체크아웃 `../docs`를 우선 참조한다.
- 기능 작업 전에는 `spec` 스킬, 설계 관련 작업 전에는 `adr` 스킬을 쓴다.
- **코드와 ADR이 어긋나면 ADR이 기준이다.** 결정을 바꾸려면 `adr-new`로 개정 ADR을 먼저 쓴다.
- git 작업은 `CONVENTION.md`를 따른다 (`conventions` 스킬).
- 스킬이 보이지 않으면 형제 `../agent-skills` 클론 여부를 확인하고, 없으면 사용자에게 클론을 안내한다.
- 문서·주석은 한국어, 코드·파일명은 영어.
- **사용자의 명시 요청 없이 `git push` 하지 않는다.**

---

## 이 레포는 무엇인가

Go로 쓴 CLI(`pulsemetry enroll`)와 데스크탑 데몬. 시스템 아키텍처의 **Desktop Application** + **Local Store**.

```
cmd/telemetryctl/            진입점
internal/
  enrollment/ contract/      서버와의 enroll 계약
  receiver/ otlpdecode/      로컬 OTLP 수신기(127.0.0.1:4318) · 디코드 · 스크럽
  store/ session/           SQLite 로컬 저장 (v3: vendors→sessions→turns→events + 승격 테이블)
  forward/                   회사 엔드포인트로 상위 전송
  config/                    ~/.claude/settings.json · ~/.codex/config.toml 배선
  credential/ autostart/     OS 키링 · launchd/systemd 등록
contracts/*.schema.json      ★ manifest·envelope JSON Schema — 계약의 기계 판독 원본
```

**소유하는 것**: 로컬 수신기, 로컬 집계·보존, 벤더 도구 설정 배선, 데몬 라이프사이클,
그리고 **manifest 계약 스키마 파일**.

**소유하지 않는 것**: 서버 측 토큰 발급 로직, 조직 정책의 결정.

데몬은 **프라이버시 1차 집행 지점**이다. 로컬 배선은 의도적으로 과수집하고,
상위 전송 직전 forwarder가 회사 manifest의 `signals` 게이팅과 `privacy` denylist 스크럽을 적용한다.

## 문서

| 문서 | 담는 것 |
|---|---|
| `docs/development-workflow.md` · `installation-architecture.md` · `local-pipeline.md` | 레포 내부 |
| `docs/adr/` (0001–0008) | 설계 결정 |
| `../docs/contracts/enrollment-api.md` | backend와의 enroll 계약 |
| `../docs/contracts/telemetry-ingest.md` | 파이프라인으로의 전송·인증 계약 |

`contracts/*.schema.json`을 고치면 **backend의 `ManifestPayload` 검증도 함께 고쳐야 한다.**
이것은 계약 변경이며 상대 레포 담당자가 리뷰어다.

## 명령어

```bash
go build ./...
go test ./...
go run ./cmd/telemetryctl enroll --invite <code> --server <url>
```

## 이 레포에서 특히 조심할 것

- **로컬 ingest 토큰과 회사 `ptt_`는 다른 값이다.** 벤더 설정 파일에 들어가는 건 로컬 ingest 토큰이고,
  `ptt_`는 OS 키링에 있다가 forwarder가 상위 전송 시 헤더에 주입한다.
- **enroll 응답은 `DisallowUnknownFields`로 파싱한다.** 서버가 필드를 추가하면 설치가 그 자리에서 실패한다.
  deprecated `invite: ""` 필드는 `omitempty`가 없어 항상 전송되며 **제거하면 안 된다**(서버가 이를 수용한다).
- **로컬 수신기의 큐 포화 응답은 429가 아니라 200 + PartialSuccess다.** 벤더 exporter의 재시도 폭주를 막는 의도된 드롭 정책이다.
- `TimeoutStopSec(20s) > 데몬 shutdown(15s)` 불변식을 깨지 않는다.
- 알려진 미구현: **Windows 자동 시작**(PROJ-56), gRPC 상위 전송, `--force` 플래그 동작.
- ADR을 추가하면 `0009`부터. 파일명은 **한국어 슬러그**. 인덱스는 `docs/adr/README.md` —
  Status 첫 토큰이 바뀌면 같은 커밋에서 표를 갱신한다.
