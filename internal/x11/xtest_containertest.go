//go:build linux && containertest

package x11

import (
	"math"
	"sync"

	"github.com/rodrigogaraujo/lanhop/internal/event"
)

const (
	xtestKeyPress       = 2
	xtestKeyRelease     = 3
	xtestMotionAbsolute = 0
	xtestMotionRelative = 1
	xtestHighestKeycode = 255
	xtestKeycodeOffset  = 8
)

type XTestReplay struct {
	mutex  sync.Mutex
	h      *harnessConn
	opcode byte
	width  int
	height int
	motion xtestMotion
}

type xtestMotion struct {
	dx, dy     int
	x, y       int
	hasX, hasY bool
}

func OpenXTestReplay() (*XTestReplay, error) {
	return openSession(openXTestReplay)
}

func openXTestReplay(d display, cookie []byte) (*XTestReplay, error) {
	h, err := dialHarness(d, cookie, func([]byte) {})
	if err != nil {
		return nil, err
	}
	replay := &XTestReplay{h: h}
	replay.opcode, err = h.extensionOpcode("XTEST")
	if err == nil {
		replay.width, replay.height, err = h.screenSize()
	}
	if err != nil {
		h.close()
		return nil, err
	}
	return replay, nil
}

func (r *XTestReplay) Emit(events ...event.Event) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	for _, e := range events {
		if err := r.apply(e); err != nil {
			return err
		}
	}
	return nil
}

func (r *XTestReplay) Close() error {
	return r.h.close()
}

func (r *XTestReplay) apply(e event.Event) error {
	switch e.Type {
	case event.TypeRelative:
		switch e.Code {
		case event.RelX:
			r.motion.dx += int(e.Value)
		case event.RelY:
			r.motion.dy += int(e.Value)
		}
	case event.TypeAbsolute:
		switch e.Code {
		case event.AbsX:
			r.motion.x, r.motion.hasX = xtestScaled(e.Value, r.width), true
		case event.AbsY:
			r.motion.y, r.motion.hasY = xtestScaled(e.Value, r.height), true
		}
	case event.TypeKey:
		if err := r.flush(); err != nil {
			return err
		}
		return r.key(e)
	case event.TypeSync:
		return r.flush()
	}
	return nil
}

func xtestScaled(position int32, size int) int {
	within := min(max(position, 0), event.AbsoluteMax)
	return int(math.Round(float64(within) * float64(size-1) / float64(event.AbsoluteMax)))
}

func (r *XTestReplay) flush() error {
	pending := r.motion
	r.motion = xtestMotion{}
	if pending.hasX || pending.hasY {
		if !pending.hasX || !pending.hasY {
			x, y, _, err := r.h.pointer()
			if err != nil {
				return err
			}
			if !pending.hasX {
				pending.x = x
			}
			if !pending.hasY {
				pending.y = y
			}
		}
		if err := r.fake(eventMotionNotify, xtestMotionAbsolute, r.h.root, pending.x, pending.y); err != nil {
			return err
		}
	}
	if pending.dx != 0 || pending.dy != 0 {
		return r.fake(eventMotionNotify, xtestMotionRelative, atomNone, pending.dx, pending.dy)
	}
	return nil
}

func (r *XTestReplay) key(e event.Event) error {
	if e.Value == event.KeyRepeated {
		return nil
	}
	pressed := e.Value == event.KeyPressed
	if button, isButton := xtestButton(e.Code); isButton {
		kind := byte(eventButtonRelease)
		if pressed {
			kind = eventButtonPress
		}
		return r.fake(kind, button, atomNone, 0, 0)
	}
	if int(e.Code)+xtestKeycodeOffset > xtestHighestKeycode {
		return nil
	}
	kind := byte(xtestKeyRelease)
	if pressed {
		kind = xtestKeyPress
	}
	return r.fake(kind, byte(int(e.Code)+xtestKeycodeOffset), atomNone, 0, 0)
}

func xtestButton(code uint16) (byte, bool) {
	switch code {
	case event.ButtonLeft:
		return 1, true
	case event.ButtonMiddle:
		return 2, true
	case event.ButtonRight:
		return 3, true
	}
	return 0, false
}

func (r *XTestReplay) fake(kind, detail byte, root uint32, x, y int) error {
	const fakeInput = 2
	request := newRequest(r.opcode, fakeInput).u8(kind).u8(detail).u16(0).u32(currentTime).u32(root).
		u32(0).u32(0).u16(uint16(int16(x))).u16(uint16(int16(y))).u32(0).u16(0).u8(0).u8(0).done()
	return r.h.send(request)
}
