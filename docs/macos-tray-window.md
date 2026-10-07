# macOS 전체 화면의 트레이 퀵뷰

macOS 퀵뷰는 앱을 활성화하지 않는 `NSPanel`에 표시한다. 패널을 만들 때
`NSWindowStyleMaskNonactivatingPanel`을 지정하고, 모든 Space에 참여하는
`CanJoinAllSpaces | FullScreenAuxiliary`를 적용한다. macOS 13 이상에서는
`CanJoinAllApplications`도 지정한다. 메인 창은 기존 Wails 창을 유지한다.

`macTrayWindow` 어댑터는 Wails가 만든 WebView를 패널로 옮겨 기존 프런트엔드와 IPC를
재사용한다. 원래 창은 숨은 호스트로 남아 패널의 수명을 소유한다. 트레이의 위치 계산에는
패널 핸들을 전달하고, 표시 여부·열기·닫기·포커스도 패널로 연결한다. `Focus()`에서
앱 전체를 활성화하지 않으므로 메인 창이 있는 데스크톱으로 전환하지 않는다.
퀵뷰의 X 버튼과 메인 창 열기는 `App.HideTrayWindow()`로 실제 패널을 닫는다.
Windows·Linux는 기존 Wails 창과 트레이 동작을 유지한다.

다른 데스크톱이나 전체 화면 Space로 이동하면 `NSWorkspaceActiveSpaceDidChangeNotification`을
받아 패널을 숨긴다. 이전 Space로 돌아와도 닫힌 상태를 유지하며, 같은 Space에서 다른 앱을
클릭하는 것만으로는 닫히지 않는다. 구독은 패널당 한 번 등록하고 패널 해제 시 정리한다.

설계 결정은 [ADR 0035](adr/0035-맥-트레이-퀵뷰를-비활성화-네이티브-패널에-표시한다.md)에 있다.
Wails 모듈 캐시와 private API는 수정하지 않는다.

## 네이티브 회귀 검사

macOS에서 다음 명령으로 화면을 표시하지 않고 뷰 소유권·패널 재사용·포커스 정책을 검사한다.

```sh
clang -framework AppKit scripts/test-tray-panel.m cmd/pulsemetry-gui/tray_window_darwin.m -o /tmp/pulsemetry-test-tray-panel
/tmp/pulsemetry-test-tray-panel
```

## 수동 검증

1. Pulsemetry 메인 창을 일반 데스크톱에 두고 다른 앱을 전체 화면으로 전환한다.
2. 메뉴 막대에서 트레이 아이콘을 한 번 클릭한다. 데스크톱으로 전환되지 않고 퀵뷰가 보여야 한다.
3. 퀵뷰 버튼·스크롤을 조작한다. 첫 상호작용에서도 Space가 전환되지 않아야 한다.
4. 트레이 아이콘을 다시 클릭하거나 X 버튼을 눌러 닫는다. 다른 Space에서 다시 열어 위치를 확인한다.
5. 트레이 메뉴의 **열기**로 메인 창이 정상 활성화되는지 확인한다.
6. 다중 모니터·자동 숨김 메뉴 막대에서도 아이콘 아래에 표시되는지 확인한다.
7. 퀵뷰를 연 채 다른 데스크톱·전체 화면 Space로 이동하면 닫히는지 확인한다. 돌아왔을 때도 닫혀 있어야 한다.

사용자가 전체 화면에서 퀵뷰 열기와 X 버튼으로 닫기를 확인했다. Space 이동 시 자동 닫기는
알림 회귀 검사와 별도로 위 수동 항목에서 확인한다.

빌드·정적 검사와 네이티브 회귀 검사는 위 화면 동작의 성공을 증명하지 않는다.
전체 화면 표시와 상호작용은 수동 검증 결과를 별도로 확인한다.

근거: [Apple — NSPanel](https://developer.apple.com/documentation/appkit/nspanel),
[CanJoinAllApplications](https://developer.apple.com/documentation/appkit/nswindow/collectionbehavior-swift.struct/canjoinallapplications).
[Space 변경 알림](https://developer.apple.com/documentation/appkit/nsworkspace/activespacedidchangenotification).
