package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/layout"
	"github.com/rodrigogaraujo/lanhop/internal/pairing"
	"github.com/rodrigogaraujo/lanhop/internal/sharer"
)

const (
	layoutFileName      = "layout"
	layoutCheckInterval = time.Second

	layoutFileIs = "The layout file is %s\n\n"

	noLayoutFileYet = `There is no layout file yet. Write the names of the machines in it as they sit on the desk,
one row per line, from left to right, with a dot for an empty place. For example:

  # my desk
  laptop   desktop   macbook
  .        server    .

This machine is %q. "lanhop peers" lists the names of the others.
`

	layoutCannotBeUsed = "the layout has problems:\n%s\nThis machine is %q. \"lanhop peers\" lists the names of the others."

	notInTheLayout = "\nThis machine, %q, is not in the layout: add its name, so the edges of its screen lead somewhere.\n"

	edgesOff = `
Edge switching is off until a layout exists. Write one in %s
"lanhop layout" shows an example. The hotkeys work without it.
`
	edgesOn = `
Edge switching is on, with the layout in %s
Push the cursor through an edge of a screen to reach the machine on that side.
`
	edgesOnAwayFromHere = `
Edge switching is on between the machines this one controls, with the layout in %s
The edges of this machine's own screen do not cross, because %s.
Leave this machine with a hotkey. An edge or a hotkey brings the keyboard and mouse back.
`
	cursorNotFound    = "lanhop cannot find the cursor of this machine on this system yet"
	machineNotPlaced  = "the layout does not name this machine, %q"
	layoutGone        = "\nThe layout file %s is gone. Edge switching is off until a layout exists.\n"
	layoutHasProblems = "\nThe layout in %s has problems:\n%s\n%s\n"
	staysOff          = "Edge switching is off until they are fixed. The hotkeys work without it."
	keepsTheLast      = "lanhop keeps the layout it had."
)

func knownOnThisDesk(own string, trusted sharer.Trusted) func(name string) bool {
	return func(name string) bool {
		if name == own {
			return true
		}
		for _, peer := range trusted.Peers() {
			if peer.Name == name {
				return true
			}
		}
		return false
	}
}

func readLayout(path string, known func(name string) bool) (*layout.Layout, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return layout.Parse(file, known)
}

func indented(problems layout.Problems) string {
	return "  " + strings.ReplaceAll(problems.Error(), "\n", "\n  ")
}

func describedFor(own string, desk *layout.Layout) string {
	if !desk.Has(own) {
		return desk.Describe() + fmt.Sprintf(notInTheLayout, own)
	}
	lines := strings.Split(desk.Describe(), "\n")
	for i, line := range lines {
		if neighbours, isOwn := strings.CutPrefix(line, own+": "); isOwn {
			lines[i] = own + " (this machine): " + neighbours
		}
	}
	return strings.Join(lines, "\n")
}

func showLayout(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("layout", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	edit := flags.Bool("edit", false, "")
	if err := flags.Parse(args); errors.Is(err, flag.ErrHelp) {
		fmt.Fprint(stdout, usage)
		return nil
	} else if err != nil {
		return usageError(err.Error())
	}
	if flags.NArg() > 0 {
		return usageError("layout takes no argument besides --edit")
	}
	if *edit {
		interrupted, stopWaiting := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
		defer stopWaiting()
		return editLayoutUntil(interrupted, stdout)
	}
	directory, err := configDirectory()
	if err != nil {
		return err
	}
	trusted, err := openTrusted()
	if err != nil {
		return err
	}
	path := filepath.Join(directory, layoutFileName)
	own := pairing.OwnName()
	fmt.Fprintf(stdout, layoutFileIs, path)

	desk, err := readLayout(path, knownOnThisDesk(own, trusted))
	var problems layout.Problems
	switch {
	case errors.Is(err, fs.ErrNotExist):
		fmt.Fprintf(stdout, noLayoutFileYet, own)
		return nil
	case errors.As(err, &problems):
		return fmt.Errorf(layoutCannotBeUsed, indented(problems), own)
	case err != nil:
		return err
	}
	fmt.Fprint(stdout, describedFor(own, desk))
	return nil
}

type fileState struct {
	exists   bool
	modified int64
	size     int64
}

func stateOf(path string) fileState {
	info, err := os.Stat(path)
	if err != nil {
		return fileState{}
	}
	return fileState{exists: true, modified: info.ModTime().UnixNano(), size: info.Size()}
}

type deskOnDisk struct {
	path      string
	peersPath string
	own       string
	known     func(name string) bool
	ownEdges  bool
	stdout    io.Writer
	current   atomic.Pointer[layout.Layout]
	seen      [2]fileState
}

func (d *deskOnDisk) Layout() *layout.Layout {
	return d.current.Load()
}

func (d *deskOnDisk) refresh() {
	found := [2]fileState{stateOf(d.path), stateOf(d.peersPath)}
	if found == d.seen {
		return
	}
	d.seen = found
	previous := d.current.Load()
	desk, err := readLayout(d.path, d.known)
	var problems layout.Problems
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if d.current.Swap(nil) != nil {
			fmt.Fprintf(d.stdout, layoutGone, d.path)
		}
	case errors.As(err, &problems):
		fmt.Fprintf(d.stdout, layoutHasProblems, d.path, indented(problems), d.whatHappensWithoutIt(previous))
	case err != nil:
		fmt.Fprintf(d.stdout, layoutHasProblems, d.path, "  "+err.Error(), d.whatHappensWithoutIt(previous))
	default:
		d.current.Store(desk)
		if previous == nil || previous.Describe() != desk.Describe() {
			fmt.Fprint(d.stdout, d.howEdgesWorkWith(desk))
		}
	}
}

func (d *deskOnDisk) whatHappensWithoutIt(previous *layout.Layout) string {
	if previous == nil {
		return staysOff
	}
	return keepsTheLast
}

func (d *deskOnDisk) howEdgesWorkWith(desk *layout.Layout) string {
	switch {
	case !d.ownEdges:
		return fmt.Sprintf(edgesOnAwayFromHere, d.path, cursorNotFound)
	case !desk.Has(d.own):
		return fmt.Sprintf(edgesOnAwayFromHere, d.path, fmt.Sprintf(machineNotPlaced, d.own))
	}
	return fmt.Sprintf(edgesOn, d.path)
}

func (d *deskOnDisk) start() {
	d.refresh()
	if !d.seen[0].exists {
		fmt.Fprintf(d.stdout, edgesOff, d.path)
	}
}

func (d *deskOnDisk) follow(done context.Context, every time.Duration) {
	check := time.NewTicker(every)
	defer check.Stop()
	for {
		select {
		case <-done.Done():
			return
		case <-check.C:
			d.refresh()
		}
	}
}
