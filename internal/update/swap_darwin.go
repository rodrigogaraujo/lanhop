//go:build darwin && cgo

package update

/*
#include <errno.h>
#include <stdio.h>
#include <stdlib.h>

static int swapPaths(const char *replacement, const char *current) {
	return renamex_np(replacement, current, RENAME_SWAP) == 0 ? 0 : errno;
}
*/
import "C"

import (
	"fmt"
	"syscall"
	"unsafe"
)

const SwapsApps = true

func swapInPlace(replacement, current string) error {
	from, to := C.CString(replacement), C.CString(current)
	defer C.free(unsafe.Pointer(from))
	defer C.free(unsafe.Pointer(to))
	if code := C.swapPaths(from, to); code != 0 {
		if syscall.Errno(code) == syscall.ENOTSUP {
			return fmt.Errorf("%w: %w", ErrSwapUnknown, syscall.Errno(code))
		}
		return syscall.Errno(code)
	}
	return nil
}
