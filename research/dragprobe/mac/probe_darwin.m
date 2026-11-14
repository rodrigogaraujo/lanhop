#include "probe_darwin.h"

#import <AppKit/AppKit.h>
#import <ApplicationServices/ApplicationServices.h>
#import <UniformTypeIdentifiers/UniformTypeIdentifiers.h>
#include <dispatch/dispatch.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

#include "_cgo_export.h"

static const CGKeyCode escapeKeyCode = 53;
static const CGFloat squareSide = 120;

static NSInteger endOperation = LANHOP_PROBE_NO_SESSION;
static BOOL sessionEnded = NO;
static BOOL writeAsked = NO;
static BOOL writeDone = NO;
static CGPoint lastPosted;

static void say(NSString *format, ...) NS_FORMAT_FUNCTION(1, 2);

static void say(NSString *format, ...) {
	@autoreleasepool {
		va_list arguments;
		va_start(arguments, format);
		NSString *line = [[NSString alloc] initWithFormat:format arguments:arguments];
		va_end(arguments);
		lanhopProbeSay((char *)[line UTF8String]);
		[line release];
	}
}

static char *copyText(NSString *text) {
	const char *bytes = [text UTF8String];
	return bytes != NULL ? strdup(bytes) : NULL;
}

char *lanhopProbeIdentity(void) {
	@autoreleasepool {
		NSString *identifier = [[NSBundle mainBundle] bundleIdentifier];
		NSString *text = [NSString stringWithFormat:@"bundle identifier %@, macOS %@", identifier != nil ? identifier : @"none (not started from the .app)",
			[[NSProcessInfo processInfo] operatingSystemVersionString]];
		return copyText(text);
	}
}

static NSPasteboard *dragPasteboard(void) {
	return [NSPasteboard pasteboardWithName:NSPasteboardNameDrag];
}

int64_t lanhopProbeDragChangeCount(void) {
	@autoreleasepool {
		return (int64_t)[dragPasteboard() changeCount];
	}
}

char *lanhopProbeDragDescription(void) {
	@autoreleasepool {
		NSPasteboard *board = dragPasteboard();
		NSMutableString *text = [NSMutableString string];
		NSArray<NSPasteboardType> *types = [board types];
		[text appendFormat:@"types (%lu): %@", (unsigned long)[types count], [types count] > 0 ? [types componentsJoinedByString:@", "] : @"none"];
		NSArray<NSPasteboardItem *> *items = [board pasteboardItems];
		[text appendFormat:@"\nitems: %lu", (unsigned long)[items count]];
		NSUInteger shown = 0;
		for (NSPasteboardItem *item in items) {
			if (shown++ == 5) {
				[text appendString:@"\n  ..."];
				break;
			}
			[text appendFormat:@"\n  item %lu: %@", (unsigned long)shown, [[item types] componentsJoinedByString:@", "]];
		}
		NSArray *files = [board readObjectsForClasses:@[ [NSURL class] ] options:@{NSPasteboardURLReadingFileURLsOnlyKey : @YES}];
		[text appendFormat:@"\nfile URLs: %lu", (unsigned long)[files count]];
		for (NSURL *file in files) {
			NSURL *path = [file filePathURL];
			[text appendFormat:@"\n  %@", path != nil ? [path path] : [file absoluteString]];
		}
		return copyText(text);
	}
}

bool lanhopProbeLeftButtonHeld(bool includePosted) {
	CGEventSourceStateID state = includePosted ? kCGEventSourceStateCombinedSessionState : kCGEventSourceStateHIDSystemState;
	return CGEventSourceButtonState(state, kCGMouseButtonLeft);
}

void lanhopProbeCursor(double *x, double *y) {
	CGEventRef now = CGEventCreate(NULL);
	CGPoint at = CGPointZero;
	if (now != NULL) {
		at = CGEventGetLocation(now);
		CFRelease(now);
	}
	*x = at.x;
	*y = at.y;
}

bool lanhopProbeCanPost(void) {
	return CGPreflightPostEventAccess();
}

bool lanhopProbeIsTrusted(void) {
	return AXIsProcessTrusted();
}

void lanhopProbeAskToPost(void) {
	CGRequestPostEventAccess();
}

static CGEventSourceRef eventSource(void) {
	static CGEventSourceRef source;
	static dispatch_once_t once;
	dispatch_once(&once, ^{
		source = CGEventSourceCreate(kCGEventSourceStateHIDSystemState);
	});
	return source;
}

static void postAndRelease(CGEventRef event) {
	if (event == NULL) {
		return;
	}
	CGEventPost(kCGHIDEventTap, event);
	CFRelease(event);
}

void lanhopProbePostEscape(void) {
	postAndRelease(CGEventCreateKeyboardEvent(eventSource(), escapeKeyCode, true));
	postAndRelease(CGEventCreateKeyboardEvent(eventSource(), escapeKeyCode, false));
}

void lanhopProbePostLeftMouse(int phase, double x, double y) {
	CGEventType type = kCGEventLeftMouseUp;
	if (phase == LANHOP_PROBE_DOWN) {
		type = kCGEventLeftMouseDown;
	} else if (phase == LANHOP_PROBE_DRAGGED) {
		type = kCGEventLeftMouseDragged;
	}
	CGPoint at = CGPointMake(x, y);
	CGEventRef event = CGEventCreateMouseEvent(eventSource(), type, at, kCGMouseButtonLeft);
	if (event == NULL) {
		return;
	}
	CGEventSetIntegerValueField(event, kCGMouseEventClickState, 1);
	if (phase == LANHOP_PROBE_DRAGGED) {
		CGEventSetIntegerValueField(event, kCGMouseEventDeltaX, (int64_t)(at.x - lastPosted.x));
		CGEventSetIntegerValueField(event, kCGMouseEventDeltaY, (int64_t)(at.y - lastPosted.y));
	}
	lastPosted = at;
	postAndRelease(event);
}

int lanhopProbeDisplays(double *rects, int capacity) {
	CGDirectDisplayID displays[32];
	uint32_t count = 0;
	if (CGGetActiveDisplayList(32, displays, &count) != kCGErrorSuccess) {
		return 0;
	}
	int written = 0;
	for (uint32_t i = 0; i < count && written < capacity; i++, written++) {
		CGRect bounds = CGDisplayBounds(displays[i]);
		rects[written * 4] = bounds.origin.x;
		rects[written * 4 + 1] = bounds.origin.y;
		rects[written * 4 + 2] = bounds.size.width;
		rects[written * 4 + 3] = bounds.size.height;
	}
	return written;
}

static NSString *operationName(NSDragOperation operation) {
	if (operation == NSDragOperationNone) {
		return @"none";
	}
	NSMutableArray<NSString *> *names = [NSMutableArray array];
	if (operation & NSDragOperationCopy) {
		[names addObject:@"copy"];
	}
	if (operation & NSDragOperationLink) {
		[names addObject:@"link"];
	}
	if (operation & NSDragOperationGeneric) {
		[names addObject:@"generic"];
	}
	if (operation & NSDragOperationPrivate) {
		[names addObject:@"private"];
	}
	if (operation & NSDragOperationMove) {
		[names addObject:@"move"];
	}
	if (operation & NSDragOperationDelete) {
		[names addObject:@"delete"];
	}
	return [names componentsJoinedByString:@"+"];
}

static void stopApplication(void) {
	[NSApp stop:nil];
	NSEvent *wake = [NSEvent otherEventWithType:NSEventTypeApplicationDefined location:NSZeroPoint modifierFlags:0 timestamp:0 windowNumber:0 context:nil subtype:0 data1:0 data2:0];
	[NSApp postEvent:wake atStart:YES];
}

static void stopWhenTheWriteSettles(int tenths) {
	if (writeDone) {
		say(@"the promised file was written; stopping");
		stopApplication();
		return;
	}
	if (!writeAsked && tenths >= 30) {
		say(@"the destination did not ask for the promised file within 3 s; stopping");
		stopApplication();
		return;
	}
	if (tenths >= 150) {
		say(@"the promised file was not written within 15 s; stopping");
		stopApplication();
		return;
	}
	dispatch_after(dispatch_time(DISPATCH_TIME_NOW, 100 * NSEC_PER_MSEC), dispatch_get_main_queue(), ^{
		stopWhenTheWriteSettles(tenths + 1);
	});
}

@interface LanhopProbeSquare : NSView <NSDraggingSource, NSFilePromiseProviderDelegate> {
	NSString *sourcePath;
	NSOperationQueue *writeQueue;
	dispatch_semaphore_t began;
	BOOL started;
	NSUInteger moves;
}
- (instancetype)initWithFrame:(NSRect)frame path:(NSString *)path began:(dispatch_semaphore_t)semaphore;
@end

@implementation LanhopProbeSquare

- (instancetype)initWithFrame:(NSRect)frame path:(NSString *)path began:(dispatch_semaphore_t)semaphore {
	self = [super initWithFrame:frame];
	if (self != nil) {
		sourcePath = [path copy];
		writeQueue = [[NSOperationQueue alloc] init];
		began = semaphore;
		dispatch_retain(began);
	}
	return self;
}

- (void)dealloc {
	[writeQueue waitUntilAllOperationsAreFinished];
	[writeQueue release];
	[sourcePath release];
	dispatch_release(began);
	[super dealloc];
}

- (BOOL)acceptsFirstMouse:(NSEvent *)event {
	return YES;
}

- (void)drawRect:(NSRect)dirtyRect {
	[[NSColor systemBlueColor] setFill];
	NSRectFill([self bounds]);
}

- (void)mouseDown:(NSEvent *)event {
	NSPoint at = [NSEvent mouseLocation];
	say(@"the square got a mouse down at %.0f,%.0f (screen points, origin at the bottom left)", at.x, at.y);
}

- (void)mouseDragged:(NSEvent *)event {
	if (started) {
		return;
	}
	started = YES;
	NSString *fileType = @"public.data";
	if (@available(macOS 11.0, *)) {
		UTType *type = [UTType typeWithFilenameExtension:[sourcePath pathExtension]];
		if (type != nil) {
			fileType = [type identifier];
		}
	}
	NSFilePromiseProvider *provider = [[NSFilePromiseProvider alloc] initWithFileType:fileType delegate:self];
	NSDraggingItem *item = [[NSDraggingItem alloc] initWithPasteboardWriter:provider];
	NSPoint at = [self convertPoint:[event locationInWindow] fromView:nil];
	[item setDraggingFrame:NSMakeRect(at.x - 32, at.y - 32, 64, 64) contents:[[NSWorkspace sharedWorkspace] iconForFile:sourcePath]];
	NSDraggingSession *session = [self beginDraggingSessionWithItems:@[ item ] event:event source:self];
	[session setAnimatesToStartingPositionsOnCancelOrFail:YES];
	say(@"asked AppKit for a drag session with a file promise of type %@ named %@", fileType, [sourcePath lastPathComponent]);
	[item release];
	[provider release];
}

- (NSDragOperation)draggingSession:(NSDraggingSession *)session sourceOperationMaskForDraggingContext:(NSDraggingContext)context {
	return NSDragOperationCopy;
}

- (void)draggingSession:(NSDraggingSession *)session willBeginAtPoint:(NSPoint)screenPoint {
	say(@"the drag session began at %.0f,%.0f", screenPoint.x, screenPoint.y);
	dispatch_semaphore_signal(began);
}

- (void)draggingSession:(NSDraggingSession *)session movedToPoint:(NSPoint)screenPoint {
	moves++;
	if (moves == 1 || moves % 10 == 0) {
		say(@"the drag session moved to %.0f,%.0f (%lu moves)", screenPoint.x, screenPoint.y, (unsigned long)moves);
	}
}

- (void)draggingSession:(NSDraggingSession *)session endedAtPoint:(NSPoint)screenPoint operation:(NSDragOperation)operation {
	say(@"the drag session ended at %.0f,%.0f after %lu moves, operation %@", screenPoint.x, screenPoint.y, (unsigned long)moves, operationName(operation));
	sessionEnded = YES;
	endOperation = (NSInteger)operation;
	if (operation == NSDragOperationNone) {
		dispatch_after(dispatch_time(DISPATCH_TIME_NOW, 300 * NSEC_PER_MSEC), dispatch_get_main_queue(), ^{
			stopApplication();
		});
		return;
	}
	stopWhenTheWriteSettles(0);
}

- (NSString *)filePromiseProvider:(NSFilePromiseProvider *)provider fileNameForType:(NSString *)fileType {
	say(@"the destination asked for the name of the promised %@", fileType);
	return [sourcePath lastPathComponent];
}

- (NSOperationQueue *)operationQueueForFilePromiseProvider:(NSFilePromiseProvider *)provider {
	return writeQueue;
}

- (void)filePromiseProvider:(NSFilePromiseProvider *)provider writePromiseToURL:(NSURL *)url completionHandler:(void (^)(NSError *errorOrNil))completionHandler {
	dispatch_async(dispatch_get_main_queue(), ^{
		writeAsked = YES;
	});
	say(@"the destination asked for the file at %@", [url path]);
	NSError *error = nil;
	BOOL copied = [[NSFileManager defaultManager] copyItemAtURL:[NSURL fileURLWithPath:sourcePath] toURL:url error:&error];
	if (copied) {
		say(@"wrote a copy of %@ to %@", [sourcePath lastPathComponent], [url path]);
	} else {
		say(@"could not write %@: %@", [url path], [error localizedDescription]);
	}
	completionHandler(copied ? nil : error);
	dispatch_async(dispatch_get_main_queue(), ^{
		writeDone = YES;
	});
}

@end

static void drive(dispatch_group_t group, dispatch_semaphore_t began, CGPoint start, CGPoint target, int steps, double holdSeconds) {
	dispatch_group_async(group, dispatch_get_global_queue(QOS_CLASS_USER_INITIATED, 0), ^{
		usleep(700000);
		say(@"posting a left mouse down at %.0f,%.0f (display points, origin at the top left)", start.x, start.y);
		lanhopProbePostLeftMouse(LANHOP_PROBE_DOWN, start.x, start.y);
		usleep(100000);
		CGPoint at = start;
		for (int i = 1; i <= 6; i++) {
			at = CGPointMake(start.x + 3 * i, start.y);
			lanhopProbePostLeftMouse(LANHOP_PROBE_DRAGGED, at.x, at.y);
			usleep(20000);
		}
		say(@"posted 6 mouse dragged events of 3 points");
		if (dispatch_semaphore_wait(began, dispatch_time(DISPATCH_TIME_NOW, 2 * NSEC_PER_SEC)) == 0) {
			say(@"moving to %.0f,%.0f in %d posted mouse dragged events", target.x, target.y, steps);
		} else {
			say(@"no drag session began within 2 s of the posted events; moving to %.0f,%.0f anyway", target.x, target.y);
		}
		CGPoint from = at;
		for (int i = 1; i <= steps; i++) {
			double share = (double)i / steps;
			lanhopProbePostLeftMouse(LANHOP_PROBE_DRAGGED, from.x + (target.x - from.x) * share, from.y + (target.y - from.y) * share);
			usleep(20000);
		}
		usleep((useconds_t)(holdSeconds * 1000000));
		say(@"posting a left mouse up at %.0f,%.0f", target.x, target.y);
		lanhopProbePostLeftMouse(LANHOP_PROBE_UP, target.x, target.y);
	});
}

int lanhopProbePromise(const char *path, double toX, double toY, bool manual, int steps, double holdSeconds, double timeoutSeconds) {
	@autoreleasepool {
		NSString *file = [NSString stringWithUTF8String:path];
		if (file == nil || ![[NSFileManager defaultManager] isReadableFileAtPath:file]) {
			say(@"cannot read %s", path);
			return LANHOP_PROBE_NO_FILE;
		}
		endOperation = LANHOP_PROBE_NO_SESSION;
		sessionEnded = NO;
		writeAsked = NO;
		writeDone = NO;
		dispatch_semaphore_t began = dispatch_semaphore_create(0);
		dispatch_group_t driving = dispatch_group_create();

		[NSApplication sharedApplication];
		[NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];

		NSPoint mouse = [NSEvent mouseLocation];
		NSRect frame = NSMakeRect(mouse.x - squareSide / 2, mouse.y - squareSide / 2, squareSide, squareSide);
		NSWindow *window = [[NSWindow alloc] initWithContentRect:frame styleMask:NSWindowStyleMaskBorderless backing:NSBackingStoreBuffered defer:NO];
		[window setReleasedWhenClosed:NO];
		[window setLevel:NSStatusWindowLevel];
		LanhopProbeSquare *square = [[LanhopProbeSquare alloc] initWithFrame:NSMakeRect(0, 0, squareSide, squareSide) path:file began:began];
		[window setContentView:square];
		[window orderFrontRegardless];
		if (@available(macOS 14.0, *)) {
			[NSApp activate];
		} else {
			[NSApp activateIgnoringOtherApps:YES];
		}
		say(@"a blue square of %.0f points is under the cursor at %.0f,%.0f (screen points, origin at the bottom left)", squareSide, mouse.x, mouse.y);

		if (manual) {
			say(@"-manual: drag from the blue square by hand within %.0f s and drop on a Finder window or the desktop", timeoutSeconds);
		} else {
			double x = 0;
			double y = 0;
			lanhopProbeCursor(&x, &y);
			drive(driving, began, CGPointMake(x, y), CGPointMake(toX, toY), steps, holdSeconds);
		}
		dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(timeoutSeconds * NSEC_PER_SEC)), dispatch_get_main_queue(), ^{
			if (!sessionEnded) {
				say(@"no drag session ended within %.0f s; stopping", timeoutSeconds);
				stopApplication();
			}
		});

		[NSApp run];

		if (dispatch_group_wait(driving, dispatch_time(DISPATCH_TIME_NOW, 15 * NSEC_PER_SEC)) != 0) {
			say(@"the posted events were still running 15 s after the end");
		}
		[window orderOut:nil];
		[window close];
		[window release];
		[square release];
		dispatch_release(driving);
		dispatch_release(began);
		return (int)endOperation;
	}
}
