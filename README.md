# telemetryctl

Codex와 Claude Code에 조직의 OpenTelemetry 설정을 안전하게 적용하기 위한 Go 기반 CLI입니다.

현재는 설정 manifest 검증과 Codex·Claude Code 설정 병합 로직을 구현한 초기 단계입니다. CLI 진입점, Enrollment API 연동, 설치 상태 저장은 아직 구현되지 않았습니다.

## 구조

```text
.
├── contracts/              # Enrollment 서버와 공유하는 JSON Schema
├── docs/                   # 설치 아키텍처와 개발 워크플로
└── internal/
    ├── configmerge/        # Codex·Claude Code 설정 병합 및 백업
    ├── hostenv/            # OS·WSL 감지와 설정 파일 경로 계산
    └── manifest/           # Enrollment manifest 파싱 및 검증
```

구현되지 않은 디렉터리는 파일이 추가되는 시점에 생성합니다.

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

1. `cmd/telemetryctl/main.go`와 명령 구조
2. Enrollment API 클라이언트와 인증 흐름
3. 설치·복구·제거를 조정하는 installer 계층
4. 로컬 설치 상태 저장소
5. Manifest JSON Schema 계약 테스트와 Codex 병합 테스트
