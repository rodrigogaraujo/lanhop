//go:build darwin && cgo

package main

import (
	"context"
	"errors"
	"io"

	"github.com/rodrigogaraujo/lanhop/internal/keymap"
	"github.com/rodrigogaraujo/lanhop/internal/macos"
	"github.com/rodrigogaraujo/lanhop/internal/sharer"
)

var errNotAllowedToShare = errors.New("lanhop is not allowed to read the keyboard and mouse of this Mac yet: in System Settings > Privacy & Security > Accessibility, allow the program that starts lanhop, as docs/setup-macos.md explains, then start it again")

type marksTheLeftButton interface {
	LeftHeldUntilRealUp()
}

func startCapture() (inputCapture, error) {
	return captureWhenAllowed(macos.CanListen() && macos.CanPost())
}

func captureWhenAllowed(allowed bool) (inputCapture, error) {
	if !allowed {
		return nil, errNotAllowedToShare
	}
	capture, err := macos.StartCapture(keymap.Translator{}, macos.HotkeyFlags)
	if err != nil {
		return nil, err
	}
	return capture, nil
}

func endTheDragHere(done context.Context, capture sharer.Capture, log io.Writer) func() error {
	return func() error {
		if err := endTheSourceDragUnderAShield(done, shieldTheCursor, endSourceDragHere, nil, log, nil); err != nil {
			return err
		}
		if left, marks := capture.(marksTheLeftButton); marks {
			left.LeftHeldUntilRealUp()
		}
		return nil
	}
}
