#include "drop_darwin.h"

#import <AppKit/AppKit.h>
#import <ApplicationServices/ApplicationServices.h>
#import <UniformTypeIdentifiers/UniformTypeIdentifiers.h>
#include <dispatch/dispatch.h>
#include <pthread.h>
#include <stdlib.h>
#include <string.h>

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

bool lanhopOnMainThread(void) {
	return pthread_main_np() != 0;
}

static id<NSObject> replayingActivity;

void lanhopAppRun(void) {
	@autoreleasepool {
		[NSApplication sharedApplication];
		[NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
		NSActivityOptions replaying = NSActivityLatencyCritical | NSActivityUserInitiatedAllowingIdleSystemSleep;
		replayingActivity = [[[NSProcessInfo processInfo] beginActivityWithOptions:replaying reason:@"lanhop replays the keyboard and mouse of another machine"] retain];
		dispatch_async(dispatch_get_main_queue(), ^{
			lanhopAppKitReady();
		});
	}
	[NSApp run];
}

static void runOnMainThread(void (^work)(void)) {
	if ([NSThread isMainThread]) {
		work();
	} else {
		dispatch_sync(dispatch_get_main_queue(), work);
	}
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

static BOOL onScreen(CGWindowID own) {
	CFArrayRef listed = CGWindowListCopyWindowInfo(kCGWindowListOptionIncludingWindow, own);
	if (listed == NULL) {
		return NO;
	}
	BOOL shown = NO;
	for (NSDictionary *info in (NSArray *)listed) {
		if ([[info objectForKey:(id)kCGWindowNumber] unsignedIntValue] == own) {
			shown = [[info objectForKey:(id)kCGWindowIsOnscreen] boolValue];
		}
	}
	CFRelease(listed);
	return shown;
}

static BOOL coveredAt(CGWindowID own, CGPoint at) {
	CFArrayRef above = CGWindowListCopyWindowInfo(kCGWindowListOptionOnScreenAboveWindow, own);
	if (above == NULL) {
		return NO;
	}
	BOOL covered = NO;
	for (NSDictionary *info in (NSArray *)above) {
		CGRect bounds;
		CFDictionaryRef bounding = (CFDictionaryRef)[info objectForKey:(id)kCGWindowBounds];
		if (bounding == NULL || !CGRectMakeWithDictionaryRepresentation(bounding, &bounds) || !CGRectContainsPoint(bounds, at)) {
			continue;
		}
		NSNumber *alpha = [info objectForKey:(id)kCGWindowAlpha];
		if (alpha != nil && [alpha doubleValue] <= 0) {
			continue;
		}
		covered = YES;
		break;
	}
	CFRelease(above);
	return covered;
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
}
- (void)beginWithEvent:(NSEvent *)event fromView:(NSView *)origin;
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
	NSWindow *shown = window;
	LanhopDropView *content = view;
	NSOperationQueue *queue = writes;
	void (^closeWindow)(void) = ^{
		[content forgetSource];
		[shown orderOut:nil];
		[shown close];
		[shown release];
		[queue release];
	};
	[writes cancelAllOperations];
	if ([NSThread isMainThread]) {
		closeWindow();
	} else {
		dispatch_async(dispatch_get_main_queue(), closeWindow);
	}
	[frontBefore release];
	[names release];
	[kinds release];
	[super dealloc];
}

- (void)beginWithEvent:(NSEvent *)event fromView:(NSView *)origin {
	if (asked || closed) {
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
	return NSDragOperationCopy;
}

- (void)draggingSession:(NSDraggingSession *)session willBeginAtPoint:(NSPoint)screenPoint {
	lanhopDropBegan(dropID);
}

- (void)draggingSession:(NSDraggingSession *)session endedAtPoint:(NSPoint)screenPoint operation:(NSDragOperation)operation {
	if (ended) {
		return;
	}
	ended = YES;
	bool held = CGEventSourceButtonState(kCGEventSourceStateCombinedSessionState, kCGMouseButtonLeft);
	[window orderOut:nil];
	giveBackActivation(frontBefore);
	lanhopDropEnded(dropID, (uint64_t)operation, held);
	if (closed) {
		dispatch_async(dispatch_get_main_queue(), ^{
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

static LanhopDropSource *openOnMainThread(uint64_t dropID, NSArray<NSString *> *names, NSArray<NSString *> *givenKinds, CGPoint home, CGFloat side) {
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
	source->writes = [[NSOperationQueue alloc] init];
	[source->writes setMaxConcurrentOperationCount:concurrentWrites];
	source->frontBefore = [[[NSWorkspace sharedWorkspace] frontmostApplication] retain];

	NSRect frame = NSMakeRect(home.x - side / 2, NSMaxY([primary frame]) - home.y - side / 2, side, side);
	NSWindow *window = [[NSWindow alloc] initWithContentRect:frame styleMask:NSWindowStyleMaskBorderless backing:NSBackingStoreBuffered defer:NO];
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
	if (@available(macOS 14.0, *)) {
		[NSApp activate];
	} else {
		[NSApp activateIgnoringOtherApps:YES];
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
			opened = openOnMainThread(dropID, nameList, kindList, CGPointMake(x, y), side);
		});
	}
	return opened;
}

int lanhopDropWindowState(void *drop) {
	LanhopDropSource *source = (LanhopDropSource *)drop;
	@autoreleasepool {
		if (source->windowNumber == 0 || !onScreen(source->windowNumber)) {
			return LANHOP_DROP_WINDOW_NOT_SHOWN;
		}
		return coveredAt(source->windowNumber, source->home) ? LANHOP_DROP_WINDOW_COVERED : LANHOP_DROP_WINDOW_READY;
	}
}

void lanhopDropClose(void *drop) {
	LanhopDropSource *source = (LanhopDropSource *)drop;
	dispatch_async(dispatch_get_main_queue(), ^{
		source->closed = YES;
		[source->window orderOut:nil];
		giveBackActivation(source->frontBefore);
		if (source->asked && !source->ended) {
			return;
		}
		[source release];
	});
}
