#include "drop_darwin.h"

#import <AppKit/AppKit.h>
#import <ApplicationServices/ApplicationServices.h>
#import <UniformTypeIdentifiers/UniformTypeIdentifiers.h>
#include <dispatch/dispatch.h>
#include <pthread.h>
#include <signal.h>
#include <stdatomic.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

#include "_cgo_export.h"

_Static_assert(NSDragOperationNone == 0, "a drag operation changed");
_Static_assert(NSDragOperationCopy == 1, "a drag operation changed");
_Static_assert(NSDragOperationLink == 2, "a drag operation changed");
_Static_assert(NSDragOperationGeneric == 4, "a drag operation changed");
_Static_assert(NSDragOperationPrivate == 8, "a drag operation changed");
_Static_assert(NSDragOperationMove == 16, "a drag operation changed");
_Static_assert(NSDragOperationDelete == 32, "a drag operation changed");

static const CGFloat iconSide = 64;
static const CGFloat pileShift = 4;
static const NSUInteger pileDepth = 4;
static const NSInteger concurrentWrites = 4;
static const CGFloat nearlyClear = 0.01;
static const NSTimeInterval abandonAfterSeconds = 10;
static const NSTimeInterval shieldLifetimeSeconds = 6;
static const NSTimeInterval quitBackstopSeconds = 15;
static const int hungShutdownExitCode = 1;
static const double mainThreadLimitSeconds = 2;

static atomic_bool exitAsked;

bool lanhopOnMainThread(void) {
	return pthread_main_np() != 0;
}

bool lanhopHasGUISession(void) {
	CFDictionaryRef session = CGSessionCopyCurrentDictionary();
	if (session == NULL) {
		return false;
	}
	CFRelease(session);
	return true;
}

static NSArray *mainThreadModes(void) {
	static NSArray *modes;
	static dispatch_once_t once;
	dispatch_once(&once, ^{
		modes = [@[ NSDefaultRunLoopMode, NSEventTrackingRunLoopMode, NSModalPanelRunLoopMode, (id)kCFRunLoopCommonModes ] retain];
	});
	return modes;
}

static void performOnMainThread(void (^work)(void)) {
	CFRunLoopRef main = CFRunLoopGetMain();
	CFRunLoopPerformBlock(main, (CFTypeRef)mainThreadModes(), work);
	CFRunLoopWakeUp(main);
}

@interface LanhopAppDelegate : NSObject <NSApplicationDelegate>
- (void)answerTheQuit;
@end

@implementation LanhopAppDelegate

- (NSApplicationTerminateReply)applicationShouldTerminate:(NSApplication *)sender {
	if (!atomic_load(&exitAsked)) {
		kill(getpid(), SIGTERM);
	}
	[self performSelector:@selector(answerTheQuit) withObject:nil afterDelay:quitBackstopSeconds inModes:mainThreadModes()];
	return NSTerminateLater;
}

- (void)answerTheQuit {
	lanhopExitNow(hungShutdownExitCode);
}

@end

static LanhopAppDelegate *appDelegate;
static id<NSObject> replayingActivity;

void lanhopAppRun(void) {
	@autoreleasepool {
		[NSApplication sharedApplication];
		[NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
		appDelegate = [[LanhopAppDelegate alloc] init];
		[NSApp setDelegate:appDelegate];
		replayingActivity = [[[NSProcessInfo processInfo] beginActivityWithOptions:NSActivityUserInitiatedAllowingIdleSystemSleep reason:@"lanhop replays the keyboard and mouse of another machine"] retain];
		dispatch_async(dispatch_get_main_queue(), ^{
			lanhopAppKitReady();
		});
	}
	[NSApp run];
}

void lanhopAppQuit(void) {
	[NSApp performSelectorOnMainThread:@selector(terminate:) withObject:nil waitUntilDone:NO];
}

void lanhopExitOnMainThread(int code) {
	atomic_store(&exitAsked, true);
	performOnMainThread(^{
		lanhopExitNow(code);
	});
}

static void runOnMainThread(void (^work)(void)) {
	if ([NSThread isMainThread]) {
		work();
		return;
	}
	dispatch_semaphore_t done = dispatch_semaphore_create(0);
	performOnMainThread(^{
		work();
		dispatch_semaphore_signal(done);
	});
	dispatch_semaphore_wait(done, DISPATCH_TIME_FOREVER);
	dispatch_release(done);
}

@interface LanhopAnswer : NSObject {
@public
	int value;
}
@end

@implementation LanhopAnswer
@end

static int answerOnMainThread(int late, int (^work)(void)) {
	if ([NSThread isMainThread]) {
		return work();
	}
	LanhopAnswer *answer = [[LanhopAnswer alloc] init];
	dispatch_semaphore_t answered = dispatch_semaphore_create(0);
	performOnMainThread(^{
		answer->value = work();
		dispatch_semaphore_signal(answered);
	});
	int value = late;
	if (dispatch_semaphore_wait(answered, dispatch_time(DISPATCH_TIME_NOW, (int64_t)(mainThreadLimitSeconds * NSEC_PER_SEC))) == 0) {
		value = answer->value;
	}
	dispatch_release(answered);
	[answer release];
	return value;
}

static NSArray<NSString *> *listOf(const char *joined, int64_t length, int count) {
	NSMutableArray<NSString *> *list = [NSMutableArray arrayWithCapacity:(NSUInteger)count];
	int64_t offset = 0;
	while ((int)[list count] < count) {
		if (offset >= length) {
			return nil;
		}
		size_t entryLength = strnlen(joined + offset, (size_t)(length - offset));
		if ((int64_t)entryLength == length - offset) {
			return nil;
		}
		NSString *entry = [[[NSString alloc] initWithBytes:joined + offset length:entryLength encoding:NSUTF8StringEncoding] autorelease];
		if (entry == nil) {
			return nil;
		}
		[list addObject:entry];
		offset += (int64_t)entryLength + 1;
	}
	return list;
}

static NSString *kindOf(NSString *name, NSString *given) {
	if ([given length] > 0) {
		return given;
	}
	if (@available(macOS 11.0, *)) {
		NSString *extension = [name pathExtension];
		UTType *type = [extension length] > 0 ? [UTType typeWithFilenameExtension:extension] : nil;
		if (type != nil && ![type isDynamic]) {
			return [type identifier];
		}
	}
	return @"public.data";
}

static NSImage *iconOf(NSString *kind) {
	if (@available(macOS 11.0, *)) {
		UTType *type = [UTType typeWithIdentifier:kind];
		if (type != nil) {
			return [[NSWorkspace sharedWorkspace] iconForContentType:type];
		}
	}
	return [NSImage imageNamed:NSImageNameMultipleDocuments];
}

static void giveBackActivation(NSRunningApplication *previous) {
	if (previous == nil || [previous isTerminated] || ![NSApp isActive] || [previous isEqual:[NSRunningApplication currentApplication]]) {
		return;
	}
	[previous activateWithOptions:0];
}

static int placement(CGWindowID own, CGPoint at) {
	CFArrayRef listed = CGWindowListCopyWindowInfo(kCGWindowListOptionIncludingWindow, own);
	if (listed == NULL) {
		return LANHOP_DROP_WINDOW_NOT_SHOWN;
	}
	int state = LANHOP_DROP_WINDOW_NOT_SHOWN;
	for (NSDictionary *info in (NSArray *)listed) {
		if ([[info objectForKey:(id)kCGWindowNumber] unsignedIntValue] != own || ![[info objectForKey:(id)kCGWindowIsOnscreen] boolValue]) {
			continue;
		}
		CGRect bounds;
		CFDictionaryRef bounding = (CFDictionaryRef)[info objectForKey:(id)kCGWindowBounds];
		BOOL around = bounding != NULL && CGRectMakeWithDictionaryRepresentation(bounding, &bounds) && CGRectContainsPoint(bounds, at);
		state = around ? LANHOP_DROP_WINDOW_READY : LANHOP_DROP_WINDOW_MISSES_THE_CURSOR;
	}
	CFRelease(listed);
	return state;
}

static NSPoint onTheScreens(CGPoint at) {
	NSScreen *primary = [[NSScreen screens] firstObject];
	return NSMakePoint(at.x, (primary != nil ? NSMaxY([primary frame]) : 0) - at.y);
}

@class LanhopDropSource;

@interface LanhopDropView : NSView {
	LanhopDropSource *source;
}
- (instancetype)initWithFrame:(NSRect)frame source:(LanhopDropSource *)owner;
- (void)forgetSource;
@end

@interface LanhopDropSource : NSObject <NSDraggingSource, NSFilePromiseProviderDelegate> {
@public
	uint64_t dropID;
	NSArray<NSString *> *names;
	NSArray<NSString *> *kinds;
	NSWindow *window;
	LanhopDropView *view;
	CGWindowID windowNumber;
	CGPoint home;
	NSOperationQueue *writes;
	NSRunningApplication *frontBefore;
	BOOL asked;
	BOOL ended;
	BOOL closed;
	BOOL flushed;
	BOOL abandoned;
	BOOL refusing;
	BOOL shield;
}
- (void)beginWithEvent:(NSEvent *)event fromView:(NSView *)origin;
- (void)closeWindow;
- (void)abandon;
- (void)expire;
@end

@implementation LanhopDropView

- (instancetype)initWithFrame:(NSRect)frame source:(LanhopDropSource *)owner {
	self = [super initWithFrame:frame];
	if (self != nil) {
		source = owner;
	}
	return self;
}

- (void)forgetSource {
	source = nil;
}

- (BOOL)acceptsFirstMouse:(NSEvent *)event {
	return YES;
}

- (void)mouseDown:(NSEvent *)event {
	if (source != nil) {
		lanhopDropMouseDown(source->dropID);
	}
}

- (void)mouseDragged:(NSEvent *)event {
	[source beginWithEvent:event fromView:self];
}

@end

@implementation LanhopDropSource

- (void)dealloc {
	NSOperationQueue *queue = writes;
	[queue cancelAllOperations];
	[self closeWindow];
	if ([NSThread isMainThread]) {
		[queue release];
	} else {
		performOnMainThread(^{
			[queue release];
		});
	}
	[frontBefore release];
	[names release];
	[kinds release];
	[super dealloc];
}

- (void)closeWindow {
	NSWindow *shown = window;
	LanhopDropView *content = view;
	window = nil;
	view = nil;
	if (shown == nil) {
		return;
	}
	void (^closing)(void) = ^{
		[content forgetSource];
		[shown orderOut:nil];
		[shown close];
		[shown release];
	};
	if ([NSThread isMainThread]) {
		closing();
	} else {
		performOnMainThread(closing);
	}
}

- (void)abandon {
	if (ended) {
		return;
	}
	ended = YES;
	abandoned = YES;
	[writes cancelAllOperations];
	[self closeWindow];
	giveBackActivation(frontBefore);
	lanhopDropSessionOver(dropID);
}

- (void)expire {
	if (closed) {
		return;
	}
	[self closeWindow];
}

- (void)beginWithEvent:(NSEvent *)event fromView:(NSView *)origin {
	if (asked || closed || shield) {
		return;
	}
	asked = YES;
	NSPoint at = [origin convertPoint:[event locationInWindow] fromView:nil];
	NSMutableArray<NSDraggingItem *> *items = [NSMutableArray arrayWithCapacity:[names count]];
	for (NSUInteger i = 0; i < [names count]; i++) {
		NSString *kind = [kinds objectAtIndex:i];
		NSFilePromiseProvider *provider = [[NSFilePromiseProvider alloc] initWithFileType:kind delegate:self];
		[provider setUserInfo:[NSNumber numberWithUnsignedInteger:i]];
		NSDraggingItem *item = [[NSDraggingItem alloc] initWithPasteboardWriter:provider];
		CGFloat shift = pileShift * (CGFloat)MIN(i, pileDepth);
		[item setDraggingFrame:NSMakeRect(at.x - iconSide / 2 + shift, at.y - iconSide / 2 - shift, iconSide, iconSide) contents:iconOf(kind)];
		[items addObject:item];
		[item release];
		[provider release];
	}
	NSDraggingSession *session = [origin beginDraggingSessionWithItems:items event:event source:self];
	[session setAnimatesToStartingPositionsOnCancelOrFail:YES];
}

- (NSDragOperation)draggingSession:(NSDraggingSession *)session sourceOperationMaskForDraggingContext:(NSDraggingContext)context {
	return refusing ? NSDragOperationNone : NSDragOperationCopy;
}

- (void)draggingSession:(NSDraggingSession *)session willBeginAtPoint:(NSPoint)screenPoint {
	lanhopDropBegan(dropID);
}

- (void)draggingSession:(NSDraggingSession *)session endedAtPoint:(NSPoint)screenPoint operation:(NSDragOperation)operation {
	if (abandoned) {
		abandoned = NO;
		performOnMainThread(^{
			[self release];
		});
		return;
	}
	if (ended) {
		return;
	}
	ended = YES;
	bool held = CGEventSourceButtonState(kCGEventSourceStateCombinedSessionState, kCGMouseButtonLeft);
	[window orderOut:nil];
	giveBackActivation(frontBefore);
	lanhopDropEnded(dropID, (uint64_t)operation, held);
	if (closed) {
		performOnMainThread(^{
			[self release];
		});
	}
}

- (NSString *)filePromiseProvider:(NSFilePromiseProvider *)provider fileNameForType:(NSString *)fileType {
	return [names objectAtIndex:[[provider userInfo] unsignedIntegerValue]];
}

- (NSOperationQueue *)operationQueueForFilePromiseProvider:(NSFilePromiseProvider *)provider {
	return writes;
}

- (void)filePromiseProvider:(NSFilePromiseProvider *)provider writePromiseToURL:(NSURL *)url completionHandler:(void (^)(NSError *errorOrNil))completionHandler {
	[self retain];
	@autoreleasepool {
		NSUInteger index = [[provider userInfo] unsignedIntegerValue];
		NSString *reason = @"lanhop can write a dropped file only to a file URL";
		if ([url isFileURL]) {
			reason = nil;
			char *failure = lanhopDropWrite(dropID, (int)index, (char *)[url fileSystemRepresentation]);
			if (failure != NULL) {
				reason = [NSString stringWithUTF8String:failure];
				if (reason == nil) {
					reason = @"lanhop could not write the dropped file";
				}
				free(failure);
			}
		}
		NSError *error = nil;
		if (reason != nil) {
			error = [NSError errorWithDomain:NSCocoaErrorDomain code:NSFileWriteUnknownError userInfo:@{NSLocalizedDescriptionKey : reason}];
		}
		completionHandler(error);
	}
	[self release];
}

@end

static LanhopDropSource *openOnMainThread(uint64_t dropID, NSArray<NSString *> *names, NSArray<NSString *> *givenKinds, CGPoint home, CGFloat side, BOOL shield) {
	NSScreen *primary = [[NSScreen screens] firstObject];
	if (primary == nil) {
		return nil;
	}
	NSMutableArray<NSString *> *kinds = [NSMutableArray arrayWithCapacity:[names count]];
	for (NSUInteger i = 0; i < [names count]; i++) {
		[kinds addObject:kindOf([names objectAtIndex:i], [givenKinds objectAtIndex:i])];
	}
	LanhopDropSource *source = [[LanhopDropSource alloc] init];
	source->dropID = dropID;
	source->names = [names copy];
	source->kinds = [kinds copy];
	source->home = home;
	source->shield = shield;
	if (!shield) {
		source->writes = [[NSOperationQueue alloc] init];
		[source->writes setMaxConcurrentOperationCount:concurrentWrites];
		source->frontBefore = [[[NSWorkspace sharedWorkspace] frontmostApplication] retain];
	}

	NSRect frame = NSMakeRect(home.x - side / 2, NSMaxY([primary frame]) - home.y - side / 2, side, side);
	NSWindow *window = nil;
	if (shield) {
		NSPanel *panel = [[NSPanel alloc] initWithContentRect:frame styleMask:NSWindowStyleMaskBorderless | NSWindowStyleMaskNonactivatingPanel backing:NSBackingStoreBuffered defer:NO];
		[panel setBecomesKeyOnlyIfNeeded:YES];
		[panel setHidesOnDeactivate:NO];
		window = panel;
	} else {
		window = [[NSWindow alloc] initWithContentRect:frame styleMask:NSWindowStyleMaskBorderless backing:NSBackingStoreBuffered defer:NO];
	}
	[window setReleasedWhenClosed:NO];
	[window setLevel:NSStatusWindowLevel];
	[window setOpaque:NO];
	[window setHasShadow:NO];
	[window setBackgroundColor:[NSColor colorWithCalibratedWhite:0 alpha:nearlyClear]];
	[window setIgnoresMouseEvents:NO];
	[window setCollectionBehavior:NSWindowCollectionBehaviorCanJoinAllSpaces | NSWindowCollectionBehaviorFullScreenAuxiliary | NSWindowCollectionBehaviorTransient | NSWindowCollectionBehaviorIgnoresCycle];
	LanhopDropView *view = [[LanhopDropView alloc] initWithFrame:NSMakeRect(0, 0, side, side) source:source];
	[window setContentView:view];
	[view release];
	source->window = window;
	source->view = view;
	[window orderFrontRegardless];
	if (!shield) {
		if (@available(macOS 14.0, *)) {
			[NSApp activate];
		} else {
			[NSApp activateIgnoringOtherApps:YES];
		}
	}
	source->windowNumber = (CGWindowID)[window windowNumber];
	return source;
}

void *lanhopDropOpen(uint64_t dropID, const char *names, int64_t namesLength, const char *kinds, int64_t kindsLength, int count, double x, double y, double side) {
	__block LanhopDropSource *opened = nil;
	@autoreleasepool {
		NSArray<NSString *> *nameList = listOf(names, namesLength, count);
		NSArray<NSString *> *kindList = listOf(kinds, kindsLength, count);
		if (count <= 0 || nameList == nil || kindList == nil) {
			return NULL;
		}
		runOnMainThread(^{
			opened = openOnMainThread(dropID, nameList, kindList, CGPointMake(x, y), side, NO);
		});
	}
	return opened;
}

void *lanhopShieldOpen(double x, double y, double side) {
	__block LanhopDropSource *opened = nil;
	runOnMainThread(^{
		@autoreleasepool {
			opened = openOnMainThread(0, @[], @[], CGPointMake(x, y), side, YES);
			[opened performSelector:@selector(expire) withObject:nil afterDelay:shieldLifetimeSeconds inModes:mainThreadModes()];
		}
	});
	return opened;
}

static NSInteger windowTakingTheMouseAt(CGPoint at) {
	return [NSWindow windowNumberAtPoint:onTheScreens(at) belowWindowWithWindowNumber:0];
}

static int stateOf(LanhopDropSource *source) {
	if (source->window == nil) {
		return LANHOP_DROP_WINDOW_NOT_SHOWN;
	}
	if (!source->flushed) {
		[source->window displayIfNeeded];
		source->flushed = YES;
	}
	if (source->windowNumber == 0) {
		return LANHOP_DROP_WINDOW_NOT_SHOWN;
	}
	int placed = placement(source->windowNumber, source->home);
	if (placed != LANHOP_DROP_WINDOW_READY) {
		return placed;
	}
	NSInteger taking = windowTakingTheMouseAt(source->home);
	if (taking == [source->window windowNumber]) {
		return LANHOP_DROP_WINDOW_READY;
	}
	return taking > 0 ? LANHOP_DROP_WINDOW_COVERED : LANHOP_DROP_WINDOW_MISSES_THE_CURSOR;
}

static int moveUnder(LanhopDropSource *source, CGPoint at) {
	if (source->window == nil || ![source->window isVisible]) {
		return LANHOP_DROP_MISSES_THE_MOUSE;
	}
	NSPoint cursor = onTheScreens(at);
	NSRect frame = [source->window frame];
	[source->window setFrameOrigin:NSMakePoint(cursor.x - NSWidth(frame) / 2, cursor.y - NSHeight(frame) / 2)];
	[source->window displayIfNeeded];
	BOOL placed = placement(source->windowNumber, at) == LANHOP_DROP_WINDOW_READY;
	BOOL taken = windowTakingTheMouseAt(at) == [source->window windowNumber];
	return placed && taken ? LANHOP_DROP_TAKES_THE_MOUSE : LANHOP_DROP_MISSES_THE_MOUSE;
}

int lanhopDropWindowState(void *drop) {
	LanhopDropSource *source = (LanhopDropSource *)drop;
	return answerOnMainThread(LANHOP_DROP_WINDOW_NOT_SHOWN, ^int(void) {
		@autoreleasepool {
			return stateOf(source);
		}
	});
}

bool lanhopDropRefuseDrops(void *drop) {
	LanhopDropSource *source = (LanhopDropSource *)drop;
	return answerOnMainThread(0, ^int(void) {
		source->refusing = YES;
		return 1;
	}) == 1;
}

int lanhopDropTakeTheMouseAt(void *drop, double x, double y) {
	LanhopDropSource *source = (LanhopDropSource *)drop;
	return answerOnMainThread(LANHOP_DROP_MAIN_THREAD_LATE, ^int(void) {
		@autoreleasepool {
			return moveUnder(source, CGPointMake(x, y));
		}
	});
}

bool lanhopDropClose(void *drop) {
	LanhopDropSource *source = (LanhopDropSource *)drop;
	return answerOnMainThread(0, ^int(void) {
		[NSObject cancelPreviousPerformRequestsWithTarget:source selector:@selector(expire) object:nil];
		source->closed = YES;
		source->refusing = YES;
		[source->window orderOut:nil];
		giveBackActivation(source->frontBefore);
		if (source->asked && !source->ended) {
			[source performSelector:@selector(abandon) withObject:nil afterDelay:abandonAfterSeconds inModes:mainThreadModes()];
			return 1;
		}
		lanhopDropSessionOver(source->dropID);
		[source release];
		return 1;
	}) == 1;
}

bool lanhopAppIsActive(void) {
	return answerOnMainThread(0, ^int(void) {
		return [NSApp isActive] ? 1 : 0;
	}) == 1;
}
