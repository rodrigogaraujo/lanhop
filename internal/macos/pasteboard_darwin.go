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
	"fmt"
	"path/filepath"
	"strings"
	"unsafe"

	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
)

var (
	ErrPasteboardRefused      = errors.New("macos: the pasteboard did not take the text")
	ErrPasteboardRefusedFiles = errors.New("macos: the pasteboard did not take the files")
	ErrNotAbsolute            = errors.New("macos: a path to put on the pasteboard is not absolute")
	ErrOutOfMemory            = errors.New("macos: out of memory while reading the file copy")
)

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
	case C.LANHOP_PASTEBOARD_EMPTY:
		return "", clipboard.ErrEmpty
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

func (p Pasteboard) ReadFiles() ([]string, error) {
	name := p.cName()
	defer C.free(unsafe.Pointer(name))
	var (
		joined *C.char
		length C.int64_t
		count  C.int64_t
	)
	switch C.lanhop_pasteboard_read_files(name, C.int64_t(clipboard.FilesAndFoldersLimit), &joined, &length, &count) {
	case C.LANHOP_PASTEBOARD_PRIVATE:
		return nil, clipboard.ErrPrivate
	case C.LANHOP_PASTEBOARD_NOT_FILES:
		return nil, clipboard.ErrNotFiles
	case C.LANHOP_PASTEBOARD_TOO_MANY:
		return nil, fmt.Errorf("%w: %d", clipboard.ErrTooManyFiles, int64(count))
	case C.LANHOP_PASTEBOARD_NO_MEMORY:
		return nil, ErrOutOfMemory
	}
	defer C.free(unsafe.Pointer(joined))
	paths := splitTerminated(C.GoStringN(joined, C.int(length)))
	if len(paths) == 0 {
		return nil, clipboard.ErrNotFiles
	}
	for _, path := range paths {
		if !filepath.IsAbs(path) {
			return nil, clipboard.ErrNotFiles
		}
	}
	return paths, nil
}

func (p Pasteboard) WriteFiles(paths []string) (mark int64, err error) {
	return p.writeFiles(paths, false)
}

func (p Pasteboard) writeFiles(paths []string, markPrivate bool) (int64, error) {
	if len(paths) == 0 {
		return 0, clipboard.ErrEmpty
	}
	var joined strings.Builder
	for _, path := range paths {
		if !filepath.IsAbs(path) {
			return 0, fmt.Errorf("%w: %q", ErrNotAbsolute, path)
		}
		if strings.IndexByte(path, 0) >= 0 {
			return 0, fmt.Errorf("%w: %q holds a NUL byte", ErrPasteboardRefusedFiles, path)
		}
		joined.WriteString(path)
		joined.WriteByte(0)
	}
	name := p.cName()
	defer C.free(unsafe.Pointer(name))
	bytes := C.CString(joined.String())
	defer C.free(unsafe.Pointer(bytes))
	private := C.int(0)
	if markPrivate {
		private = 1
	}
	mark := int64(C.lanhop_pasteboard_write_files(name, bytes, C.int64_t(joined.Len()), private))
	if mark == C.LANHOP_PASTEBOARD_REFUSED {
		return 0, ErrPasteboardRefusedFiles
	}
	return mark, nil
}

func splitTerminated(joined string) []string {
	if joined == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(joined, "\x00"), "\x00")
}

func (p Pasteboard) cName() *C.char {
	if p.name == "" {
		return nil
	}
	return C.CString(p.name)
}

func (p Pasteboard) writeTyped(kind, data string, text *string) error {
	name := p.cName()
	defer C.free(unsafe.Pointer(name))
	cKind := C.CString(kind)
	defer C.free(unsafe.Pointer(cKind))
	cData := C.CString(data)
	defer C.free(unsafe.Pointer(cData))
	var plain *C.char
	var plainLength int
	if text != nil {
		plain = C.CString(*text)
		defer C.free(unsafe.Pointer(plain))
		plainLength = len(*text)
	}
	if C.lanhop_pasteboard_write_typed(name, cKind, cData, C.int64_t(len(data)), plain, C.int64_t(plainLength)) == C.LANHOP_PASTEBOARD_REFUSED {
		return ErrPasteboardRefused
	}
	return nil
}

func (p Pasteboard) types() []string {
	name := p.cName()
	defer C.free(unsafe.Pointer(name))
	var (
		joined *C.char
		length C.int64_t
	)
	if C.lanhop_pasteboard_types(name, &joined, &length) == 0 {
		return nil
	}
	defer C.free(unsafe.Pointer(joined))
	return splitTerminated(C.GoStringN(joined, C.int(length)))
}

func fileReferenceURL(path string) (string, bool) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))
	reference := C.lanhop_file_reference_url(cPath)
	if reference == nil {
		return "", false
	}
	defer C.free(unsafe.Pointer(reference))
	return C.GoString(reference), true
}

func (p Pasteboard) empty() {
	name := p.cName()
	defer C.free(unsafe.Pointer(name))
	C.lanhop_pasteboard_empty(name)
}

func (p Pasteboard) release() {
	if p.name == "" {
		return
	}
	name := p.cName()
	defer C.free(unsafe.Pointer(name))
	C.lanhop_pasteboard_release(name)
}
