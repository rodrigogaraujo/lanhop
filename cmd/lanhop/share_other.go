//go:build !(linux && (386 || amd64 || arm || arm64 || loong64 || riscv64))

package main

import (
	"fmt"
	"runtime"
)

func startCapture() (inputCapture, error) {
	return nil, fmt.Errorf("sharing the keyboard and mouse of this machine is not supported on %s/%s yet", runtime.GOOS, runtime.GOARCH)
}
