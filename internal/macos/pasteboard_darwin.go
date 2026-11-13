//go:build darwin && cgo

package macos

/*
#cgo LDFLAGS: -framework AppKit
#include <stdlib.h>
#include "pasteboard_darwin.h"
*/
import "C"

import (
	"errors"
	"unsafe"

	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
)

var ErrPasteboardRefused = errors.New("macos: the pasteboard did not take the text")

type Pasteboard struct {
	name string
}

func GeneralPasteboard() Pasteboard {
	return Pasteboard{}
}

func (p Pasteboard) Mark() int64 {
	name := p.cName()
	defer C.free(unsafe.Pointer(name))
	return int64(C.lanhop_pasteboard_mark(name))
}

func (p Pasteboard) Read() (string, error) {
	name := p.cName()
	defer C.free(unsafe.Pointer(name))
	var (
		text   *C.char
		length C.int64_t
	)
	switch C.lanhop_pasteboard_read(name, C.int64_t(clipboard.Limit), &text, &length) {
	case C.LANHOP_PASTEBOARD_PRIVATE:
		return "", clipboard.ErrPrivate
	case C.LANHOP_PASTEBOARD_NOT_TEXT:
		return "", clipboard.ErrNotText
	case C.LANHOP_PASTEBOARD_TOO_LARGE:
		return "", clipboard.ErrTooLarge
	}
	defer C.free(unsafe.Pointer(text))
	return C.GoStringN(text, C.int(length)), nil
}

func (p Pasteboard) Write(text string) (mark int64, err error) {
	return p.write(text, false)
}

func (p Pasteboard) write(text string, markPrivate bool) (int64, error) {
	name := p.cName()
	defer C.free(unsafe.Pointer(name))
	bytes := C.CString(text)
	defer C.free(unsafe.Pointer(bytes))
	private := C.int(0)
	if markPrivate {
		private = 1
	}
	mark := int64(C.lanhop_pasteboard_write(name, bytes, C.int64_t(len(text)), private))
	if mark == C.LANHOP_PASTEBOARD_REFUSED {
		return 0, ErrPasteboardRefused
	}
	return mark, nil
}

func (p Pasteboard) cName() *C.char {
	if p.name == "" {
		return nil
	}
	return C.CString(p.name)
}

func (p Pasteboard) release() {
	name := p.cName()
	defer C.free(unsafe.Pointer(name))
	C.lanhop_pasteboard_release(name)
}
