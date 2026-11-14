//go:build darwin && cgo

package macos

/*
#cgo LDFLAGS: -framework CoreFoundation
#include <stdlib.h>
#include <CoreFoundation/CoreFoundation.h>

static char *lanhopDockOrientation(void) {
	CFStringRef dock = CFSTR("com.apple.dock");
	CFPreferencesAppSynchronize(dock);
	CFPropertyListRef value = CFPreferencesCopyAppValue(CFSTR("orientation"), dock);
	if (value == NULL) {
		return NULL;
	}
	char *orientation = NULL;
	if (CFGetTypeID(value) == CFStringGetTypeID()) {
		CFStringRef text = (CFStringRef)value;
		CFIndex size = CFStringGetMaximumSizeForEncoding(CFStringGetLength(text), kCFStringEncodingUTF8) + 1;
		orientation = malloc((size_t)size);
		if (orientation != NULL && !CFStringGetCString(text, orientation, size, kCFStringEncodingUTF8)) {
			free(orientation);
			orientation = NULL;
		}
	}
	CFRelease(value);
	return orientation;
}
*/
import "C"

import "unsafe"

func DockOrientation() string {
	orientation := C.lanhopDockOrientation()
	if orientation == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(orientation))
	return C.GoString(orientation)
}
