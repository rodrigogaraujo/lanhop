//go:build darwin && cgo

package update

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>
#include <string.h>

static OSStatus checkAppAgainst(const char *path, const char *requirementText) {
	CFURLRef url = CFURLCreateFromFileSystemRepresentation(NULL, (const UInt8 *)path, (CFIndex)strlen(path), true);
	if (url == NULL) {
		return errSecParam;
	}
	SecStaticCodeRef code = NULL;
	OSStatus status = SecStaticCodeCreateWithPath(url, kSecCSDefaultFlags, &code);
	CFRelease(url);
	if (status != errSecSuccess) {
		return status;
	}
	CFStringRef text = CFStringCreateWithCString(NULL, requirementText, kCFStringEncodingUTF8);
	if (text == NULL) {
		CFRelease(code);
		return errSecParam;
	}
	SecRequirementRef requirement = NULL;
	status = SecRequirementCreateWithString(text, kSecCSDefaultFlags, &requirement);
	CFRelease(text);
	if (status == errSecSuccess) {
		status = SecStaticCodeCheckValidity(code, kSecCSCheckAllArchitectures | kSecCSStrictValidate | kSecCSCheckNestedCode, requirement);
		CFRelease(requirement);
	}
	CFRelease(code);
	return status;
}

static void describeStatus(OSStatus status, char *into, CFIndex room) {
	into[0] = 0;
	CFStringRef message = SecCopyErrorMessageString(status, NULL);
	if (message != NULL) {
		CFStringGetCString(message, into, room, kCFStringEncodingUTF8);
		CFRelease(message);
	}
}

static const OSStatus statusRequirementFailed = errSecCSReqFailed;
static const OSStatus statusUnsigned = errSecCSUnsigned;
*/
import "C"

import (
	"fmt"
	"unsafe"
)

func checkApp(app, requirement string) error {
	path, text := C.CString(app), C.CString(requirement)
	defer C.free(unsafe.Pointer(path))
	defer C.free(unsafe.Pointer(text))
	status := C.checkAppAgainst(path, text)
	switch status {
	case C.errSecSuccess:
		return nil
	case C.statusRequirementFailed, C.statusUnsigned:
		return fmt.Errorf("%w: %s does not meet %s", ErrAppNotOurs, app, requirement)
	}
	var message [256]C.char
	C.describeStatus(status, &message[0], C.CFIndex(len(message)))
	return fmt.Errorf("update: macOS could not check the signature of %s: %s (OSStatus %d)", app, C.GoString(&message[0]), int(status))
}
