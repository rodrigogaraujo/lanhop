//go:build linux

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/clipshare"
	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/x11"
)

const sessionLookupLimit = 2 * time.Second

var (
	errWaylandSession   = errors.New("this machine runs a Wayland session, where lanhop cannot start or see a drag")
	errSourceDragStays  = errors.New("lanhop cannot end a file drag of this machine on its source")
	errNoEdgeWithoutX11 = errors.New("lanhop cannot reach the X server to catch the drag at the edge")
)

func openDropsFor(central bool, hand *dropReplay, log io.Writer) (clipshare.Drops, func(), error) {
	if central {
		return nil, func() {}, nil
	}
	if waylandSession() {
		return nil, func() {}, errWaylandSession
	}
	drags, release, err := sharedDrags.hold()
	if err != nil {
		return nil, func() {}, err
	}
	drops := newXdndDrops(func() (xdndDrag, error) {
		drag, err := drags.Start()
		if err != nil {
			return nil, err
		}
		return drag, nil
	}, hand, xdndDropHappened, log)
	return drops, func() {
		drops.close()
		release()
	}, nil
}

func xdndDropHappened(err error, accepted, filesGiven bool) bool {
	switch {
	case err == nil, errors.Is(err, x11.ErrNotFinished), errors.Is(err, x11.ErrDropFailed):
		return true
	case errors.Is(err, x11.ErrClosed):
		return accepted && filesGiven
	}
	return false
}

func watchLocalDrags(ctx context.Context) (<-chan localDrag, func(), error) {
	if waylandSession() {
		return nil, func() {}, errWaylandSession
	}
	drags, release, err := sharedDrags.hold()
	if err != nil {
		return nil, func() {}, err
	}
	watch, err := drags.WatchDrags()
	if err != nil {
		release()
		return nil, func() {}, err
	}
	seen, stop := forwardDrags(ctx, watch.Events(), func(drag x11.DragEvent) localDrag {
		return localDrag{Started: drag.Kind == x11.DragStarted, Paths: drag.Paths}
	}, func() {
		watch.Close()
		release()
	})
	return seen, stop, nil
}

func resetLocalDrags() {}

func endSourceDragHere() error {
	return errSourceDragStays
}

func dragLeavesHandler(hand *dropReplay, log io.Writer, stayed func()) func(context.Context) {
	return func(ctx context.Context) {
		drags, release, err := sharedDrags.hold()
		if err != nil {
			fallBackAtTheEdge(replayHand{hand}, log, stayed, errors.Join(errNoEdgeWithoutX11, err))
			return
		}
		defer release()
		_, err = drags.EndDragAtEdge(ctx, replayHand{hand})
		switch {
		case errors.Is(err, x11.ErrDragStayed):
			reportTheDragStayed(log, stayed, err)
		case err != nil:
			fmt.Fprintf(log, "The file drag that left this machine was released over the window of lanhop at the edge, and then: %s\n", forTheLog(err))
		}
	}
}

type replayHand struct {
	hand *dropReplay
}

func (r replayHand) Nudge(dx, dy int) error {
	var moves []event.Event
	if dx != 0 {
		moves = append(moves, event.Relative(event.RelX, int32(dx)))
	}
	if dy != 0 {
		moves = append(moves, event.Relative(event.RelY, int32(dy)))
	}
	return r.hand.emitForLanhop(append(moves, event.Sync())...)
}

func (r replayHand) Release() error {
	return r.hand.emitForLanhop(event.Key(event.ButtonLeft, event.KeyReleased), event.Sync())
}

func (r replayHand) Escape() error {
	return r.hand.emitForLanhop(event.Key(keyEscape, event.KeyPressed), event.Sync(), event.Key(keyEscape, event.KeyReleased), event.Sync())
}

type dragsHolder struct {
	mutex   sync.Mutex
	open    func() (*x11.Drags, error)
	drags   *x11.Drags
	holders int
}

var sharedDrags = &dragsHolder{open: x11.OpenDrags}

func (h *dragsHolder) hold() (*x11.Drags, func(), error) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	if h.drags == nil {
		drags, err := h.open()
		if err != nil {
			return nil, func() {}, err
		}
		h.drags = drags
	}
	h.holders++
	var releasing sync.Once
	return h.drags, func() { releasing.Do(h.release) }, nil
}

func (h *dragsHolder) release() {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	h.holders--
	if h.holders == 0 && h.drags != nil {
		h.drags.Close()
		h.drags = nil
	}
}

func waylandSession() bool {
	if os.Getenv("WAYLAND_DISPLAY") != "" || strings.EqualFold(os.Getenv("XDG_SESSION_TYPE"), "wayland") {
		return true
	}
	if os.Getenv("DISPLAY") != "" {
		return false
	}
	asking, cancel := context.WithTimeout(context.Background(), sessionLookupLimit)
	defer cancel()
	environment, err := exec.CommandContext(asking, "systemctl", "--user", "show-environment").Output()
	return err == nil && saysWayland(environment)
}

func saysWayland(environment []byte) bool {
	for _, line := range bytes.Split(environment, []byte("\n")) {
		name, value, found := strings.Cut(strings.TrimSpace(string(line)), "=")
		if !found || value == "" {
			continue
		}
		if name == "WAYLAND_DISPLAY" || (name == "XDG_SESSION_TYPE" && strings.EqualFold(value, "wayland")) {
			return true
		}
	}
	return false
}
