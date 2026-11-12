//go:build linux && (386 || amd64 || arm || arm64 || loong64 || riscv64)

package main

import (
	"fmt"
	"io"

	"github.com/rodrigogaraujo/lanhop/internal/evdev"
)

func listDevices(out io.Writer) error {
	devices, err := evdev.List()
	if err != nil {
		return err
	}
	for _, device := range devices {
		fmt.Fprintf(out, "%s\t%s\n", device.Path, device.Name)
	}
	return nil
}
