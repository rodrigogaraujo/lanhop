//go:build darwin && cgo

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rodrigogaraujo/lanhop/research/dragprobe/cli"
)

const usage = `usage: open -n -W lanhop-dragprobe.app --args <watch|cancel|promise> [flags]

  watch            print every change of the drag pasteboard: types and file URLs
  cancel           when a drag starts, post Escape then a left mouse up at the cursor
  promise <file>   start a drag of a file promise from a small window, with posted events

Every subcommand takes -log (default: ~/Library/Logs/lanhop-dragprobe.log).`

const (
	mostSteps   = 200
	longestHold = 3 * time.Second
	longestGap  = time.Second
	longestWait = 10 * time.Second
)

const (
	operationNone    = 0
	operationCopy    = 1
	operationLink    = 2
	operationGeneric = 4
	operationPrivate = 8
	operationMove    = 16
	operationDelete  = 32
)

type point struct {
	x, y float64
}

type rect struct {
	x, y, width, height float64
}

func (r rect) contains(p point) bool {
	return p.x >= r.x && p.x < r.x+r.width && p.y >= r.y && p.y < r.y+r.height
}

func (r rect) clamp(p point) point {
	return point{min(max(p.x, r.x), r.x+r.width-1), min(max(p.y, r.y), r.y+r.height-1)}
}

type command struct {
	name    string
	logPath string
	every   time.Duration
	runFor  time.Duration
	wait    time.Duration
	gap     time.Duration
	after   time.Duration
	timeout time.Duration
	file    string
	dx, dy  float64
	to      *point
	steps   int
	hold    time.Duration
	manual  bool
}

func expandPath(path, home string) (string, error) {
	if path == "~" {
		return home, nil
	}
	if rest, found := strings.CutPrefix(path, "~/"); found {
		return filepath.Join(home, rest), nil
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%q is not an absolute path; under open the probe starts in /, so give the whole path", path)
	}
	return filepath.Clean(path), nil
}

func parsePoint(text string) (*point, error) {
	xText, yText, found := strings.Cut(text, ",")
	if !found {
		return nil, fmt.Errorf("-to %q is not x,y", text)
	}
	x, errX := strconv.ParseFloat(strings.TrimSpace(xText), 64)
	y, errY := strconv.ParseFloat(strings.TrimSpace(yText), 64)
	if errX != nil || errY != nil {
		return nil, fmt.Errorf("-to %q is not x,y", text)
	}
	return &point{x, y}, nil
}

func parseCommand(args []string, home string) (command, error) {
	if len(args) == 0 {
		return command{}, errors.New(usage)
	}
	c := command{name: args[0], logPath: filepath.Join(home, "Library", "Logs", "lanhop-dragprobe.log")}
	set := flag.NewFlagSet(c.name, flag.ContinueOnError)
	set.SetOutput(io.Discard)
	set.StringVar(&c.logPath, "log", c.logPath, "")
	var to string
	switch c.name {
	case "watch":
		set.DurationVar(&c.every, "every", 20*time.Millisecond, "")
		set.DurationVar(&c.runFor, "for", 5*time.Minute, "")
	case "cancel":
		set.DurationVar(&c.every, "every", 20*time.Millisecond, "")
		set.DurationVar(&c.wait, "wait", 800*time.Millisecond, "")
		set.DurationVar(&c.gap, "gap", 20*time.Millisecond, "")
		set.DurationVar(&c.after, "after", 3*time.Second, "")
		set.DurationVar(&c.timeout, "timeout", 2*time.Minute, "")
	case "promise":
		set.Float64Var(&c.dx, "dx", 300, "")
		set.Float64Var(&c.dy, "dy", 0, "")
		set.StringVar(&to, "to", "", "")
		set.IntVar(&c.steps, "steps", 30, "")
		set.DurationVar(&c.hold, "hold", 400*time.Millisecond, "")
		set.BoolVar(&c.manual, "manual", false, "")
		set.DurationVar(&c.timeout, "timeout", 0, "")
	default:
		return c, fmt.Errorf("unknown subcommand %q\n\n%s", c.name, usage)
	}
	positional, err := cli.ParseInterleaved(set, args[1:])
	if err != nil {
		return c, fmt.Errorf("%s: %w\n\n%s", c.name, err, usage)
	}
	if c.logPath, err = expandPath(c.logPath, home); err != nil {
		return c, err
	}
	if c.name != "promise" {
		if len(positional) > 0 {
			return c, fmt.Errorf("%s takes no file\n\n%s", c.name, usage)
		}
		if c.every <= 0 {
			return c, errors.New("-every must be above zero")
		}
		if c.gap < 0 || c.gap > longestGap || c.wait < 0 || c.wait > longestWait {
			return c, fmt.Errorf("-gap must be between 0 and %v, and -wait between 0 and %v", longestGap, longestWait)
		}
		return c, nil
	}
	if len(positional) != 1 {
		return c, fmt.Errorf("promise needs exactly one file\n\n%s", usage)
	}
	if c.file, err = expandPath(positional[0], home); err != nil {
		return c, err
	}
	if c.steps < 1 || c.steps > mostSteps {
		return c, fmt.Errorf("-steps must be between 1 and %d", mostSteps)
	}
	if c.hold < 0 || c.hold > longestHold {
		return c, fmt.Errorf("-hold must be between 0 and %v", longestHold)
	}
	if to != "" {
		if c.to, err = parsePoint(to); err != nil {
			return c, err
		}
	}
	if c.timeout <= 0 {
		c.timeout = 30 * time.Second
		if c.manual {
			c.timeout = 90 * time.Second
		}
	}
	return c, nil
}

func regularFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file (%s); promise drags one throwaway file, never a folder or a link", path, info.Mode().Type())
	}
	return nil
}

func dropTarget(start point, dx, dy float64, to *point, screens []rect) (point, bool) {
	target := point{start.x + dx, start.y + dy}
	if to != nil {
		target = *to
	}
	for _, screen := range screens {
		if screen.contains(target) {
			return target, false
		}
	}
	for _, screen := range screens {
		if screen.contains(start) {
			return screen.clamp(target), true
		}
	}
	if len(screens) > 0 {
		return screens[0].clamp(target), true
	}
	return target, false
}

func operationName(operation int) string {
	switch operation {
	case -1:
		return "no drag session ended"
	case -2:
		return "the file could not be read"
	case operationNone:
		return "none: nothing was dropped"
	}
	var names []string
	for _, known := range []struct {
		bit  int
		name string
	}{{operationCopy, "copy"}, {operationLink, "link"}, {operationGeneric, "generic"}, {operationPrivate, "private"}, {operationMove, "move"}, {operationDelete, "delete"}} {
		if operation&known.bit != 0 {
			names = append(names, known.name)
		}
	}
	return strings.Join(names, "+")
}
