//go:build linux && (386 || amd64 || arm || arm64 || loong64 || riscv64)

package main

import (
	"fmt"

	"github.com/rodrigogaraujo/lanhop/internal/evdev"
	"github.com/rodrigogaraujo/lanhop/internal/receiver"
)

const (
	virtualDeviceName  = "lanhop"
	virtualPointerName = "lanhop pointer"
)

func openReplay() (receiver.Replay, error) {
	replay, err := evdev.NewReplay(virtualDeviceName, virtualPointerName)
	if err != nil {
		return nil, fmt.Errorf("cannot create the virtual keyboard, mouse and pointer: %w", err)
	}
	return replay, nil
}
