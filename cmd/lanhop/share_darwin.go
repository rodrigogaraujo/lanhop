//go:build darwin && cgo

package main

import (
	"context"
	"errors"
	"fmt"
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
		shield, shieldMissed := macos.ShieldCursor(done)
		if shieldMissed != nil {
			fmt.Fprintf(log, "lanhop could not put its window under the cursor before it ended the file drag on this Mac: %v\n", shieldMissed)
		}
		ended := macos.EndSourceDrag()
		if shieldMissed == nil {
			if err := shield.Close(); err != nil {
				fmt.Fprintf(log, "lanhop could not take its window from under the cursor: %v\n", err)
			}
		}
		if ended != nil {
			return ended
		}
		if left, marks := capture.(marksTheLeftButton); marks {
			left.LeftHeldUntilRealUp()
		}
		return nil
	}
}
