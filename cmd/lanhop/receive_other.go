//go:build !(linux && (386 || amd64 || arm || arm64 || loong64 || riscv64))

package main

import (
	"fmt"
	"runtime"

	"github.com/rodrigogaraujo/lanhop/internal/receiver"
)

func openReplay() (receiver.Replay, error) {
	return nil, fmt.Errorf("receiving is not supported on %s/%s yet", runtime.GOOS, runtime.GOARCH)
}
