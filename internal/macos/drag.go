package macos

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
)

const (
	DragPollInterval = 20 * time.Millisecond
	shortestDragPoll = time.Millisecond
	longestDragPoll  = time.Second
	readsPerChange   = 5
	dragEventQueue   = 8

	escapeKeyCode   uint16 = 0x35
	leftMouseButton        = 0
	escapeToRelease        = 20 * time.Millisecond
)

var (
	ErrDragPollInterval = errors.New("macos: a drag watch polls at most every millisecond and at least every second")
	ErrNoDragToEnd      = errors.New("macos: the left button is not held, so there is no drag to end and nothing was posted")
	ErrCannotEndDrag    = errors.New("macos: this program is not allowed to post the events that end a drag, so nothing was posted")
)

type DragKind int

const (
	DragStarted DragKind = iota + 1
	DragEnded
)

type DragEvent struct {
	Kind  DragKind
	Paths []string
}

type filesBoard interface {
	Mark() int64
	ReadFiles() ([]string, error)
}

type dragSource struct {
	board    filesBoard
	leftHeld func() bool
}

type dragTracker struct {
	mark      int64
	dragging  bool
	paths     []string
	candidate []string
	readsLeft int
}

func (d *dragTracker) poll(source dragSource) []DragEvent {
	mark := source.board.Mark()
	held := source.leftHeld()
	changed := mark != d.mark
	d.mark = mark
	if !held {
		d.readsLeft, d.candidate = 0, nil
		if !d.dragging {
			return nil
		}
		d.dragging, d.paths = false, nil
		return []DragEvent{{Kind: DragEnded}}
	}
	if changed {
		d.readsLeft, d.candidate = readsPerChange, nil
	}
	if d.readsLeft == 0 {
		return nil
	}
	d.readsLeft--
	paths, err := source.board.ReadFiles()
	if d.dragging && err == nil && slices.Equal(paths, d.paths) {
		d.readsLeft, d.candidate = 0, nil
		return nil
	}
	var seen []DragEvent
	if d.dragging {
		d.dragging, d.paths = false, nil
		seen = append(seen, DragEvent{Kind: DragEnded})
	}
	switch {
	case err == nil && d.candidate != nil && slices.Equal(paths, d.candidate):
		d.dragging, d.paths, d.candidate, d.readsLeft = true, paths, nil, 0
		seen = append(seen, DragEvent{Kind: DragStarted, Paths: slices.Clone(paths)})
	case err == nil:
		d.candidate = paths
	case errors.Is(err, clipboard.ErrNotFiles):
		d.candidate = nil
	default:
		d.candidate, d.readsLeft = nil, 0
	}
	return seen
}

func (d *dragTracker) reset(mark int64) {
	*d = dragTracker{mark: mark}
}

type DragWatch struct {
	source  dragSource
	tracker dragTracker
	events  chan DragEvent
	resets  chan struct{}
	stop    chan struct{}
	stopped chan struct{}
	closing sync.Once
}

func watchDrags(source dragSource, interval time.Duration) (*DragWatch, error) {
	if interval < shortestDragPoll || interval > longestDragPoll {
		return nil, fmt.Errorf("%w: %v", ErrDragPollInterval, interval)
	}
	watch := &DragWatch{
		source:  source,
		tracker: dragTracker{mark: source.board.Mark()},
		events:  make(chan DragEvent),
		resets:  make(chan struct{}),
		stop:    make(chan struct{}),
		stopped: make(chan struct{}),
	}
	go watch.run(interval)
	return watch, nil
}

func (w *DragWatch) run(interval time.Duration) {
	defer close(w.stopped)
	defer close(w.events)
	tick := time.NewTicker(interval)
	defer tick.Stop()
	var pending []DragEvent
	for {
		var send chan<- DragEvent
		var next DragEvent
		if len(pending) > 0 {
			send, next = w.events, pending[0]
		}
		select {
		case <-w.stop:
			return
		case <-w.resets:
			pending = nil
			w.tracker.reset(w.source.board.Mark())
		case send <- next:
			pending = pending[1:]
		case <-tick.C:
			if len(pending) < dragEventQueue {
				pending = append(pending, w.tracker.poll(w.source)...)
			}
		}
	}
}

func (w *DragWatch) Events() <-chan DragEvent {
	return w.events
}

func (w *DragWatch) Reset() {
	select {
	case w.resets <- struct{}{}:
	case <-w.stopped:
	}
}

func (w *DragWatch) Close() error {
	w.closing.Do(func() {
		close(w.stop)
		<-w.stopped
	})
	return nil
}

type dragEnder interface {
	ButtonDown(number int) bool
	Cursor() Point
	PostKey(key KeyPress)
	PostMouse(mouse MouseEvent)
}

var endingSourceDrag sync.Mutex

func endSourceDrag(screen dragEnder, canPost func() bool, pause func(time.Duration)) error {
	endingSourceDrag.Lock()
	defer endingSourceDrag.Unlock()
	if !canPost() {
		return ErrCannotEndDrag
	}
	if !screen.ButtonDown(leftMouseButton) {
		return ErrNoDragToEnd
	}
	screen.PostKey(KeyPress{Code: escapeKeyCode, Down: true})
	screen.PostKey(KeyPress{Code: escapeKeyCode})
	pause(escapeToRelease)
	screen.PostMouse(MouseEvent{Action: MouseUp, At: screen.Cursor(), Button: leftMouseButton, Clicks: 1})
	return nil
}
