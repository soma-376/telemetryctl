// 창을 표시하지 않고 패널의 WebView 소유권·재사용·포커스 정책을 검증한다.
#import <AppKit/AppKit.h>
#import <assert.h>
#import "../cmd/pulsemetry-gui/tray_window_darwin.h"

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
        [host release];
    }
    puts("tray panel: native ownership and policy checks passed");
    return 0;
}
