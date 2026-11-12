//go:build darwin && cgo

package main

import (
	"errors"

	"github.com/rodrigogaraujo/lanhop/internal/keymap"
	"github.com/rodrigogaraujo/lanhop/internal/macos"
)

var errNotAllowedToShare = errors.New("lanhop is not allowed to read the keyboard and mouse of this Mac yet: grant the Accessibility permission to the program that starts lanhop, as docs/setup-macos.md explains, then start it again")

func startCapture() (inputCapture, error) {
	return captureWhenAllowed(macos.CanListen() && macos.CanPost())
}

func captureWhenAllowed(allowed bool) (inputCapture, error) {
	if !allowed {
		return nil, errNotAllowedToShare
	}
	capture, err := macos.StartCapture(keymap.Translator{})
	if err != nil {
		return nil, err
	}
	return capture, nil
}
