//go:build linux && (386 || amd64 || arm || arm64 || loong64 || riscv64)

package main

import (
	"fmt"

	"github.com/rodrigogaraujo/lanhop/internal/evdev"
	"github.com/rodrigogaraujo/lanhop/internal/receiver"
)

const virtualDeviceName = "lanhop"

func openReplay() (receiver.Replay, error) {
	device, err := evdev.CreateVirtualDevice(virtualDeviceName)
	if err != nil {
		return nil, fmt.Errorf("cannot create the virtual keyboard and mouse: %w", err)
	}
	return device, nil
}
