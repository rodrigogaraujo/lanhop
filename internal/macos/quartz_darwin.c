#include "quartz_darwin.h"

#include <ApplicationServices/ApplicationServices.h>
#include <IOKit/hid/IOHIDProperties.h>
#include <IOKit/hidsystem/IOHIDEventSystemClient.h>
#include <dispatch/dispatch.h>
#include <stdlib.h>

#include "_cgo_export.h"

_Static_assert(kCGEventFlagMaskShift == 0x20000, "the Shift flag changed");
_Static_assert(kCGEventFlagMaskControl == 0x40000, "the Control flag changed");
_Static_assert(kCGEventFlagMaskAlternate == 0x80000, "the Option flag changed");
_Static_assert(kCGEventFlagMaskCommand == 0x100000, "the Command flag changed");

struct lanhopTap {
	CFMachPortRef port;
	CFRunLoopSourceRef source;
	CFRunLoopSourceRef keepsLoopAlive;
	CFRunLoopRef loop;
	uintptr_t handle;
	bool stopping;
};

static void doNothing(void *info) {
}

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
	case lanhopActionMoved:
		return kCGEventMouseMoved;
	case lanhopActionDragged:
		return button == 0 ? kCGEventLeftMouseDragged : button == 1 ? kCGEventRightMouseDragged : kCGEventOtherMouseDragged;
	case lanhopActionDown:
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

void lanhopPostKey(uint16_t keyCode, bool down, bool modifier, bool autorepeat, uint64_t flags) {
	CGEventRef event = CGEventCreateKeyboardEvent(eventSource(), keyCode, down);
	if (event != NULL && modifier) {
		CGEventSetType(event, kCGEventFlagsChanged);
	}
	if (event != NULL && autorepeat) {
		CGEventSetIntegerValueField(event, kCGKeyboardEventAutorepeat, 1);
	}
	post(event, flags);
}

void lanhopPostMouse(int action, double x, double y, int button, int64_t clicks, int64_t deltaX, int64_t deltaY, uint64_t flags) {
	CGEventRef event = CGEventCreateMouseEvent(eventSource(), mouseType(action, button), CGPointMake(x, y), (CGMouseButton)button);
	if (event == NULL) {
		return;
	}
	if (action != lanhopActionMoved) {
		CGEventSetIntegerValueField(event, kCGMouseEventClickState, clicks);
	}
	if (action == lanhopActionMoved || action == lanhopActionDragged) {
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

static IOHIDEventSystemClientRef inputSettings(void) {
	static IOHIDEventSystemClientRef client;
	static dispatch_once_t once;
	dispatch_once(&once, ^{
		client = IOHIDEventSystemClientCreateSimpleClient(kCFAllocatorDefault);
	});
	return client;
}

static bool inputSetting(CFStringRef name, int64_t *value) {
	IOHIDEventSystemClientRef settings = inputSettings();
	if (settings == NULL) {
		return false;
	}
	CFTypeRef setting = IOHIDEventSystemClientCopyProperty(settings, name);
	if (setting == NULL) {
		return false;
	}
	bool isNumber = CFGetTypeID(setting) == CFNumberGetTypeID() && CFNumberGetValue(setting, kCFNumberSInt64Type, value);
	CFRelease(setting);
	return isNumber;
}

bool lanhopKeyRepeat(int64_t *delayNanoseconds, int64_t *intervalNanoseconds) {
	return inputSetting(CFSTR(kIOHIDServiceInitialKeyRepeatDelayKey), delayNanoseconds) &&
		inputSetting(CFSTR(kIOHIDServiceKeyRepeatDelayKey), intervalNanoseconds);
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
		.autorepeat = CGEventGetIntegerValueField(event, kCGKeyboardEventAutorepeat) != 0,
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
	CFRunLoopSourceContext idle = {.perform = doNothing};
	tap->keepsLoopAlive = CFRunLoopSourceCreate(NULL, 0, &idle);
	if (tap->source == NULL || tap->keepsLoopAlive == NULL) {
		lanhopTapFree(tap);
		return NULL;
	}
	return tap;
}

void lanhopTapRun(lanhopTap *tap) {
	tap->loop = CFRunLoopGetCurrent();
	CFRetain(tap->loop);
	CFRunLoopAddSource(tap->loop, tap->keepsLoopAlive, kCFRunLoopCommonModes);
	CFRunLoopAddSource(tap->loop, tap->source, kCFRunLoopCommonModes);
	CGEventTapEnable(tap->port, true);
	lanhopTapReady(tap->handle);
	CFRunLoopRun();
}

void lanhopTapStop(lanhopTap *tap) {
	CFRunLoopPerformBlock(tap->loop, kCFRunLoopCommonModes, ^{
		tap->stopping = true;
		CGEventTapEnable(tap->port, false);
		CFRunLoopRemoveSource(tap->loop, tap->source, kCFRunLoopCommonModes);
		CFMachPortInvalidate(tap->port);
		CFRunLoopSourceInvalidate(tap->keepsLoopAlive);
		CFRunLoopStop(tap->loop);
	});
	CFRunLoopWakeUp(tap->loop);
}

void lanhopTapFree(lanhopTap *tap) {
	if (tap->source != NULL) {
		CFRelease(tap->source);
	}
	if (tap->keepsLoopAlive != NULL) {
		CFRelease(tap->keepsLoopAlive);
	}
	CFRelease(tap->port);
	if (tap->loop != NULL) {
		CFRelease(tap->loop);
	}
	free(tap);
}
