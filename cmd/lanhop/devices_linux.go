//go:build linux && (386 || amd64 || arm || arm64 || loong64 || riscv64)

package main

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/rodrigogaraujo/lanhop/internal/evdev"
)

func listDevices(out io.Writer) error {
	devices, err := evdev.List()
	if err != nil {
		return err
	}
	return printDevices(out, devices)
}

func printDevices(out io.Writer, devices []evdev.Info) error {
	table := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	for _, device := range devices {
		fmt.Fprintf(table, "%s\t%s\t%s\n", device.Path, device.Kind, describedName(device))
	}
	return table.Flush()
}

func describedName(device evdev.Info) string {
	if device.Own {
		return device.Name + " (lanhop's own device)"
	}
	return device.Name
}
