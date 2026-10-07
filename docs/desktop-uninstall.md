# 데스크톱 제거

앱 설정의 **Pulsemetry 제거**는 독립 제거 창을 연다. Windows는 설정 → 앱의 제거 항목,
macOS는 `~/Applications/Uninstall Pulsemetry.app`, Linux는 앱 메뉴의 **Uninstall Pulsemetry**에서도 연다.
로컬 사용 기록 삭제는 기본으로 꺼져 있다. 서버에 전송한 기록은 이 화면에서 삭제하지 않는다.

제거는 자동 시작 해제와 데몬 종료를 확인한 뒤 관리 설정·로컬 자격증명·등록한 프로그램 파일을 정리한다.
사용자가 바꾼 파일과 추가한 파일은 남기고 결과에 표시한다. 실패하면 제거 도구를 유지해 재시도한다.
성공 화면에서 **닫고 마무리**를 누르면 독립 작업이 제거 도구와 OS 제거 항목을 정리한다.
마지막 정리가 실패하면 `~/.pulsemetry/uninstall-error.txt`에 이유를 남긴다.

## 설치 경로와 소유권

`~/.pulsemetry/product-installation.json`은 GUI·CLI·제거 도구·런처의 경로와 SHA-256을 기록한다.
디렉터리 전체 삭제는 하지 않는다. 파일 지문이 달라졌으면 사용자 변경으로 취급해 보존한다.
개발용 `.dev.app`와 작업 트리 실행 파일은 등록하지 않는다.

| OS | GUI | 독립 제거 도구 |
|---|---|---|
| Windows | `%LOCALAPPDATA%/Programs/Pulsemetry/Pulsemetry.exe` | `%USERPROFILE%/.pulsemetry/uninstaller/PulsemetryUninstall.exe` |
| macOS | `~/Applications/Pulsemetry.app` 또는 `/Applications/Pulsemetry.app` | `~/Applications/Uninstall Pulsemetry.app` |
| Linux | 설치한 `Pulsemetry.AppImage` | `~/.local/share/pulsemetry/PulsemetryUninstall.AppImage` |

Windows NSIS는 사용자별 고정 경로에 설치하고 제거를 전용 창에 위임한다.
macOS 패키지에는 별도 식별자의 제거 앱을 포함하며, 최초 실행 또는 부트스트랩 설치가 이를 복사한다.
Linux는 AppImage의 독립 복사본과 두 `.desktop` 진입점을 등록한다.
CLI 파일은 부트스트랩 고정 경로만 관리한다. 수동 다운로드한 임의 CLI는 자동 인수하지 않는다.

curl/irm 부트스트랩은 CLI와 GUI를 모두 설치한다. backend에도 같은 릴리스의 GUI 파일을 배치해야 한다.
구형 시스템 전체 Windows 설치는 자동 인수하지 않는다. `/Applications` 파일에 삭제 권한이 없으면
오류를 반환하므로 사용자 설치 경로를 권장한다. 이 구현은 관리자 권한을 요청하지 않는다.

## 검증

- `task test`: 파일 변경·링크 보존, 실패 후 재시도, 선택적 DB 삭제, 제거 화면 상호작용.
- `task build`와 `task package:gui`: 현재 OS의 실제 GUI·패키지 빌드.
- macOS 패키징 뒤 `python3 scripts/test-desktop-uninstall.py`: 임시 HOME에서 본 앱 선삭제 후 독립 제거와 자체 정리를 검증.
  실제 launchctl·키체인 실행은 차단한다.
- Windows·Linux의 OS 제거 진입점과 실제 서비스 종료는 해당 OS의 격리된 사용자 환경에서 검증한다.

설계의 기준은 문서 허브 ADR 0017과 로컬 ADR 0022다.
