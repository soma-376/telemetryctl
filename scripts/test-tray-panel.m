// 창을 표시하지 않고 패널의 WebView 소유권·재사용·포커스 정책을 검증한다.
#import <AppKit/AppKit.h>
#import <assert.h>
#import <objc/runtime.h>
#import "../cmd/pulsemetry-gui/tray_window_darwin.h"

// 실제 화면을 띄우지 않고 알림이 패널 숨김으로 연결되는지 관찰한다.
static int orderOutCalls;
static void recordOrderOut(id panel, SEL selector, id sender) {
    orderOutCalls++;
}

int main(void) {
    @autoreleasepool {
        [NSApplication sharedApplication];
        assert(pulsemetryTrayPanel(NULL) == NULL);
        assert(!pulsemetryTrayVisible(NULL));
        pulsemetryShowTray(NULL);
        pulsemetryHideTray(NULL);
        NSWindow *host = [[NSWindow alloc] initWithContentRect:NSMakeRect(0, 0, 360, 480)
            styleMask:NSWindowStyleMaskBorderless backing:NSBackingStoreBuffered defer:NO];
        host.releasedWhenClosed = NO;
        NSView *content = host.contentView;
        NSView *child = [[[NSView alloc] initWithFrame:NSMakeRect(0, 0, 50, 50)] autorelease];
        [content addSubview:child];
        NSPanel *panel = (NSPanel *)pulsemetryTrayPanel(host);
        assert([panel isKindOfClass:NSPanel.class]);
        assert(panel.contentView == content);
        assert(child.window == panel);
        assert(host.contentView != content);
        assert(pulsemetryTrayPanel(host) == panel);
        assert(panel.styleMask & NSWindowStyleMaskNonactivatingPanel);
        assert(panel.collectionBehavior & NSWindowCollectionBehaviorFullScreenAuxiliary);
        assert(panel.collectionBehavior & NSWindowCollectionBehaviorCanJoinAllSpaces);
        assert(panel.canBecomeKeyWindow && !panel.canBecomeMainWindow);
        assert(!panel.hidesOnDeactivate);
        assert(!host.visible && !pulsemetryTrayVisible(panel));
        pulsemetryHideTray(panel);
        assert(!pulsemetryTrayVisible(panel));
        assert(class_addMethod(panel.class, @selector(orderOut:), (IMP)recordOrderOut, "v@:@"));
        NSNotificationCenter *center = NSWorkspace.sharedWorkspace.notificationCenter;
        [center postNotificationName:NSWorkspaceDidActivateApplicationNotification object:NSWorkspace.sharedWorkspace];
        assert(orderOutCalls == 0);
        [center postNotificationName:NSWorkspaceActiveSpaceDidChangeNotification object:NSWorkspace.sharedWorkspace];
        assert(orderOutCalls == 1);
        // 캐시된 패널을 다시 얻어도 Space 알림 구독이 중복되지 않는다.
        assert(pulsemetryTrayPanel(host) == panel);
        [center postNotificationName:NSWorkspaceActiveSpaceDidChangeNotification object:NSWorkspace.sharedWorkspace];
        assert(orderOutCalls == 2);
        [host release];
    }
    puts("tray panel: ownership, policy and Space notification checks passed");
    return 0;
}
