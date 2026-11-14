//go:build linux && containertest && (386 || amd64 || arm || arm64 || loong64 || riscv64)

package main

import (
	"fmt"

	"github.com/rodrigogaraujo/lanhop/internal/receiver"
	"github.com/rodrigogaraujo/lanhop/internal/x11"
)

func openReplay() (receiver.Replay, error) {
	replay, err := x11.OpenXTestReplay()
	if err != nil {
		return nil, fmt.Errorf("this lanhop is built for the container test and replays its input through XTEST, which it cannot reach: %w", err)
	}
	return replay, nil
}
