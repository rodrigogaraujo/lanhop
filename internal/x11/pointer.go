//go:build linux

package x11

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

const (
	opGrabPointer   = 26
	opUngrabPointer = 27
	opQueryPointer  = 38

	buttonPressMask   = 0x4
	buttonReleaseMask = 0x8
	pointerMotionMask = 0x40
	grabModeAsync     = 1
	atomWindow        = 33
	firstButtonMask   = 0x100
	grabRetryEvery    = 50 * time.Millisecond
)

var ErrNotGrabbed = errors.New("x11: the pointer could not be grabbed")

type PointerState struct {
	X, Y  int
	Child uint32
	Mask  uint16
}

func (p PointerState) Held() []int {
	var held []int
	for button := 1; button <= 5; button++ {
		if p.Mask&(firstButtonMask<<(button-1)) != 0 {
			held = append(held, button)
		}
	}
	return held
}

type GrabStatus byte

const (
	GrabSuccess        GrabStatus = 0
	GrabAlreadyGrabbed GrabStatus = 1
	GrabInvalidTime    GrabStatus = 2
	GrabNotViewable    GrabStatus = 3
	GrabFrozen         GrabStatus = 4
)

func (s GrabStatus) String() string {
	switch s {
	case GrabSuccess:
		return "Success"
	case GrabAlreadyGrabbed:
		return "AlreadyGrabbed"
	case GrabInvalidTime:
		return "InvalidTime"
	case GrabNotViewable:
		return "NotViewable"
	case GrabFrozen:
		return "Frozen"
	}
	return fmt.Sprintf("status %d", byte(s))
}

func (d *Drags) Pointer() (PointerState, error) {
	return d.queryPointer(d.c.root)
}

func (d *Drags) queryPointer(window uint32) (PointerState, error) {
	reply, err := d.c.call(newRequest(opQueryPointer, 0).u32(window).done())
	if err != nil {
		return PointerState{}, err
	}
	le := binary.LittleEndian
	return PointerState{
		X:     int(int16(le.Uint16(reply[16:18]))),
		Y:     int(int16(le.Uint16(reply[18:20]))),
		Child: le.Uint32(reply[12:16]),
		Mask:  le.Uint16(reply[24:26]),
	}, nil
}

func (d *Drags) GrabPointer(ctx context.Context, patience time.Duration) (GrabStatus, int, error) {
	deadline := time.Now().Add(patience)
	for tries := 1; ; tries++ {
		request := newRequest(opGrabPointer, 0).u32(d.c.root).u16(buttonPressMask | buttonReleaseMask | pointerMotionMask).
			u8(grabModeAsync).u8(grabModeAsync).u32(atomNone).u32(atomNone).u32(currentTime).done()
		reply, err := d.c.call(request)
		if err != nil {
			return 0, tries, err
		}
		status := GrabStatus(reply[1])
		if status == GrabSuccess {
			return status, tries, nil
		}
		retry := status == GrabAlreadyGrabbed || status == GrabFrozen
		if !retry || time.Now().After(deadline) {
			return status, tries, fmt.Errorf("%w: %s", ErrNotGrabbed, status)
		}
		select {
		case <-time.After(grabRetryEvery):
		case <-ctx.Done():
			return status, tries, ctx.Err()
		}
	}
}

func (d *Drags) UngrabPointer() error {
	if err := d.c.send(newRequest(opUngrabPointer, 0).u32(currentTime).done()); err != nil {
		return err
	}
	_, err := d.Pointer()
	return err
}

type MoveMeasure struct {
	Baseline       bool
	BaselineBefore PointerState
	BaselineAfter  PointerState
	Grab           GrabStatus
	Before         PointerState
	After          PointerState
	MotionEvents   int
}

func (d *Drags) MeasureMoveWhileGrabbed(ctx context.Context, move func(dx, dy int) error, dx, dy int, settle time.Duration, baseline bool) (MoveMeasure, error) {
	measure := MoveMeasure{Baseline: baseline}
	var err error
	if baseline {
		if measure.BaselineBefore, err = d.Pointer(); err != nil {
			return measure, err
		}
		if err := move(dx, dy); err != nil {
			return measure, err
		}
		time.Sleep(settle)
		if measure.BaselineAfter, err = d.Pointer(); err != nil {
			return measure, err
		}
		dx, dy = -dx, -dy
	}
	status, _, err := d.GrabPointer(ctx, time.Second)
	measure.Grab = status
	if err != nil {
		return measure, err
	}
	defer d.UngrabPointer()
	drain(d.pointer)
	if measure.Before, err = d.Pointer(); err != nil {
		return measure, err
	}
	if err := move(dx, dy); err != nil {
		return measure, err
	}
	time.Sleep(settle)
	if measure.After, err = d.Pointer(); err != nil {
		return measure, err
	}
	for {
		select {
		case event := <-d.pointer:
			if event.Kind == PointerMoved {
				measure.MotionEvents++
			}
		default:
			return measure, nil
		}
	}
}
