//go:build darwin && cgo

package main

/*
#cgo LDFLAGS: -framework AppKit -framework ApplicationServices -framework UniformTypeIdentifiers
#include <stdlib.h>
#include "probe_darwin.h"
*/
import "C"

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/rodrigogaraujo/lanhop/research/dragprobe/cli"
)

const maxDisplays = 32

var printer = cli.NewPrinter(os.Stdout)

func init() {
	runtime.LockOSThread()
}

//export lanhopProbeSay
func lanhopProbeSay(line *C.char) {
	printer.Say("%s", C.GoString(line))
}

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	c, err := parseCommand(os.Args[1:], home)
	if err != nil {
		reportCommandLine(c.logPath, home, err)
		os.Exit(2)
	}
	logFile, err := openLog(c.logPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	printer = cli.NewPrinter(io.MultiWriter(os.Stdout, logFile))
	printer.Say("lanhop-dragprobe %s, pid %d, arguments %q", c.name, os.Getpid(), os.Args[1:])
	printer.Say("this output also goes to %s", c.logPath)
	printer.Say("%s", takeText(C.lanhopProbeIdentity()))
	go stopOnSignal()
	code := run(c)
	releasePendingDown()
	printer.Say("lanhop-dragprobe %s ended with exit code %d", c.name, code)
	logFile.Close()
	os.Exit(code)
}

func reportCommandLine(logPath, home string, problem error) {
	fmt.Fprintln(os.Stderr, problem)
	if !filepath.IsAbs(logPath) {
		logPath = filepath.Join(home, "Library", "Logs", "lanhop-dragprobe.log")
	}
	logFile, err := openLog(logPath)
	if err != nil {
		return
	}
	defer logFile.Close()
	cli.NewPrinter(logFile).Say("lanhop-dragprobe refused the arguments %q:\n%v", os.Args[1:], problem)
}

func openLog(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
}

func stopOnSignal() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	got := <-signals
	releasePendingDown()
	printer.Say("stopped by %v", got)
	os.Exit(1)
}

func releasePendingDown() {
	if bool(C.lanhopProbeReleasePendingDown()) {
		printer.Say("posted the pending left mouse up at the cursor")
	}
}

func run(c command) int {
	switch c.name {
	case "watch":
		return watch(c)
	case "cancel":
		return cancel(c)
	default:
		return promise(c)
	}
}

func takeText(text *C.char) string {
	if text == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(text))
	return C.GoString(text)
}

func changeCount() int64 {
	return int64(C.lanhopProbeDragChangeCount())
}

func describeDrag() string {
	return takeText(C.lanhopProbeDragDescription())
}

func leftHeld(includePosted bool) bool {
	return bool(C.lanhopProbeLeftButtonHeld(C.bool(includePosted)))
}

func cursor() point {
	var x, y C.double
	C.lanhopProbeCursor(&x, &y)
	return point{float64(x), float64(y)}
}

func displays() []rect {
	values := make([]C.double, maxDisplays*4)
	count := int(C.lanhopProbeDisplays(&values[0], maxDisplays))
	screens := make([]rect, count)
	for i := range screens {
		screens[i] = rect{float64(values[i*4]), float64(values[i*4+1]), float64(values[i*4+2]), float64(values[i*4+3])}
	}
	return screens
}

func mayPost() bool {
	trusted := bool(C.lanhopProbeIsTrusted())
	if bool(C.lanhopProbeCanPost()) {
		printer.Say("this app may post events (Accessibility trusted: %v)", trusted)
		return true
	}
	printer.Say("this app may not post events yet (Accessibility trusted: %v)", trusted)
	printer.Say("macOS should now ask. In System Settings > Privacy & Security > Accessibility, turn on lanhop-dragprobe, then run the probe again")
	C.lanhopProbeAskToPost()
	return false
}

func watch(c command) int {
	printer.Say("watching the drag pasteboard every %v for %v; drag the throwaway file in Finder and drop it back where it was", c.every, c.runFor)
	last := changeCount()
	printer.Say("drag pasteboard change count now: %d", last)
	deadline := time.Now().Add(c.runFor)
	tick := time.NewTicker(c.every)
	defer tick.Stop()
	for range tick.C {
		if time.Now().After(deadline) {
			printer.Say("done after %v", c.runFor)
			return 0
		}
		now := changeCount()
		if now == last {
			continue
		}
		last = now
		printer.Say("drag pasteboard changed: count %d, left button held: %v\n%s", now, leftHeld(false), describeDrag())
	}
	return 0
}

func cancel(c command) int {
	if !mayPost() {
		return 1
	}
	printer.Say("start dragging the throwaway file in Finder, hold it still over the scratch target folder, and keep the button held; the probe acts %v after it sees the drag", c.wait)
	last := changeCount()
	deadline := time.Now().Add(c.timeout)
	for {
		time.Sleep(c.every)
		if time.Now().After(deadline) {
			printer.Say("no drag seen within %v", c.timeout)
			return 1
		}
		now := changeCount()
		if now == last {
			continue
		}
		last = now
		if leftHeld(false) {
			break
		}
		printer.Say("the drag pasteboard changed to %d while no button was held; still waiting", now)
	}
	seen := time.Now()
	printer.Say("drag seen: count %d\n%s", last, describeDrag())
	time.Sleep(time.Until(seen.Add(c.wait)))
	if !leftHeld(false) {
		printer.Say("the button was released before the probe acted, so nothing was posted")
		return 1
	}
	C.lanhopProbePostEscape()
	printer.Say("posted Escape (key down and key up)")
	time.Sleep(c.gap)
	at := cursor()
	C.lanhopProbePostLeftMouse(C.LANHOP_PROBE_UP, C.double(at.x), C.double(at.y))
	printer.Say("posted a left mouse up at %.0f,%.0f", at.x, at.y)
	observeAfterCancel(c, last)
	return 0
}

func observeAfterCancel(c command, count int64) {
	printer.Say("watching for %v; release the real button when you like, then check that nothing was moved or copied", c.after)
	state := func() string {
		return fmt.Sprintf("left button held: %v by the hardware, %v counting posted events; drag pasteboard count %d", leftHeld(false), leftHeld(true), changeCount())
	}
	last := state()
	printer.Say("%s", last)
	deadline := time.Now().Add(c.after)
	for time.Now().Before(deadline) {
		time.Sleep(c.every)
		if now := state(); now != last {
			printer.Say("%s", now)
			last = now
		}
	}
	if changeCount() != count {
		printer.Say("drag pasteboard now:\n%s", describeDrag())
	}
}

func promise(c command) int {
	if err := regularFile(c.file); err != nil {
		printer.Say("%v", err)
		return 1
	}
	if !c.manual && !mayPost() {
		return 1
	}
	start := cursor()
	screens := displays()
	target, clamped := dropTarget(start, c.dx, c.dy, c.to, screens)
	var layout []string
	for _, screen := range screens {
		layout = append(layout, fmt.Sprintf("%.0fx%.0f at %.0f,%.0f", screen.width, screen.height, screen.x, screen.y))
	}
	printer.Say("displays: %s", strings.Join(layout, "; "))
	if !c.manual {
		printer.Say("cursor at %.0f,%.0f; the posted drag goes to %.0f,%.0f (display points, origin at the top left of the main display)", start.x, start.y, target.x, target.y)
		if clamped {
			printer.Say("the drop point was off every screen, so it was moved onto the screen of the cursor")
		}
		printer.Say("do not touch the mouse until the probe ends")
	}
	path := C.CString(c.file)
	defer C.free(unsafe.Pointer(path))
	operation := int(C.lanhopProbePromise(path, C.double(target.x), C.double(target.y), C.bool(c.manual), C.int(c.steps), C.double(c.hold.Seconds()), C.double(c.timeout.Seconds())))
	printer.Say("end operation of the drag session: %d (%s)", operation, operationName(operation))
	if operation < 0 {
		return 1
	}
	return 0
}
