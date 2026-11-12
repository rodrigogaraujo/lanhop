//go:build !(linux && (386 || amd64 || arm || arm64 || loong64 || riscv64))

package main

import (
	"fmt"
	"io"
	"runtime"
)

func listDevices(io.Writer) error {
	return fmt.Errorf("listing devices is not supported on %s/%s yet", runtime.GOOS, runtime.GOARCH)
}
