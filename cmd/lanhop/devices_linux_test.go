//go:build linux && (386 || amd64 || arm || arm64 || loong64 || riscv64)

package main

import (
	"bytes"
	"testing"

	"github.com/rodrigogaraujo/lanhop/internal/evdev"
)

func TestDevicesArePrintedWithTheirKindInAColumnAndLanhopsOwnIsMarked(t *testing.T) {
	devices := []evdev.Info{
		{Path: "/dev/input/event0", Name: "AT Translated Set 2 keyboard", Kind: evdev.Keyboard},
		{Path: "/dev/input/event1", Name: "Power Button", Kind: evdev.Other},
		{Path: "/dev/input/event2", Name: "USB Optical Mouse", Kind: evdev.Mouse},
		{Path: "/dev/input/event3", Name: "Keyboard with trackball", Kind: evdev.Keyboard | evdev.Mouse},
		{Path: "/dev/input/event14", Name: "lanhop", Kind: evdev.Keyboard | evdev.Mouse, Own: true},
	}
	want := "" +
		"/dev/input/event0   keyboard        AT Translated Set 2 keyboard\n" +
		"/dev/input/event1   other           Power Button\n" +
		"/dev/input/event2   mouse           USB Optical Mouse\n" +
		"/dev/input/event3   keyboard+mouse  Keyboard with trackball\n" +
		"/dev/input/event14  keyboard+mouse  lanhop (lanhop's own device)\n"

	var out bytes.Buffer
	if err := printDevices(&out, devices); err != nil {
		t.Fatalf("printDevices: %v", err)
	}
	if got := out.String(); got != want {
		t.Errorf("printed devices:\n%s\nwant:\n%s", got, want)
	}
}
