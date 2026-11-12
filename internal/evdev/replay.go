//go:build linux && (386 || amd64 || arm || arm64 || loong64 || riscv64)

package evdev

import (
	"errors"

	"github.com/rodrigogaraujo/lanhop/internal/event"
)

type destination uint8

const (
	toKeyboardAndMouse destination = 1 << iota
	toPointer
)

type reportRouter struct {
	reached destination
}

func (r *reportRouter) route(e event.Event) destination {
	if isEndOfReport(e) {
		reached := r.reached
		r.reached = 0
		return reached
	}
	to := toKeyboardAndMouse
	if e.Type == event.TypeAbsolute {
		to = toPointer
	}
	r.reached |= to
	return to
}

func isEndOfReport(e event.Event) bool {
	return e.Type == event.TypeSync && e.Code == event.SyncReport
}

type Replay struct {
	keyboardAndMouse *VirtualDevice
	pointer          *VirtualDevice
	report           reportRouter
}

func NewReplay(keyboardAndMouseName, pointerName string) (*Replay, error) {
	keyboardAndMouse, err := CreateVirtualDevice(keyboardAndMouseName)
	if err != nil {
		return nil, err
	}
	pointer, err := createAbsolutePointer(pointerName)
	if err != nil {
		keyboardAndMouse.Close()
		return nil, err
	}
	return &Replay{keyboardAndMouse: keyboardAndMouse, pointer: pointer}, nil
}

func (r *Replay) Emit(events ...event.Event) error {
	for _, e := range events {
		to := r.report.route(e)
		if to&toPointer != 0 {
			if err := r.pointer.Emit(e); err != nil {
				return err
			}
		}
		if to&toKeyboardAndMouse != 0 {
			if err := r.keyboardAndMouse.Emit(e); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *Replay) Close() error {
	return errors.Join(r.keyboardAndMouse.Close(), r.pointer.Close())
}
