//go:build darwin && cgo

package main

import (
	"strings"
	"testing"
)

func TestReceiveWithoutThePermissionSaysHowToGrantIt(t *testing.T) {
	replay, err := replayWhenAllowed(false)
	if replay != nil {
		t.Error("a replay was opened without the permission")
	}
	if err == nil || !strings.Contains(err.Error(), "System Settings > Privacy & Security > Accessibility") || !strings.Contains(err.Error(), "docs/setup-macos.md") {
		t.Errorf("error = %v, want it to name the System Settings pane and the setup guide", err)
	}
}

func TestReceiveWithThePermissionOpensAReplay(t *testing.T) {
	replay, err := replayWhenAllowed(true)
	if err != nil || replay == nil {
		t.Fatalf("replayWhenAllowed(true) = %v, %v, want a replay", replay, err)
	}
	if err := replay.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}
