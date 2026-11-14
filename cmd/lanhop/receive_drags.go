package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/edge"
)

const dockPollInterval = 30 * time.Second

type edgeHand interface {
	Nudge(dx, dy int) error
	Release() error
	Escape() error
}

func fallBackAtTheEdge(hand edgeHand, log io.Writer, stayed func(), why error) {
	hand.Escape()
	hand.Release()
	reportTheDragStayed(log, stayed, why)
}

func reportTheDragStayed(log io.Writer, stayed func(), why error) {
	fmt.Fprintf(log, "The file drag that left this machine did not reach the window of lanhop at the edge, so lanhop pressed Escape and released the button here, and asked the machine with the keyboard to cancel the drop: %s\n", forTheLog(why))
	if stayed != nil {
		stayed()
	}
}

type cursorShield func(ctx context.Context) (io.Closer, error)

type sourceDragEnd func(under io.Closer) error

func endTheSourceDragUnderAShield(ctx context.Context, shield cursorShield, end sourceDragEnd, hand *dropReplay, log io.Writer, stayed func()) error {
	guard, err := shield(ctx)
	if err != nil {
		fmt.Fprintf(log, "lanhop could not put its window under the cursor before it ended the file drag that left this machine, and ends it anyway: %s\n", forTheLog(err))
	}
	ended := end(guard)
	switch {
	case ended != nil && stayed != nil:
		fmt.Fprintf(log, "The file drag that left this machine could not be ended here, so lanhop asks the machine with the keyboard to cancel the drop: %s\n", forTheLog(ended))
		stayed()
	case ended != nil:
		fmt.Fprintf(log, "The file drag that left this machine could not be ended here: %s\n", forTheLog(ended))
	}
	if guard != nil {
		if err := guard.Close(); err != nil {
			fmt.Fprintf(log, "The window of lanhop under the cursor did not close: %s\n", forTheLog(err))
		}
	}
	hand.forgetTheLeftButton()
	return ended
}

type dragDescriber interface {
	DragSeen(paths []string, dock edge.Side)
	DragGone()
	DockIs(dock edge.Side)
}

type localDragFollower struct {
	side    dragDescriber
	watch   func(context.Context) (<-chan localDrag, func(), error)
	dock    func() edge.Side
	every   time.Duration
	log     io.Writer
	changes chan bool
}

func newLocalDragFollower(side dragDescriber, log io.Writer) *localDragFollower {
	return &localDragFollower{side: side, watch: watchLocalDrags, dock: readDock, every: dockPollInterval, log: log, changes: make(chan bool, 1)}
}

func (f *localDragFollower) describing(on bool) {
	for {
		select {
		case f.changes <- on:
			return
		default:
		}
		select {
		case <-f.changes:
		default:
		}
	}
}

type localDragState struct {
	wanted    bool
	drags     <-chan localDrag
	stopWatch func()
	ticker    *time.Ticker
	dock      edge.Side
	dragging  bool
	failure   string
}

func (f *localDragFollower) run(ctx context.Context) {
	var state localDragState
	defer f.stop(&state)
	for {
		var poll <-chan time.Time
		if state.ticker != nil {
			poll = state.ticker.C
		}
		select {
		case <-ctx.Done():
			return
		case on := <-f.changes:
			if !on {
				f.stop(&state)
				continue
			}
			if state.wanted {
				continue
			}
			state.wanted = true
			state.ticker = time.NewTicker(f.every)
			f.readTheDock(&state)
			f.startWatching(ctx, &state)
		case <-poll:
			f.readTheDock(&state)
			if state.drags == nil {
				f.startWatching(ctx, &state)
			}
		case drag, open := <-state.drags:
			if !open {
				state.drags = nil
				f.endTheDrag(&state)
				continue
			}
			if drag.Started {
				state.dragging = true
				f.side.DragSeen(drag.Paths, state.dock)
				continue
			}
			f.endTheDrag(&state)
		}
	}
}

func (f *localDragFollower) readTheDock(state *localDragState) {
	state.dock = f.dock()
	f.side.DockIs(state.dock)
}

func (f *localDragFollower) startWatching(ctx context.Context, state *localDragState) {
	drags, stop, err := f.watch(ctx)
	if err != nil {
		if failure := forTheLog(err); failure != state.failure {
			state.failure = failure
			fmt.Fprintf(f.log, "lanhop cannot see a file dragged on this machine, so such a drag stays here: %s\n", failure)
		}
		stop()
		return
	}
	state.failure = ""
	state.drags, state.stopWatch = drags, stop
}

func (f *localDragFollower) endTheDrag(state *localDragState) {
	if state.dragging {
		state.dragging = false
		f.side.DragGone()
	}
}

func (f *localDragFollower) stop(state *localDragState) {
	state.wanted = false
	if state.stopWatch != nil {
		state.stopWatch()
		state.stopWatch = nil
	}
	state.drags = nil
	if state.ticker != nil {
		state.ticker.Stop()
		state.ticker = nil
	}
	f.endTheDrag(state)
}
