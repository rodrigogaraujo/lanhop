//go:build linux

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/evdev"
	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/x11"
	"github.com/rodrigogaraujo/lanhop/research/dragprobe/cli"
)

const (
	keyEscape         uint16 = 1
	virtualDeviceName        = "lanhop drag probe"
	longestGap               = time.Second
)

const usage = `usage: lanhop-dragprobe <watch|drop|cancel|grab> [flags]

  watch            print every change of the XdndSelection owner and what it offers
  drop <file>      act as an XDND source and drop <file> where button 1 is released,
                   only on a target that accepts XdndActionCopy
  cancel           when a drag starts, send Escape and a left button release through uinput,
                   only if X still reports button 1 held at that moment
  grab             grab the pointer and move it through uinput, printing the cursor before and after

Every subcommand takes -display (default: $DISPLAY).`

type command struct {
	name       string
	display    string
	runFor     time.Duration
	file       string
	now        bool
	verbose    bool
	grabWait   time.Duration
	statusWait time.Duration
	finishWait time.Duration
	timeout    time.Duration
	wait       time.Duration
	gap        time.Duration
	pressFirst bool
	ready      time.Duration
	after      time.Duration
	dx, dy     int
	settle     time.Duration
	baseline   bool
}

func parse(args []string) (command, error) {
	if len(args) == 0 {
		return command{}, errors.New(usage)
	}
	c := command{name: args[0]}
	set := flag.NewFlagSet(c.name, flag.ContinueOnError)
	set.SetOutput(io.Discard)
	set.StringVar(&c.display, "display", os.Getenv("DISPLAY"), "")
	switch c.name {
	case "watch":
		set.DurationVar(&c.runFor, "for", 0, "")
	case "drop":
		set.BoolVar(&c.now, "now", false, "")
		set.BoolVar(&c.verbose, "v", false, "")
		set.DurationVar(&c.grabWait, "grab-wait", 2*time.Second, "")
		set.DurationVar(&c.statusWait, "status-wait", 2*time.Second, "")
		set.DurationVar(&c.finishWait, "finish-wait", 10*time.Second, "")
		set.DurationVar(&c.timeout, "timeout", time.Minute, "")
	case "cancel":
		set.DurationVar(&c.wait, "wait", 800*time.Millisecond, "")
		set.DurationVar(&c.gap, "gap", 20*time.Millisecond, "")
		set.BoolVar(&c.pressFirst, "press-first", false, "")
		set.DurationVar(&c.ready, "ready", time.Second, "")
		set.DurationVar(&c.after, "after", 3*time.Second, "")
		set.DurationVar(&c.timeout, "timeout", 2*time.Minute, "")
	case "grab":
		set.IntVar(&c.dx, "dx", 100, "")
		set.IntVar(&c.dy, "dy", 50, "")
		set.DurationVar(&c.ready, "ready", time.Second, "")
		set.DurationVar(&c.settle, "settle", 300*time.Millisecond, "")
		set.BoolVar(&c.baseline, "baseline", true, "")
	default:
		return c, fmt.Errorf("unknown subcommand %q\n\n%s", c.name, usage)
	}
	positional, err := cli.ParseInterleaved(set, args[1:])
	if err != nil {
		return c, fmt.Errorf("%s: %w\n\n%s", c.name, err, usage)
	}
	switch {
	case c.name == "drop" && len(positional) != 1:
		return c, fmt.Errorf("drop needs exactly one file\n\n%s", usage)
	case c.name == "drop":
		absolute, err := filepath.Abs(positional[0])
		if err != nil {
			return c, err
		}
		c.file = absolute
	case len(positional) > 0:
		return c, fmt.Errorf("%s takes no file\n\n%s", c.name, usage)
	}
	if c.gap < 0 || c.gap > longestGap {
		return c, fmt.Errorf("-gap must be between 0 and %v", longestGap)
	}
	return c, nil
}

func main() {
	c, err := parse(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	p := cli.NewPrinter(os.Stdout)
	if err := run(ctx, c, p); err != nil {
		p.Say("error: %v", err)
		stop()
		os.Exit(1)
	}
}

func run(ctx context.Context, c command, p *cli.Printer) error {
	p.Say("lanhop-dragprobe %s on display %s, pid %d", c.name, c.display, os.Getpid())
	drags, err := x11.OpenDrags(c.display)
	if err != nil {
		return err
	}
	defer drags.Close()
	switch c.name {
	case "watch":
		return watch(ctx, c, drags, p)
	case "drop":
		return drop(ctx, c, drags, p)
	case "cancel":
		return endDrag(ctx, c, drags, p)
	default:
		return measureGrab(ctx, c, drags, p)
	}
}

func describeOwner(drags *x11.Drags, p *cli.Printer) {
	owner, err := drags.Owner()
	if err != nil {
		p.Say("XdndSelection owner: %v", err)
		return
	}
	p.Say("XdndSelection owner now: 0x%x", owner)
}

func describeOffer(offer x11.Offer, p *cli.Printer) {
	if offer.TargetsErr != nil {
		p.Say("TARGETS: %v", offer.TargetsErr)
	} else {
		p.Say("TARGETS (%d): %v", len(offer.Targets), offer.Targets)
	}
	if offer.URIListErr != nil {
		p.Say("text/uri-list: %v", offer.URIListErr)
		return
	}
	p.Say("text/uri-list (%d bytes, every address a file: %v):\n%s", len(offer.URIList), offer.AllFiles(), offer.URIList)
}

func watch(ctx context.Context, c command, drags *x11.Drags, p *cli.Printer) error {
	if err := drags.Watch(); err != nil {
		return err
	}
	describeOwner(drags, p)
	if c.runFor > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.runFor)
		defer cancel()
	}
	p.Say("watching XdndSelection through XFIXES; drag the throwaway file in Nautilus and drop it back where it was (Ctrl+C to stop)")
	for {
		select {
		case <-ctx.Done():
			p.Say("stopped")
			return nil
		case change := <-drags.OwnerChanges():
			p.Say("XdndSelection: %s, owner 0x%x, event time %d, owner since %d", change.Reason, change.Owner, change.Time, change.SelectionTime)
			if change.Reason != x11.NewOwner || change.Owner == 0 {
				continue
			}
			start := time.Now()
			offer := drags.ReadOffer()
			p.Say("read the offer of 0x%x in %.1f ms", change.Owner, time.Since(start).Seconds()*1000)
			describeOffer(offer, p)
		}
	}
}

func drop(ctx context.Context, c command, drags *x11.Drags, p *cli.Printer) error {
	if _, err := os.Stat(c.file); err != nil {
		return err
	}
	uri := x11.FileURI(c.file)
	p.Say("dropping %s", uri)
	if c.now {
		p.Say("-now: grabbing the pointer at once")
	} else {
		p.Say("the pointer is grabbed until the drop ends: press the left button anywhere, move over the scratch target folder in Nautilus, release (Ctrl+C to stop)")
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	report, err := drags.Drop(ctx, x11.DropOptions{
		URIs:         []string{uri},
		StartNow:     c.now,
		GrabPatience: c.grabWait,
		StatusWait:   c.statusWait,
		FinishWait:   c.finishWait,
		Verbose:      c.verbose,
		Say:          func(line string) { p.Say("%s", line) },
	})
	p.Say("pointer released")
	p.Say("outcome: %s", report.Outcome)
	p.Say("grab %s after %d tries; target 0x%x (messages to 0x%x), XDND version %d", report.Grab, report.GrabTries, report.Target, report.Destination, report.Version)
	p.Say("%d XdndEnter, %d XdndLeave, %d XdndPosition, %d XdndStatus; last status accepts %v with action %s", report.Enters, report.Leaves, report.Positions, report.Statuses, report.Accepted, report.Action)
	p.Say("dropped %v, finished %v (accepted %v, action %s, %.1f ms after XdndDrop)", report.Dropped, report.Finished, report.FinishAccepted, report.FinishAction, report.DropToFinish.Seconds()*1000)
	for _, line := range report.Served {
		p.Say("conversion: %s", line)
	}
	return err
}

func pointerLine(drags *x11.Drags) string {
	state, err := drags.Pointer()
	if err != nil {
		return fmt.Sprintf("pointer: %v", err)
	}
	return fmt.Sprintf("pointer at %d,%d, buttons held %v", state.X, state.Y, state.Held())
}

func readyDevice(ctx context.Context, ready time.Duration, p *cli.Printer) (*evdev.VirtualDevice, error) {
	device, err := evdev.CreateVirtualDevice(virtualDeviceName)
	if err != nil {
		return nil, fmt.Errorf("create the uinput device (needs write access to /dev/uinput): %w", err)
	}
	name, _ := device.SysName()
	p.Say("uinput device %q created (%s); waiting %v for the X server to take it", virtualDeviceName, name, ready)
	select {
	case <-time.After(ready):
	case <-ctx.Done():
		device.Close()
		return nil, ctx.Err()
	}
	return device, nil
}

func endDrag(ctx context.Context, c command, drags *x11.Drags, p *cli.Printer) error {
	if err := drags.Watch(); err != nil {
		return err
	}
	device, err := readyDevice(ctx, c.ready, p)
	if err != nil {
		return err
	}
	defer device.Close()
	describeOwner(drags, p)
	p.Say("start dragging the throwaway file in Nautilus, hold it still over the scratch target folder, and keep the button held; the probe acts %v after the drag starts", c.wait)
	waiting, stop := context.WithTimeout(ctx, c.timeout)
	defer stop()
	_, err = drags.EndDragAfter(waiting, c.wait, func(change x11.OwnerChange) {
		p.Say("drag seen: XdndSelection owned by 0x%x since server time %d", change.Owner, change.SelectionTime)
		p.Say("%s", pointerLine(drags))
	}, func() error {
		p.Say("before the keys: %s", pointerLine(drags))
		if err := device.Emit(event.Key(keyEscape, event.KeyPressed), event.Sync(), event.Key(keyEscape, event.KeyReleased), event.Sync()); err != nil {
			return err
		}
		p.Say("sent Escape (press and release) through uinput")
		time.Sleep(c.gap)
		release := []event.Event{event.Key(event.ButtonLeft, event.KeyReleased), event.Sync()}
		if c.pressFirst {
			release = append([]event.Event{event.Key(event.ButtonLeft, event.KeyPressed), event.Sync()}, release...)
		}
		if err := device.Emit(release...); err != nil {
			return err
		}
		if c.pressFirst {
			p.Say("sent a left button press then release through uinput")
		} else {
			p.Say("sent a left button release through uinput (the kernel drops it if this device never pressed the button; see -press-first)")
		}
		return nil
	})
	if errors.Is(err, x11.ErrFirstButtonNotHeld) {
		p.Say("%v when the probe was about to act, so it sent nothing", err)
		return nil
	}
	if err != nil {
		return err
	}
	return observe(ctx, drags, c.after, p)
}

func observe(ctx context.Context, drags *x11.Drags, period time.Duration, p *cli.Printer) error {
	p.Say("watching the owner and the buttons for %v; release the real button when you like and check that nothing was dropped", period)
	last := ""
	deadline := time.After(period)
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case change := <-drags.OwnerChanges():
			p.Say("XdndSelection: %s, owner 0x%x", change.Reason, change.Owner)
		case <-tick.C:
			if line := pointerLine(drags); line != last {
				p.Say("%s", line)
				last = line
			}
		case <-deadline:
			describeOwner(drags, p)
			return nil
		case <-ctx.Done():
			p.Say("stopped")
			return nil
		}
	}
}

func measureGrab(ctx context.Context, c command, drags *x11.Drags, p *cli.Printer) error {
	device, err := readyDevice(ctx, c.ready, p)
	if err != nil {
		return err
	}
	defer device.Close()
	move := func(dx, dy int) error {
		p.Say("moving %+d,%+d through uinput", dx, dy)
		return device.Emit(event.Relative(event.RelX, int32(dx)), event.Relative(event.RelY, int32(dy)), event.Sync())
	}
	p.Say("do not touch the mouse for a second")
	measure, err := drags.MeasureMoveWhileGrabbed(ctx, move, c.dx, c.dy, c.settle, c.baseline)
	if measure.Baseline {
		p.Say("without a grab: %d,%d -> %d,%d, moved %+d,%+d", measure.BaselineBefore.X, measure.BaselineBefore.Y, measure.BaselineAfter.X, measure.BaselineAfter.Y,
			measure.BaselineAfter.X-measure.BaselineBefore.X, measure.BaselineAfter.Y-measure.BaselineBefore.Y)
	}
	p.Say("grab status: %s", measure.Grab)
	if err != nil {
		return err
	}
	moved := measure.After.X != measure.Before.X || measure.After.Y != measure.Before.Y
	p.Say("with the grab: %d,%d -> %d,%d, moved %+d,%+d; %d motion events reached the grabbing program", measure.Before.X, measure.Before.Y, measure.After.X, measure.After.Y,
		measure.After.X-measure.Before.X, measure.After.Y-measure.Before.Y, measure.MotionEvents)
	p.Say("the cursor moved while lanhop held the grab: %v", moved)
	return nil
}
