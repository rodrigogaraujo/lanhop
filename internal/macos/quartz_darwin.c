#include "quartz_darwin.h"

#include <ApplicationServices/ApplicationServices.h>
#include <dispatch/dispatch.h>
#include <stdlib.h>

#include "_cgo_export.h"

enum { actionMoved, actionDragged, actionDown, actionUp };

struct lanhopTap {
	CFMachPortRef port;
	CFRunLoopSourceRef source;
	CFRunLoopRef loop;
	uintptr_t handle;
	bool stopping;
};

static CGEventSourceRef eventSource(void) {
	static CGEventSourceRef source;
	static dispatch_once_t once;
	dispatch_once(&once, ^{
		source = CGEventSourceCreate(kCGEventSourceStateHIDSystemState);
	});
	return source;
}

static void post(CGEventRef event, uint64_t flags) {
	if (event == NULL) {
		return;
	}
	CGEventSetFlags(event, (CGEventFlags)flags);
	CGEventSetIntegerValueField(event, kCGEventSourceUserData, LANHOP_MARKER);
	CGEventPost(kCGHIDEventTap, event);
	CFRelease(event);
}

static CGEventType mouseType(int action, int button) {
	switch (action) {
	case actionMoved:
		return kCGEventMouseMoved;
	case actionDragged:
		return button == 0 ? kCGEventLeftMouseDragged : button == 1 ? kCGEventRightMouseDragged : kCGEventOtherMouseDragged;
	case actionDown:
		return button == 0 ? kCGEventLeftMouseDown : button == 1 ? kCGEventRightMouseDown : kCGEventOtherMouseDown;
	default:
		return button == 0 ? kCGEventLeftMouseUp : button == 1 ? kCGEventRightMouseUp : kCGEventOtherMouseUp;
	}
}

bool lanhopCanPost(void) {
	return CGPreflightPostEventAccess();
}

bool lanhopCanListen(void) {
	return CGPreflightListenEventAccess();
}

void lanhopPostKey(uint16_t keyCode, bool down, bool modifier, uint64_t flags) {
	CGEventRef event = CGEventCreateKeyboardEvent(eventSource(), keyCode, down);
	if (event != NULL && modifier) {
		CGEventSetType(event, kCGEventFlagsChanged);
	}
	post(event, flags);
}

void lanhopPostMouse(int action, double x, double y, int button, int64_t clicks, int64_t deltaX, int64_t deltaY, uint64_t flags) {
	CGEventRef event = CGEventCreateMouseEvent(eventSource(), mouseType(action, button), CGPointMake(x, y), (CGMouseButton)button);
	if (event == NULL) {
		return;
	}
	if (action == actionDown || action == actionUp) {
		CGEventSetIntegerValueField(event, kCGMouseEventClickState, clicks);
	} else {
		CGEventSetIntegerValueField(event, kCGMouseEventDeltaX, deltaX);
		CGEventSetIntegerValueField(event, kCGMouseEventDeltaY, deltaY);
	}
	post(event, flags);
}

void lanhopPostScroll(int32_t vertical, int32_t horizontal, uint64_t flags) {
	post(CGEventCreateScrollWheelEvent2(eventSource(), kCGScrollEventUnitLine, 2, vertical, horizontal, 0), flags);
}

void lanhopCursor(double *x, double *y) {
	CGEventRef now = CGEventCreate(NULL);
	if (now == NULL) {
		*x = 0;
		*y = 0;
		return;
	}
	CGPoint location = CGEventGetLocation(now);
	CFRelease(now);
	*x = location.x;
	*y = location.y;
}

int lanhopDisplays(double *rects, int capacity) {
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

static CGEventRef observe(CGEventTapProxy proxy, CGEventType type, CGEventRef event, void *info) {
	lanhopTap *tap = info;
	if (type == kCGEventTapDisabledByTimeout || type == kCGEventTapDisabledByUserInput) {
		if (!tap->stopping) {
			CGEventTapEnable(tap->port, true);
		}
		return event;
	}
	CGPoint location = CGEventGetLocation(event);
	lanhopObservation seen = {
		.type = (uint32_t)type,
		.keyCode = (uint16_t)CGEventGetIntegerValueField(event, kCGKeyboardEventKeycode),
		.flags = (uint64_t)CGEventGetFlags(event),
		.x = location.x,
		.y = location.y,
		.deltaX = CGEventGetIntegerValueField(event, kCGMouseEventDeltaX),
		.deltaY = CGEventGetIntegerValueField(event, kCGMouseEventDeltaY),
		.clicks = CGEventGetIntegerValueField(event, kCGMouseEventClickState),
		.button = CGEventGetIntegerValueField(event, kCGMouseEventButtonNumber),
		.scrollVertical = CGEventGetIntegerValueField(event, kCGScrollWheelEventDeltaAxis1),
		.scrollHorizontal = CGEventGetIntegerValueField(event, kCGScrollWheelEventDeltaAxis2),
		.marker = CGEventGetIntegerValueField(event, kCGEventSourceUserData),
	};
	lanhopObserved(tap->handle, &seen);
	return event;
}

lanhopTap *lanhopTapCreate(uintptr_t handle) {
	lanhopTap *tap = calloc(1, sizeof(lanhopTap));
	if (tap == NULL) {
		return NULL;
	}
	tap->handle = handle;
	CGEventMask mask = CGEventMaskBit(kCGEventKeyDown) | CGEventMaskBit(kCGEventKeyUp) | CGEventMaskBit(kCGEventFlagsChanged) |
		CGEventMaskBit(kCGEventMouseMoved) | CGEventMaskBit(kCGEventScrollWheel) |
		CGEventMaskBit(kCGEventLeftMouseDown) | CGEventMaskBit(kCGEventLeftMouseUp) | CGEventMaskBit(kCGEventLeftMouseDragged) |
		CGEventMaskBit(kCGEventRightMouseDown) | CGEventMaskBit(kCGEventRightMouseUp) | CGEventMaskBit(kCGEventRightMouseDragged) |
		CGEventMaskBit(kCGEventOtherMouseDown) | CGEventMaskBit(kCGEventOtherMouseUp) | CGEventMaskBit(kCGEventOtherMouseDragged);
	tap->port = CGEventTapCreate(kCGSessionEventTap, kCGHeadInsertEventTap, kCGEventTapOptionListenOnly, mask, observe, tap);
	if (tap->port == NULL) {
		free(tap);
		return NULL;
	}
	tap->source = CFMachPortCreateRunLoopSource(NULL, tap->port, 0);
	return tap;
}

void lanhopTapRun(lanhopTap *tap) {
	tap->loop = CFRunLoopGetCurrent();
	CFRetain(tap->loop);
	CFRunLoopAddSource(tap->loop, tap->source, kCFRunLoopCommonModes);
	CGEventTapEnable(tap->port, true);
	lanhopTapReady(tap->handle);
	CFRunLoopRun();
}

void lanhopTapStop(lanhopTap *tap) {
	tap->stopping = true;
	CGEventTapEnable(tap->port, false);
	CFRunLoopRemoveSource(tap->loop, tap->source, kCFRunLoopCommonModes);
	CFMachPortInvalidate(tap->port);
	CFRunLoopStop(tap->loop);
}

void lanhopTapFree(lanhopTap *tap) {
	CFRelease(tap->source);
	CFRelease(tap->port);
	if (tap->loop != NULL) {
		CFRelease(tap->loop);
	}
	free(tap);
}
