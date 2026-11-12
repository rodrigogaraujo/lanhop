//go:build darwin

package macos

/*
#cgo LDFLAGS: -framework ApplicationServices -framework CoreFoundation
#include "quartz_darwin.h"
*/
import "C"

import (
	"errors"
	"runtime"
	"runtime/cgo"
	"sync"
)

const (
	Marker           = int64(C.LANHOP_MARKER)
	maxDisplays      = 32
	observationQueue = 256
)

var ErrNotPermitted = errors.New("macos: this program is not allowed to observe the keyboard and mouse")

type Quartz struct{}

func CanPost() bool {
	return bool(C.lanhopCanPost())
}

func CanListen() bool {
	return bool(C.lanhopCanListen())
}

func (Quartz) PostKey(key KeyPress) {
	C.lanhopPostKey(C.uint16_t(key.Code), C.bool(key.Down), C.bool(key.Modifier), C.uint64_t(key.Flags))
}

func (Quartz) PostMouse(mouse MouseEvent) {
	C.lanhopPostMouse(C.int(mouse.Action), C.double(mouse.At.X), C.double(mouse.At.Y), C.int(mouse.Button),
		C.int64_t(mouse.Clicks), C.int64_t(mouse.DX), C.int64_t(mouse.DY), C.uint64_t(mouse.Flags))
}

func (Quartz) PostScroll(scroll Scroll) {
	C.lanhopPostScroll(C.int32_t(scroll.Vertical), C.int32_t(scroll.Horizontal), C.uint64_t(scroll.Flags))
}

func (Quartz) Cursor() Point {
	var x, y C.double
	C.lanhopCursor(&x, &y)
	return Point{X: float64(x), Y: float64(y)}
}

func (Quartz) Displays() []Rect {
	var raw [maxDisplays * 4]C.double
	count := int(C.lanhopDisplays(&raw[0], maxDisplays))
	displays := make([]Rect, count)
	for i := range displays {
		displays[i] = Rect{X: float64(raw[i*4]), Y: float64(raw[i*4+1]), Width: float64(raw[i*4+2]), Height: float64(raw[i*4+3])}
	}
	return displays
}

type Observation struct {
	Type             uint32
	KeyCode          uint16
	Flags            Flags
	At               Point
	DX, DY           int64
	Clicks           int64
	Button           int64
	ScrollVertical   int64
	ScrollHorizontal int64
	Marker           int64
}

type Tap struct {
	native   *C.lanhopTap
	handle   cgo.Handle
	observed chan Observation
	ready    chan struct{}
	stopped  chan struct{}
	closing  sync.Once
}

func StartTap() (*Tap, error) {
	tap := &Tap{
		observed: make(chan Observation, observationQueue),
		ready:    make(chan struct{}),
		stopped:  make(chan struct{}),
	}
	tap.handle = cgo.NewHandle(tap)
	tap.native = C.lanhopTapCreate(C.uintptr_t(tap.handle))
	if tap.native == nil {
		tap.handle.Delete()
		return nil, ErrNotPermitted
	}
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		C.lanhopTapRun(tap.native)
		close(tap.stopped)
	}()
	<-tap.ready
	return tap, nil
}

func (t *Tap) Observed() <-chan Observation {
	return t.observed
}

func (t *Tap) Close() error {
	t.closing.Do(func() {
		C.lanhopTapStop(t.native)
		<-t.stopped
		C.lanhopTapFree(t.native)
		t.handle.Delete()
	})
	return nil
}

//export lanhopTapReady
func lanhopTapReady(handle C.uintptr_t) {
	close(cgo.Handle(handle).Value().(*Tap).ready)
}

//export lanhopObserved
func lanhopObserved(handle C.uintptr_t, seen *C.lanhopObservation) {
	observation := Observation{
		Type:             uint32(seen._type),
		KeyCode:          uint16(seen.keyCode),
		Flags:            Flags(seen.flags),
		At:               Point{X: float64(seen.x), Y: float64(seen.y)},
		DX:               int64(seen.deltaX),
		DY:               int64(seen.deltaY),
		Clicks:           int64(seen.clicks),
		Button:           int64(seen.button),
		ScrollVertical:   int64(seen.scrollVertical),
		ScrollHorizontal: int64(seen.scrollHorizontal),
		Marker:           int64(seen.marker),
	}
	select {
	case cgo.Handle(handle).Value().(*Tap).observed <- observation:
	default:
	}
}
