# telemetryctl

Codex와 Claude Code에 조직의 OpenTelemetry 설정을 안전하게 적용하기 위한 Go 기반 CLI입니다.

현재는 로컬 manifest 파일을 입력받아 설정을 적용하는 CLI(`install`·`status`)와 설치 상태 저장까지 구현한 단계입니다. Enrollment API 연동과 `uninstall`은 아직 구현되지 않았습니다.

## 구조

```text
.
├── cmd/telemetryctl/       # CLI 진입점 (install·status·version)
├── contracts/              # Enrollment 서버와 공유하는 JSON Schema
├── docs/                   # 설치 아키텍처와 개발 워크플로
└── internal/
    ├── configmerge/        # Codex·Claude Code 설정 병합 및 백업
    ├── hostenv/            # OS·WSL 감지와 설정 파일 경로 계산
    ├── installer/          # 설치 흐름 오케스트레이션과 로컬 상태 저장
    └── manifest/           # Enrollment manifest 파싱 및 검증
```

구현되지 않은 디렉터리는 파일이 추가되는 시점에 생성합니다.

## 사용법

```sh
telemetryctl install --manifest ./enrollment.json   # enroll 응답 봉투를 읽어 설정 적용
telemetryctl status                                 # 현재 설치 상태 표시
```

`install` 은 Claude Code(`~/.claude/settings.json`)와 Codex(`~/.codex/config.toml`)에 OTel 키만
병합하고, 적용 내역을 `~/.telemetryctl/state.json` 에 기록합니다. Enrollment API 연동 전까지는
enroll 응답 봉투(`{installation_id, installation_token, manifest}`)를 로컬 파일로 전달합니다.
`manifest` 는 순수 설정이고, 설치 정체성·토큰은 봉투 상위에 있습니다.

## 핵심 원칙

- 기존 설정 파일 전체를 덮어쓰지 않고 필요한 OTel 키만 병합합니다.
- 기존 파일은 최초 변경 전에 `.telemetryctl.bak`으로 백업합니다.
- 다른 OTel endpoint가 있으면 명시적인 강제 옵션 없이는 교체하지 않습니다.
- manifest의 알 수 없는 필드와 지원하지 않는 버전을 거부합니다.
- 토큰을 로그에 출력하지 않습니다.

## 개발

요구 사항은 Go 1.23 이상입니다.

```sh
go test ./...
```

자세한 설계는 [설치 아키텍처](docs/installation-architecture.md), 협업 규칙은 [개발 워크플로](docs/development-workflow.md)를 참고하세요.

## 다음 구현 대상

1. Enrollment API 클라이언트와 브라우저/device-code 인증 흐름
2. `uninstall`·`repair` (상태의 관리 키만 제거하여 설치와 대칭 복구)
3. Windows·WSL 동시 대상 지원과 설정 drift 감지
4. Manifest JSON Schema 계약 테스트 (스키마 ↔ Go 타입 드리프트 방지)
5. `resource_attributes` → `OTEL_RESOURCE_ATTRIBUTES` 배선 (회사 단위 태깅)
