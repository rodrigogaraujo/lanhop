//go:build darwin && cgo

package macos

/*
#cgo LDFLAGS: -framework ApplicationServices -framework CoreFoundation -framework IOKit
#include "quartz_darwin.h"
*/
import "C"

import (
	"errors"
	"runtime"
	"runtime/cgo"
	"sync"
	"time"
)

const (
	maxDisplays      = 32
	observationQueue = 4096
)

var (
	_ [0]struct{} = [Marker - C.LANHOP_MARKER]struct{}{}
	_ [0]struct{} = [TypeLeftMouseDown - C.LANHOP_TYPE_LEFT_MOUSE_DOWN]struct{}{}
	_ [0]struct{} = [TypeLeftMouseUp - C.LANHOP_TYPE_LEFT_MOUSE_UP]struct{}{}
	_ [0]struct{} = [TypeRightMouseDown - C.LANHOP_TYPE_RIGHT_MOUSE_DOWN]struct{}{}
	_ [0]struct{} = [TypeRightMouseUp - C.LANHOP_TYPE_RIGHT_MOUSE_UP]struct{}{}
	_ [0]struct{} = [TypeMouseMoved - C.LANHOP_TYPE_MOUSE_MOVED]struct{}{}
	_ [0]struct{} = [TypeLeftMouseDragged - C.LANHOP_TYPE_LEFT_MOUSE_DRAGGED]struct{}{}
	_ [0]struct{} = [TypeRightMouseDragged - C.LANHOP_TYPE_RIGHT_MOUSE_DRAGGED]struct{}{}
	_ [0]struct{} = [TypeKeyDown - C.LANHOP_TYPE_KEY_DOWN]struct{}{}
	_ [0]struct{} = [TypeKeyUp - C.LANHOP_TYPE_KEY_UP]struct{}{}
	_ [0]struct{} = [TypeFlagsChanged - C.LANHOP_TYPE_FLAGS_CHANGED]struct{}{}
	_ [0]struct{} = [TypeScrollWheel - C.LANHOP_TYPE_SCROLL_WHEEL]struct{}{}
	_ [0]struct{} = [TypeOtherMouseDown - C.LANHOP_TYPE_OTHER_MOUSE_DOWN]struct{}{}
	_ [0]struct{} = [TypeOtherMouseUp - C.LANHOP_TYPE_OTHER_MOUSE_UP]struct{}{}
	_ [0]struct{} = [TypeOtherMouseDragged - C.LANHOP_TYPE_OTHER_MOUSE_DRAGGED]struct{}{}
	_ [0]struct{} = [TypeTapDisabled - C.LANHOP_TYPE_TAP_DISABLED]struct{}{}
)

var ErrNotPermitted = errors.New("macos: this program is not allowed to observe the keyboard and mouse")

type Quartz struct{}

func CanPost() bool {
	return bool(C.lanhopCanPost())
}

func CanListen() bool {
	return bool(C.lanhopCanListen())
}

func IsTrusted() bool {
	return bool(C.lanhopIsTrusted())
}

func (Quartz) PostKey(key KeyPress) {
	C.lanhopPostKey(C.uint16_t(key.Code), C.bool(key.Down), C.bool(key.Modifier), C.bool(key.Repeat), C.uint64_t(key.Flags))
}

func (Quartz) KeyRepeat() (delay, interval time.Duration, known bool) {
	var delayNanoseconds, intervalNanoseconds C.int64_t
	known = bool(C.lanhopKeyRepeat(&delayNanoseconds, &intervalNanoseconds))
	return time.Duration(delayNanoseconds), time.Duration(intervalNanoseconds), known
}

func nativeAction(action MouseAction) C.int {
	switch action {
	case MouseDragged:
		return C.lanhopActionDragged
	case MouseDown:
		return C.lanhopActionDown
	case MouseUp:
		return C.lanhopActionUp
	}
	return C.lanhopActionMoved
}

func (Quartz) PostMouse(mouse MouseEvent) {
	C.lanhopPostMouse(nativeAction(mouse.Action), C.double(mouse.At.X), C.double(mouse.At.Y), C.int(mouse.Button),
		C.int64_t(mouse.Clicks), C.int64_t(mouse.DX), C.int64_t(mouse.DY), C.uint64_t(mouse.Flags))
}

func (Quartz) PostScroll(scroll Scroll) {
	C.lanhopPostScroll(C.int32_t(scroll.Vertical), C.int32_t(scroll.Horizontal), C.uint64_t(scroll.Flags))
}

func (Quartz) KeyDown(macOSKey uint16) bool {
	return bool(C.lanhopKeyIsDown(C.uint16_t(macOSKey)))
}

func (Quartz) ButtonDown(number int) bool {
	return bool(C.lanhopButtonIsDown(C.int(number)))
}

func (Quartz) Cursor() Point {
	var x, y C.double
	C.lanhopCursor(&x, &y)
	return Point{X: float64(x), Y: float64(y)}
}

func (Quartz) PlaceCursor(at Point) {
	C.lanhopPlaceCursor(C.double(at.X), C.double(at.Y))
}

func (Quartz) PutCursorBack(at Point) {
	C.lanhopPutCursorBack(C.double(at.X), C.double(at.Y))
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

type Tap struct {
	native   *C.lanhopTap
	handle   cgo.Handle
	keeps    func(Observation) bool
	observed chan Observation
	missed   chan struct{}
	ready    chan struct{}
	stopped  chan struct{}
	closing  sync.Once
}

type tapPlace bool

const (
	seesInputFirst tapPlace = false
	seesInputLast  tapPlace = true
)

func StartTap() (*Tap, error) {
	return startTap(seesInputFirst, nil)
}

func startTap(place tapPlace, keeps func(Observation) bool) (*Tap, error) {
	tap := &Tap{
		keeps:    keeps,
		observed: make(chan Observation, observationQueue),
		missed:   make(chan struct{}, 1),
		ready:    make(chan struct{}),
		stopped:  make(chan struct{}),
	}
	tap.handle = cgo.NewHandle(tap)
	tap.native = C.lanhopTapCreate(C.uintptr_t(tap.handle), C.bool(keeps != nil), C.bool(place))
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

func (t *Tap) Overflowed() <-chan struct{} {
	return t.missed
}

func (t *Tap) reportOverflow() {
	select {
	case t.missed <- struct{}{}:
	default:
	}
}

func (t *Tap) Close() error {
	t.closing.Do(func() {
		C.lanhopTapStop(t.native)
		<-t.stopped
		C.lanhopTapFree(t.native)
		t.handle.Delete()
		close(t.observed)
	})
	return nil
}

//export lanhopTapReady
func lanhopTapReady(handle C.uintptr_t) {
	close(cgo.Handle(handle).Value().(*Tap).ready)
}

//export lanhopObserved
func lanhopObserved(handle C.uintptr_t, seen *C.lanhopObservation) C.bool {
	tap := cgo.Handle(handle).Value().(*Tap)
	observation := Observation{
		Type:             uint32(seen._type),
		KeyCode:          uint16(seen.keyCode),
		Repeat:           bool(seen.autorepeat),
		Flags:            Flags(seen.flags),
		At:               Point{X: float64(seen.x), Y: float64(seen.y)},
		DX:               int64(seen.deltaX),
		DY:               int64(seen.deltaY),
		Clicks:           int64(seen.clicks),
		Button:           int64(seen.button),
		ScrollVertical:   int64(seen.scrollVertical),
		ScrollHorizontal: int64(seen.scrollHorizontal),
		PointsVertical:   float64(seen.pointsVertical),
		PointsHorizontal: float64(seen.pointsHorizontal),
		Continuous:       bool(seen.continuous),
		Marker:           int64(seen.marker),
	}
	return C.bool(tap.take(observation))
}

func (t *Tap) take(observation Observation) bool {
	select {
	case t.observed <- observation:
	default:
		t.reportOverflow()
		return false
	}
	return t.keeps != nil && t.keeps(observation)
}

//export lanhopTapMissedInput
func lanhopTapMissedInput(handle C.uintptr_t) {
	tap := cgo.Handle(handle).Value().(*Tap)
	select {
	case tap.observed <- Observation{Type: TypeTapDisabled}:
	default:
		tap.reportOverflow()
	}
}
