//go:build darwin && cgo

package main

import (
	"errors"

	"github.com/rodrigogaraujo/lanhop/internal/keymap"
	"github.com/rodrigogaraujo/lanhop/internal/macos"
	"github.com/rodrigogaraujo/lanhop/internal/receiver"
)

var errNotAllowedToControl = errors.New("lanhop is not allowed to control this Mac yet: in System Settings > Privacy & Security > Accessibility, allow the program that starts lanhop, as docs/setup-macos.md explains, then start it again")

func openReplay() (receiver.Replay, error) {
	return replayWhenAllowed(macos.CanPost())
}

func replayWhenAllowed(allowed bool) (receiver.Replay, error) {
	if !allowed {
		return nil, errNotAllowedToControl
	}
	return macos.NewReplay(macos.Quartz{}, keymap.Translator{}), nil
}
