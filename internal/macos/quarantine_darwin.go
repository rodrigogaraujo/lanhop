//go:build darwin && cgo

package macos

/*
#include <stdlib.h>
#include <sys/xattr.h>
*/
import "C"

import (
	"fmt"
	"unsafe"
)

const quarantineAttribute = "com.apple.quarantine"

func SetQuarantine(path, value string) error {
	cPath, cName, cValue := C.CString(path), C.CString(quarantineAttribute), C.CString(value)
	defer C.free(unsafe.Pointer(cPath))
	defer C.free(unsafe.Pointer(cName))
	defer C.free(unsafe.Pointer(cValue))
	if result, err := C.setxattr(cPath, cName, unsafe.Pointer(cValue), C.size_t(len(value)), 0, C.XATTR_NOFOLLOW); result != 0 {
		return fmt.Errorf("macos: setting the quarantine mark on %s: %w", path, err)
	}
	return nil
}

func SetQuarantineOn(descriptor uintptr, value string) error {
	cName, cValue := C.CString(quarantineAttribute), C.CString(value)
	defer C.free(unsafe.Pointer(cName))
	defer C.free(unsafe.Pointer(cValue))
	if result, err := C.fsetxattr(C.int(descriptor), cName, unsafe.Pointer(cValue), C.size_t(len(value)), 0, 0); result != 0 {
		return fmt.Errorf("macos: setting the quarantine mark on descriptor %d: %w", descriptor, err)
	}
	return nil
}

func Quarantine(path string) (string, error) {
	cPath, cName := C.CString(path), C.CString(quarantineAttribute)
	defer C.free(unsafe.Pointer(cPath))
	defer C.free(unsafe.Pointer(cName))
	size, err := C.getxattr(cPath, cName, nil, 0, 0, C.XATTR_NOFOLLOW)
	if size < 0 {
		return "", fmt.Errorf("macos: reading the quarantine mark of %s: %w", path, err)
	}
	if size == 0 {
		return "", nil
	}
	value := C.malloc(C.size_t(size))
	defer C.free(value)
	read, err := C.getxattr(cPath, cName, value, C.size_t(size), 0, C.XATTR_NOFOLLOW)
	if read < 0 {
		return "", fmt.Errorf("macos: reading the quarantine mark of %s: %w", path, err)
	}
	return C.GoStringN((*C.char)(value), C.int(read)), nil
}
