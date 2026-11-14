package macos

import (
	"errors"
	"fmt"
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
	readsLeft int
}

func (d *dragTracker) poll(source dragSource) (DragEvent, bool) {
	mark := source.board.Mark()
	held := source.leftHeld()
	changed := mark != d.mark
	d.mark = mark
	switch {
	case d.dragging && held:
		return DragEvent{}, false
	case d.dragging:
		d.dragging, d.readsLeft = false, 0
		return DragEvent{Kind: DragEnded}, true
	case !held:
		d.readsLeft = 0
		return DragEvent{}, false
	case changed:
		d.readsLeft = readsPerChange
	}
	if d.readsLeft == 0 {
		return DragEvent{}, false
	}
	d.readsLeft--
	paths, err := source.board.ReadFiles()
	switch {
	case err == nil:
		d.dragging, d.readsLeft = true, 0
		return DragEvent{Kind: DragStarted, Paths: paths}, true
	case !errors.Is(err, clipboard.ErrNotFiles):
		d.readsLeft = 0
	}
	return DragEvent{}, false
}

type DragWatch struct {
	events  chan DragEvent
	stop    chan struct{}
	stopped chan struct{}
	closing sync.Once
}

func watchDrags(source dragSource, interval time.Duration) (*DragWatch, error) {
	if interval < shortestDragPoll || interval > longestDragPoll {
		return nil, fmt.Errorf("%w: %v", ErrDragPollInterval, interval)
	}
	watch := &DragWatch{
		events:  make(chan DragEvent, dragEventQueue),
		stop:    make(chan struct{}),
		stopped: make(chan struct{}),
	}
	tracker := &dragTracker{mark: source.board.Mark()}
	go watch.run(source, tracker, interval)
	return watch, nil
}

func (w *DragWatch) run(source dragSource, tracker *dragTracker, interval time.Duration) {
	defer close(w.stopped)
	defer close(w.events)
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-w.stop:
			return
		case <-tick.C:
		}
		seen, happened := tracker.poll(source)
		if !happened {
			continue
		}
		select {
		case w.events <- seen:
		case <-w.stop:
			return
		}
	}
}

func (w *DragWatch) Events() <-chan DragEvent {
	return w.events
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
