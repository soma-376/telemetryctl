#import <AppKit/AppKit.h>
#import <objc/runtime.h>
#import "tray_window_darwin.h"

@interface PulsemetryTrayPanel : NSPanel
- (void)activeSpaceDidChange:(NSNotification *)notification;
@end

@implementation PulsemetryTrayPanel
- (BOOL)canBecomeKeyWindow { return YES; }
- (BOOL)canBecomeMainWindow { return NO; }
- (void)activeSpaceDidChange:(NSNotification *)notification {
    // 앱 전환과 Space 전환을 구분한다. 숨김 이벤트는 기존 Wails delegate가 전달한다.
    if (NSThread.isMainThread) {
        [self orderOut:nil];
    } else {
        dispatch_async(dispatch_get_main_queue(), ^{ [self orderOut:nil]; });
    }
}
- (void)dealloc {
    [NSWorkspace.sharedWorkspace.notificationCenter removeObserver:self];
    [super dealloc];
}
@end

static char panelKey;

void *pulsemetryTrayPanel(void *owner) {
    if (owner == NULL) return NULL;
    NSWindow *host = (NSWindow *)owner;
    PulsemetryTrayPanel *panel = objc_getAssociatedObject(host, &panelKey);
    if (panel != nil) return panel;

    NSView *content = host.contentView;
    if (content == nil) return NULL;
    panel = [[PulsemetryTrayPanel alloc]
        initWithContentRect:NSMakeRect(0, 0, content.frame.size.width, content.frame.size.height)
        styleMask:NSWindowStyleMaskBorderless | NSWindowStyleMaskNonactivatingPanel
        backing:NSBackingStoreBuffered defer:NO];
    panel.title = host.title;
    panel.releasedWhenClosed = NO;
    panel.floatingPanel = YES;
    panel.hidesOnDeactivate = NO;
    panel.becomesKeyOnlyIfNeeded = NO;
    panel.level = NSPopUpMenuWindowLevel;
    panel.collectionBehavior = NSWindowCollectionBehaviorCanJoinAllSpaces |
        NSWindowCollectionBehaviorFullScreenAuxiliary;
    if (@available(macOS 13.0, *)) {
        panel.collectionBehavior |= NSWindowCollectionBehaviorCanJoinAllApplications;
    }
    panel.opaque = NO;
    panel.backgroundColor = NSColor.clearColor;
    panel.hasShadow = YES;
    // Wails의 창 ID가 붙은 delegate를 공유해 기존 표시·숨김 이벤트를 유지한다.
    panel.delegate = host.delegate;
    // NSWorkspace 알림은 기본 NotificationCenter가 아닌 전용 센터에서 구독한다.
    [NSWorkspace.sharedWorkspace.notificationCenter addObserver:panel
        selector:@selector(activeSpaceDidChange:)
        name:NSWorkspaceActiveSpaceDidChangeNotification object:NSWorkspace.sharedWorkspace];

    // 원래 창은 숨은 IPC 호스트로 남긴다. 패널이 먼저 뷰를 소유해 이동 중 해제되지 않게 한다.
    [content retain];
    host.contentView = nil;
    panel.contentView = content;
    [content release];
    objc_setAssociatedObject(host, &panelKey, panel, OBJC_ASSOCIATION_RETAIN_NONATOMIC);
    [panel release];
    return panel;
}

bool pulsemetryTrayVisible(void *panel) {
    return panel != NULL && [(NSPanel *)panel isVisible];
}

void pulsemetryShowTray(void *panel) {
    if (panel != NULL) [(NSPanel *)panel makeKeyAndOrderFront:nil];
}

void pulsemetryHideTray(void *panel) {
    if (panel != NULL) [(NSPanel *)panel orderOut:nil];
}
