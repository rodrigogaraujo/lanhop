//go:build linux

package x11

import (
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

const (
	opGrabPointer          = 26
	opUngrabPointer        = 27
	opQueryPointer         = 38
	opTranslateCoordinates = 40

	buttonPressMask   = 0x4
	buttonReleaseMask = 0x8
	pointerMotionMask = 0x40
	grabModeAsync     = 1
	grabSuccess       = 0
	grabTaken         = 1
	grabInvalidTime   = 2
	grabNotViewable   = 3
	grabFrozen        = 4
	grabRetryEvery    = 50 * time.Millisecond
)

var ErrNotGrabbed = errors.New("x11: the pointer could not be grabbed")

type pointerPosition struct {
	x, y int
}

func grabRefusal(status byte) string {
	switch status {
	case grabTaken:
		return "another program holds it"
	case grabInvalidTime:
		return "invalid time"
	case grabNotViewable:
		return "the window is not viewable"
	case grabFrozen:
		return "the pointer is frozen"
	}
	return fmt.Sprintf("status %d", status)
}

func (d *Drags) grabPointer() error {
	deadline := time.Now().Add(d.limits.grab)
	for {
		request := newRequest(opGrabPointer, 0).u32(d.c.root).u16(buttonPressMask | buttonReleaseMask | pointerMotionMask).
			u8(grabModeAsync).u8(grabModeAsync).u32(atomNone).u32(atomNone).u32(currentTime).done()
		reply, err := d.c.call(request)
		if err != nil {
			return err
		}
		status := reply[1]
		if status == grabSuccess {
			return nil
		}
		mayClearSoon := status == grabTaken || status == grabFrozen
		if !mayClearSoon || !time.Now().Before(deadline) {
			return fmt.Errorf("%w: %s", ErrNotGrabbed, grabRefusal(status))
		}
		select {
		case <-time.After(grabRetryEvery):
		case <-d.c.closed:
			return d.c.err()
		}
	}
}

func (d *Drags) ungrabPointer() {
	if d.c.send(newRequest(opUngrabPointer, 0).u32(currentTime).done()) == nil {
		d.queryPointer()
	}
}

func (d *Drags) queryPointer() (pointerPosition, error) {
	reply, err := d.c.call(newRequest(opQueryPointer, 0).u32(d.c.root).done())
	if err != nil {
		return pointerPosition{}, err
	}
	le := binary.LittleEndian
	return pointerPosition{x: int(int16(le.Uint16(reply[16:18]))), y: int(int16(le.Uint16(reply[18:20])))}, nil
}

func (d *Drags) childAt(window uint32, x, y int) (uint32, error) {
	reply, err := d.c.call(newRequest(opTranslateCoordinates, 0).u32(d.c.root).u32(window).u16(uint16(int16(x))).u16(uint16(int16(y))).done())
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(reply[8:12]), nil
}
