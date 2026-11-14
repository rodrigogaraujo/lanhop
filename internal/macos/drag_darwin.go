//go:build darwin && cgo

package macos

/*
#cgo LDFLAGS: -framework AppKit
#include <stdlib.h>
#include "drag_darwin.h"
*/
import "C"

import (
	"errors"
	"time"
	"unsafe"
)

var ErrNoDragPasteboard = errors.New("macos: AppKit gave no name for the drag pasteboard")

var (
	_ dragEnder         = Quartz{}
	_ replayedDragEnder = Quartz{}
)

func WatchDrags(interval time.Duration) (*DragWatch, error) {
	board, err := systemDragPasteboard()
	if err != nil {
		return nil, err
	}
	return watchDrags(dragSource{board: board, leftHeld: leftButtonHeld}, interval)
}

func EndSourceDrag() error {
	return endSourceDrag(Quartz{}, CanPost, time.Sleep)
}

func EndReplayedSourceDrag(under Point) error {
	return endReplayedSourceDrag(Quartz{}, CanPost, time.Sleep, under)
}

func leftButtonHeld() bool {
	return Quartz{}.ButtonDown(leftMouseButton)
}

func systemDragPasteboard() (Pasteboard, error) {
	name := C.lanhop_drag_pasteboard_name()
	if name == nil {
		return Pasteboard{}, ErrNoDragPasteboard
	}
	defer C.free(unsafe.Pointer(name))
	board := Pasteboard{name: C.GoString(name)}
	if board.name == "" {
		return Pasteboard{}, ErrNoDragPasteboard
	}
	return board, nil
}
