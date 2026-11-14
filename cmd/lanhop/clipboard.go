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
	"github.com/rodrigogaraujo/lanhop/internal/features"
	"github.com/rodrigogaraujo/lanhop/internal/receiver"
	"github.com/rodrigogaraujo/lanhop/internal/sharer"
)

const (
	clipboardSwitchFileName = "clipboard"
	switchedOff             = "off"
	switchedOn              = "on"
	followedAtTheNextFocus  = "lanhop share and lanhop receive follow this the next time focus moves."
)

type copySwitches struct {
	clipboard bool
	files     bool
}

func copySwitchesIn(directory string) copySwitches {
	return copySwitches{
		clipboard: switchFileSaysOn(filepath.Join(directory, clipboardSwitchFileName)),
		files:     switchFileSaysOn(filepath.Join(directory, filesSwitchFileName)),
	}
}

func (s copySwitches) filesMove() bool {
	return s.clipboard && s.files
}

func (s copySwitches) clipboardState() string {
	switch {
	case !s.clipboard:
		return switchedOff
	case !s.files:
		return "on for text, off for files"
	}
	return switchedOn
}

func (s copySwitches) afterTheClipboardSwitch() string {
	switch {
	case !s.clipboard:
		return "Copies of text and files no longer move to or from this machine. " + followedAtTheNextFocus
	case !s.files:
		return "Copies of text move to and from this machine, and copies of files stay off. " + followedAtTheNextFocus
	}
	return "Copies of text and files move to and from this machine. " + followedAtTheNextFocus
}

func switchClipboard(args []string, stdout io.Writer) error {
	return showOrFlipSwitch("clipboard", clipboardSwitchFileName, args, stdout, copySwitches.clipboardState, copySwitches.afterTheClipboardSwitch)
}

func showOrFlipSwitch(command, fileName string, args []string, stdout io.Writer, state, flipped func(copySwitches) string) error {
	word, err := switchWordIn(command, args)
	if err != nil {
		return err
	}
	directory, err := configDirectory()
	if err != nil {
		return err
	}
	if word == "" {
		fmt.Fprintln(stdout, state(copySwitchesIn(directory)))
		return nil
	}
	if err := flipSwitch(directory, fileName, word); err != nil {
		return err
	}
	fmt.Fprintln(stdout, flipped(copySwitchesIn(directory)))
	return nil
}

func switchWordIn(command string, args []string) (string, error) {
	switch {
	case len(args) == 0:
		return "", nil
	case len(args) > 1:
		return "", usageError(command + " takes at most one word: on or off")
	case args[0] == switchedOn || args[0] == switchedOff:
		return args[0], nil
	}
	return "", usageError(fmt.Sprintf("%s takes on or off, not %q", command, args[0]))
}

func flipSwitch(directory, fileName, word string) error {
	path := filepath.Join(directory, fileName)
	if word == switchedOn {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(switchedOff+"\n"), 0o600)
}

func switchFileSaysOn(path string) bool {
	contents, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return true
	}
	if err != nil {
		return false
	}
	return !strings.EqualFold(strings.TrimSpace(string(contents)), switchedOff)
}

func clipboardConfigIn(done context.Context, directory string, announced *features.Set, stdout io.Writer) (config clipshare.Config, release func(), supported bool) {
	if openClipboard == nil {
		return clipshare.Config{}, func() {}, false
	}
	arrivals, release := keepArrivals(done, stdout)
	return clipshare.Config{
		Open:       openClipboard,
		SwitchedOn: func() bool { return copySwitchesIn(directory).clipboard },
		FilesOn:    func() bool { return copySwitchesIn(directory).filesMove() },
		Arrivals:   arrivals,
		Log:        stdout,
		Features:   announced,
	}, release, true
}

func shareTheClipboard(done context.Context, machine *sharer.Sharer, directory string, awaitData clipshare.AwaitData, announced *features.Set, alongside sharer.Clipboard, stdout io.Writer) (stop func()) {
	config, release, supported := clipboardConfigIn(done, directory, announced, stdout)
	if !supported {
		machine.ShareClipboard(alongside)
		return func() {}
	}
	config.AwaitData = awaitData
	central := clipshare.NewCentral(config)
	machine.ShareClipboard(sharer.Together(central, alongside))
	return inTurn(runUntilStopped(done, central.Run), release)
}

func clipboardOfThisSide(done context.Context, directory string, announced *features.Set, dialData clipshare.DialData, stdout io.Writer) (receiver.Clipboard, func()) {
	config, release, supported := clipboardConfigIn(done, directory, announced, stdout)
	if !supported {
		return nil, func() {}
	}
	config.DialData = dialData
	side := clipshare.NewSide(config)
	return side, inTurn(runUntilStopped(done, side.Run), release)
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

func inTurn(steps ...func()) func() {
	return func() {
		for _, step := range steps {
			step()
		}
	}
}
