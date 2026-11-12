//go:build linux && (386 || amd64 || arm || arm64 || loong64 || riscv64)

package main

import "github.com/rodrigogaraujo/lanhop/internal/evdev"

func startCapture() (inputCapture, error) {
	capture, err := evdev.StartCapture()
	if err != nil {
		return nil, err
	}
	return capture, nil
}
