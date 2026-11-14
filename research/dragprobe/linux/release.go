//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/evdev"
	"github.com/rodrigogaraujo/lanhop/internal/x11"
	"github.com/rodrigogaraujo/lanhop/research/dragprobe/cli"
)

const (
	busVirtual             = 0x06
	parentlessInputDevices = "/sys/devices/virtual/input/"

	notAPointer    = "not a pointer with BTN_LEFT and REL_X"
	ownDevice      = "a virtual device of lanhop"
	virtualDevice  = "a uinput device or another device with no hardware behind it"
	unknownSysPath = "no sysfs path, so it may be virtual"
	alsoKeyboard   = "also a keyboard, and the probe never writes to a keyboard"
	notWritable    = "not writable by this user"
	leftUp         = "BTN_LEFT is up"
)

var (
	errNoPointer = errors.New("no physical pointer with BTN_LEFT and REL_X was found, so release-real cannot act")
	errNoAccess  = errors.New("no write access to the event nodes of the physical pointers: the input group gives it; check `ls -l /dev/input/event*` and `id`, and log in again after joining the group (do not use sudo)")
	errNoneHeld  = errors.New("refused: no physical pointer has BTN_LEFT down at this moment, so nothing was written")
)

type judged struct {
	node    evdev.Node
	refusal string
}

func refusal(node evdev.Node) string {
	switch {
	case node.Err != nil:
		return fmt.Sprintf("not read: %v", node.Err)
	case !node.LeftButton || !node.RelativeX:
		return notAPointer
	case node.Own:
		return ownDevice
	case node.Bus == busVirtual || strings.HasPrefix(node.SysPath, parentlessInputDevices):
		return virtualDevice
	case node.SysPath == "":
		return unknownSysPath
	case node.Kind&evdev.Keyboard != 0:
		return alsoKeyboard
	case !node.Writable:
		return notWritable
	case !node.LeftDown:
		return leftUp
	}
	return ""
}

func judge(nodes []evdev.Node) []judged {
	verdicts := make([]judged, 0, len(nodes))
	for _, node := range nodes {
		verdicts = append(verdicts, judged{node: node, refusal: refusal(node)})
	}
	return verdicts
}

func chosen(verdicts []judged) []evdev.Node {
	var nodes []evdev.Node
	for _, verdict := range verdicts {
		if verdict.refusal == "" {
			nodes = append(nodes, verdict.node)
		}
	}
	return nodes
}

func preflight(verdicts []judged) ([]evdev.Node, error) {
	var usable []evdev.Node
	denied := false
	for _, verdict := range verdicts {
		switch {
		case verdict.refusal == "" || verdict.refusal == leftUp:
			usable = append(usable, verdict.node)
		case verdict.refusal == notWritable || errors.Is(verdict.node.Err, fs.ErrPermission):
			denied = true
		}
	}
	switch {
	case len(usable) > 0:
		return usable, nil
	case denied:
		return nil, errNoAccess
	}
	return nil, errNoPointer
}

func nodeLine(node evdev.Node, verdict string) string {
	return fmt.Sprintf("%s %q (bus 0x%02x, %04x:%04x, %s): %s", node.Path, node.Name, node.Bus, node.Vendor, node.Product, node.SysPath, verdict)
}

func sayPointers(verdicts []judged, usable func(judged) string, p *cli.Printer) {
	others := 0
	for _, verdict := range verdicts {
		if verdict.refusal == notAPointer {
			others++
			continue
		}
		p.Say("%s", nodeLine(verdict.node, usable(verdict)))
	}
	p.Say("%d other event nodes are not pointers with BTN_LEFT and REL_X", others)
}

func atStart(verdict judged) string {
	if verdict.refusal == "" || verdict.refusal == leftUp {
		return "usable: a physical pointer this user can write to"
	}
	return "skipped: " + verdict.refusal
}

func atAction(verdict judged) string {
	if verdict.refusal == "" {
		return "chosen: a physical pointer with BTN_LEFT down"
	}
	return "skipped: " + verdict.refusal
}

func allowedToWrite(fresh evdev.Node) error {
	if reason := refusal(fresh); reason != "" {
		return errors.New(reason)
	}
	return nil
}

func releaseReal(ctx context.Context, c command, drags *x11.Drags, p *cli.Printer) error {
	verdicts := judge(evdev.Nodes())
	sayPointers(verdicts, atStart, p)
	if _, err := preflight(verdicts); err != nil {
		return err
	}
	if err := drags.Watch(); err != nil {
		return err
	}
	var drops <-chan edgeResult
	if c.withEdge {
		strip, err := openEdge(ctx, c, p)
		if err != nil {
			return err
		}
		defer strip.close(p)
		drops = strip.drops
	}
	describeOwner(drags, p)
	if c.withEdge {
		p.Say("start dragging the throwaway file in Nautilus, push the pointer against the %s edge of the screen, hold it there and keep the button held; %v after the drag starts the probe writes one left button release into the event node of the mouse", c.edge, c.wait)
	} else {
		p.Say("start dragging the throwaway file in Nautilus, hold it still over an empty part of the Nautilus window of ~/drag-scratch, never over a folder, and keep the button held; %v after the drag starts the probe writes one left button release into the event node of the mouse", c.wait)
	}
	waiting, stop := context.WithTimeout(ctx, c.timeout)
	defer stop()
	change, err := drags.WaitForDragStart(waiting)
	if err != nil {
		return stoppedBecause(ctx, err, c.timeout, "drag", p)
	}
	p.Say("drag seen: XdndSelection owned by 0x%x since server time %d", change.Owner, change.SelectionTime)
	p.Say("%s", pointerLine(drags))
	timer := time.NewTimer(time.Until(change.At.Add(c.wait)))
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		p.Say("stopped before writing anything")
		return nil
	}
	written, err := releaseHeldPointers(drags, p)
	if !written {
		return err
	}
	if err != nil {
		p.Say("%v", err)
	}
	p.Say("keep the button held until the probe says \"after %v\", then release it and check the scratch folder", c.after)
	dropped, err := observe(ctx, drags, c.after, drops, p)
	if c.withEdge && !dropped && err == nil {
		p.Say("the edge took no drop")
	}
	return err
}

func releaseHeldPointers(drags *x11.Drags, p *cli.Printer) (bool, error) {
	state, err := drags.Pointer()
	if err != nil {
		return false, err
	}
	owner, err := drags.Owner()
	if err != nil {
		return false, err
	}
	if !slices.Contains(state.Held(), 1) {
		p.Say("X does not report button 1 held (buttons held: %v), so the probe wrote nothing", state.Held())
		return false, nil
	}
	if owner == 0 {
		p.Say("XdndSelection has no owner any more, so no drag is in progress and the probe wrote nothing")
		return false, nil
	}
	verdicts := judge(evdev.Nodes())
	targets := chosen(verdicts)
	if len(targets) == 0 {
		sayPointers(verdicts, atAction, p)
		return false, errNoneHeld
	}
	p.Say("before the write: pointer at %d,%d, button mask 0x%04x, buttons held %v; XdndSelection owner 0x%x", state.X, state.Y, state.Mask, state.Held(), owner)
	written := false
	for _, target := range targets {
		after, err := evdev.ReleaseLeft(target.Path, allowedToWrite)
		if err != nil {
			return written, fmt.Errorf("wrote nothing to %s (%q): %w", target.Path, target.Name, err)
		}
		written = true
		p.Say("wrote EV_KEY BTN_LEFT 0 and EV_SYN SYN_REPORT to %s (%q, bus 0x%02x, %04x:%04x); BTN_LEFT down in the kernel afterwards: %v", after.Path, after.Name, after.Bus, after.Vendor, after.Product, after.LeftDown)
	}
	describeAfterWrite(drags, p)
	return written, nil
}

func describeAfterWrite(drags *x11.Drags, p *cli.Printer) {
	state, err := drags.Pointer()
	if err != nil {
		p.Say("right after the write: pointer: %v", err)
		return
	}
	owner, err := drags.Owner()
	if err != nil {
		p.Say("right after the write: XdndSelection owner: %v", err)
		return
	}
	p.Say("right after the write: button mask 0x%04x, buttons held %v; XdndSelection owner 0x%x", state.Mask, state.Held(), owner)
}
