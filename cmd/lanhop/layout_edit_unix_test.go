//go:build unix

package main

import (
	"context"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestLaunchKeepsTheCommandOutOfTheProcessGroupThatCtrlCInTheTerminalStops(t *testing.T) {
	stillOpen := exec.Command("sleep", "60")
	t.Cleanup(func() {
		if stillOpen.Process != nil {
			stillOpen.Process.Kill()
		}
	})

	if err := launch(context.Background(), stillOpen, 10*time.Millisecond); err != nil {
		t.Fatalf("launch: %v", err)
	}

	group, err := syscall.Getpgid(stillOpen.Process.Pid)
	if err != nil {
		t.Fatalf("Getpgid: %v", err)
	}
	if group == syscall.Getpgrp() {
		t.Errorf("the command runs in the process group of lanhop, %d, so Ctrl+C in the terminal would stop the browser it opened", group)
	}
}
