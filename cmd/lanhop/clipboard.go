package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/rodrigogaraujo/lanhop/internal/clipshare"
	"github.com/rodrigogaraujo/lanhop/internal/receiver"
	"github.com/rodrigogaraujo/lanhop/internal/sharer"
)

const (
	clipboardSwitchFileName = "clipboard"
	switchedOff             = "off"
	switchedOn              = "on"
)

func switchClipboard(args []string, stdout io.Writer) error {
	if len(args) > 1 {
		return usageError("clipboard takes at most one word: on or off")
	}
	directory, err := configDirectory()
	if err != nil {
		return err
	}
	path := filepath.Join(directory, clipboardSwitchFileName)
	if len(args) == 0 {
		fmt.Fprintln(stdout, clipboardSwitchWord(path))
		return nil
	}
	switch args[0] {
	case switchedOff:
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(switchedOff+"\n"), 0o600); err != nil {
			return err
		}
	case switchedOn:
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	default:
		return usageError(fmt.Sprintf("clipboard takes on or off, not %q", args[0]))
	}
	if clipboardSwitchedOn(path) {
		fmt.Fprintln(stdout, "Copies of text move to and from this machine. lanhop share and lanhop receive follow this the next time focus moves.")
	} else {
		fmt.Fprintln(stdout, "Copies of text no longer move to or from this machine. lanhop share and lanhop receive follow this the next time focus moves.")
	}
	return nil
}

func clipboardSwitchWord(path string) string {
	if clipboardSwitchedOn(path) {
		return switchedOn
	}
	return switchedOff
}

func clipboardSwitchedOn(path string) bool {
	contents, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return true
	}
	if err != nil {
		return false
	}
	return !strings.EqualFold(strings.TrimSpace(string(contents)), switchedOff)
}

func clipboardConfigIn(directory string, stdout io.Writer) (clipshare.Config, bool) {
	if openClipboard == nil {
		return clipshare.Config{}, false
	}
	path := filepath.Join(directory, clipboardSwitchFileName)
	return clipshare.Config{
		Open:       openClipboard,
		SwitchedOn: func() bool { return clipboardSwitchedOn(path) },
		Log:        stdout,
	}, true
}

func shareTheClipboard(done context.Context, machine *sharer.Sharer, directory string, stdout io.Writer) (stop func()) {
	config, supported := clipboardConfigIn(directory, stdout)
	if !supported {
		return func() {}
	}
	central := clipshare.NewCentral(config)
	machine.ShareClipboard(central)
	return runUntilStopped(done, central.Run)
}

func clipboardOfThisSide(done context.Context, directory string, stdout io.Writer) (receiver.Clipboard, func()) {
	config, supported := clipboardConfigIn(directory, stdout)
	if !supported {
		return nil, func() {}
	}
	side := clipshare.NewSide(config)
	return side, runUntilStopped(done, side.Run)
}

func runUntilStopped(done context.Context, run func(context.Context)) (stop func()) {
	running, cancel := context.WithCancel(done)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		run(running)
	}()
	return func() {
		cancel()
		<-stopped
	}
}
