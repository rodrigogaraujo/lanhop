package macos

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
)

const watchPatience = 2 * time.Second

type dragBoard struct {
	mu    sync.Mutex
	mark  int64
	paths []string
	err   error
	held  bool
	reads int
}

func newDragBoard() *dragBoard {
	return &dragBoard{mark: 700, err: clipboard.ErrNotFiles}
}

func (b *dragBoard) Mark() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.mark
}

func (b *dragBoard) ReadFiles() ([]string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.reads++
	if b.err != nil {
		return nil, b.err
	}
	return slices.Clone(b.paths), nil
}

func (b *dragBoard) leftHeld() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.held
}

func (b *dragBoard) source() dragSource {
	return dragSource{board: b, leftHeld: b.leftHeld}
}

func (b *dragBoard) tracker() *dragTracker {
	return &dragTracker{mark: b.Mark()}
}

func (b *dragBoard) press() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.held = true
}

func (b *dragBoard) release() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.held = false
}

func (b *dragBoard) change(paths []string, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.mark++
	b.paths, b.err = paths, err
}

func (b *dragBoard) holdFiles(paths ...string) {
	b.change(paths, nil)
}

func (b *dragBoard) holdNoFiles(err error) {
	b.change(nil, err)
}

func (b *dragBoard) finishWriting(paths ...string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.paths, b.err = paths, nil
}

func (b *dragBoard) readCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.reads
}

func pollTimes(tracker *dragTracker, source dragSource, times int) []DragEvent {
	var seen []DragEvent
	for range times {
		if event, happened := tracker.poll(source); happened {
			seen = append(seen, event)
		}
	}
	return seen
}

func expectDragEvents(t *testing.T, got []DragEvent, want ...DragEvent) {
	t.Helper()
	if !slices.EqualFunc(got, want, func(a, b DragEvent) bool { return a.Kind == b.Kind && slices.Equal(a.Paths, b.Paths) }) {
		t.Errorf("drag events %+v, want %+v", got, want)
	}
}

func startedWith(paths ...string) DragEvent {
	return DragEvent{Kind: DragStarted, Paths: paths}
}

var dragEnd = DragEvent{Kind: DragEnded}

func TestAChangeOfTheDragPasteboardWithTheLeftButtonHeldStartsADragOfItsFiles(t *testing.T) {
	board := newDragBoard()
	tracker := board.tracker()
	board.press()
	board.holdFiles("/Users/someone/report.pdf", "/Users/someone/photos")

	expectDragEvents(t, pollTimes(tracker, board.source(), 1), startedWith("/Users/someone/report.pdf", "/Users/someone/photos"))
}

func TestTheEndIsReportedOnceWhenTheLeftButtonComesUp(t *testing.T) {
	board := newDragBoard()
	tracker := board.tracker()
	board.press()
	board.holdFiles("/tmp/report.pdf")
	expectDragEvents(t, pollTimes(tracker, board.source(), 4), startedWith("/tmp/report.pdf"))

	board.release()

	expectDragEvents(t, pollTimes(tracker, board.source(), 4), dragEnd)
}

func TestChangesDuringADragAreNotReportedOrReadAgain(t *testing.T) {
	board := newDragBoard()
	tracker := board.tracker()
	board.press()
	board.holdFiles("/tmp/report.pdf")
	pollTimes(tracker, board.source(), 1)
	reads := board.readCount()

	board.holdFiles("/tmp/report.pdf")
	board.holdFiles("/tmp/other.pdf")

	expectDragEvents(t, pollTimes(tracker, board.source(), 3))
	if got := board.readCount(); got != reads {
		t.Errorf("the drag pasteboard was read %d more times during the drag", got-reads)
	}
}

func TestAChangeWithoutTheLeftButtonHeldIsNotADragAndIsNotRead(t *testing.T) {
	board := newDragBoard()
	tracker := board.tracker()
	board.holdFiles("/tmp/report.pdf")
	missed := pollTimes(tracker, board.source(), 2)

	board.press()

	expectDragEvents(t, append(missed, pollTimes(tracker, board.source(), 3)...))
	if got := board.readCount(); got != 0 {
		t.Errorf("the drag pasteboard was read %d times", got)
	}
}

func TestTheLeftButtonHeldWithoutAChangeIsNotADragAndIsNotRead(t *testing.T) {
	board := newDragBoard()
	tracker := board.tracker()
	board.press()

	expectDragEvents(t, pollTimes(tracker, board.source(), 5))
	if got := board.readCount(); got != 0 {
		t.Errorf("the drag pasteboard was read %d times", got)
	}
}

func TestFilesWrittenAfterTheChangeWasSeenStillStartTheDrag(t *testing.T) {
	board := newDragBoard()
	tracker := board.tracker()
	board.press()
	board.holdNoFiles(clipboard.ErrNotFiles)
	expectDragEvents(t, pollTimes(tracker, board.source(), 1))

	board.finishWriting("/tmp/report.pdf")

	expectDragEvents(t, pollTimes(tracker, board.source(), 1), startedWith("/tmp/report.pdf"))
}

func TestADragOfSomethingOtherThanFilesIsReadOnlyAFewTimes(t *testing.T) {
	board := newDragBoard()
	tracker := board.tracker()
	board.press()
	board.holdNoFiles(clipboard.ErrNotFiles)

	expectDragEvents(t, pollTimes(tracker, board.source(), 3*readsPerChange))
	if got := board.readCount(); got != readsPerChange {
		t.Errorf("the drag pasteboard was read %d times, want %d", got, readsPerChange)
	}
}

func TestADragOfPrivateFilesOrOfTooManyIsReadOnce(t *testing.T) {
	for _, refusal := range []error{clipboard.ErrPrivate, fmt.Errorf("%w: %d", clipboard.ErrTooManyFiles, clipboard.FilesAndFoldersLimit+1)} {
		t.Run(refusal.Error(), func(t *testing.T) {
			board := newDragBoard()
			tracker := board.tracker()
			board.press()
			board.holdNoFiles(refusal)

			expectDragEvents(t, pollTimes(tracker, board.source(), 3*readsPerChange))
			if got := board.readCount(); got != 1 {
				t.Errorf("the drag pasteboard was read %d times, want once", got)
			}
		})
	}
}

func TestANewDragAfterTheEndStartsWithItsOwnFiles(t *testing.T) {
	board := newDragBoard()
	tracker := board.tracker()
	board.press()
	board.holdFiles("/tmp/first.pdf")
	first := pollTimes(tracker, board.source(), 2)
	board.release()
	first = append(first, pollTimes(tracker, board.source(), 2)...)

	board.press()
	board.holdFiles("/tmp/second.pdf")

	expectDragEvents(t, append(first, pollTimes(tracker, board.source(), 2)...), startedWith("/tmp/first.pdf"), dragEnd, startedWith("/tmp/second.pdf"))
}

func TestAClickAfterADragDoesNotReadTheFilesOfThatDragAgain(t *testing.T) {
	board := newDragBoard()
	tracker := board.tracker()
	board.press()
	board.holdFiles("/tmp/report.pdf")
	pollTimes(tracker, board.source(), 1)
	board.holdFiles("/tmp/report.pdf")
	pollTimes(tracker, board.source(), 1)
	board.release()
	expectDragEvents(t, pollTimes(tracker, board.source(), 1), dragEnd)
	reads := board.readCount()

	board.press()

	expectDragEvents(t, pollTimes(tracker, board.source(), 3))
	if got := board.readCount(); got != reads {
		t.Errorf("a click read the drag pasteboard %d more times", got-reads)
	}
}

func nextDragEvent(t *testing.T, watch *DragWatch) DragEvent {
	t.Helper()
	select {
	case seen, open := <-watch.Events():
		if !open {
			t.Fatal("the watch stopped before the event")
		}
		return seen
	case <-time.After(watchPatience):
		t.Fatalf("no drag event within %v", watchPatience)
	}
	return DragEvent{}
}

func TestAWatchReportsADragUntilClosedAndThenClosesItsEvents(t *testing.T) {
	board := newDragBoard()
	watch, err := watchDrags(board.source(), shortestDragPoll)
	if err != nil {
		t.Fatalf("watchDrags: %v", err)
	}
	defer watch.Close()
	board.press()
	board.holdFiles("/tmp/report.pdf")
	started := nextDragEvent(t, watch)
	board.release()
	end := nextDragEvent(t, watch)

	watch.Close()

	expectDragEvents(t, []DragEvent{started, end}, startedWith("/tmp/report.pdf"), dragEnd)
	select {
	case seen, open := <-watch.Events():
		if open {
			t.Errorf("the closed watch still gave %+v", seen)
		}
	case <-time.After(watchPatience):
		t.Error("the events of a closed watch were not closed")
	}
}

type flickeringDrag struct {
	polls atomic.Int64
}

func (f *flickeringDrag) Mark() int64 {
	return f.polls.Add(1)
}

func (f *flickeringDrag) ReadFiles() ([]string, error) {
	return []string{"/tmp/report.pdf"}, nil
}

func (f *flickeringDrag) leftHeld() bool {
	return f.polls.Load()%2 == 1
}

func TestClosingAWatchThatNobodyReadsReturns(t *testing.T) {
	flicker := &flickeringDrag{}
	watch, err := watchDrags(dragSource{board: flicker, leftHeld: flicker.leftHeld}, shortestDragPoll)
	if err != nil {
		t.Fatalf("watchDrags: %v", err)
	}
	full := time.Now().Add(watchPatience)
	for len(watch.events) < dragEventQueue {
		if time.Now().After(full) {
			t.Fatalf("the queue of the watch holds %d events, want %d", len(watch.events), dragEventQueue)
		}
		time.Sleep(shortestDragPoll)
	}

	closed := make(chan struct{})
	go func() {
		watch.Close()
		close(closed)
	}()

	select {
	case <-closed:
	case <-time.After(watchPatience):
		t.Fatalf("Close did not return within %v while nobody read the events", watchPatience)
	}
}

func TestAWatchPollsAtMostEveryMillisecondAndAtLeastEverySecond(t *testing.T) {
	for _, interval := range []time.Duration{-time.Millisecond, 0, time.Millisecond - 1, time.Second + 1, time.Minute} {
		if watch, err := watchDrags(newDragBoard().source(), interval); !errors.Is(err, ErrDragPollInterval) {
			if watch != nil {
				watch.Close()
			}
			t.Errorf("watchDrags every %v: error = %v, want %v", interval, err, ErrDragPollInterval)
		}
	}
	for _, interval := range []time.Duration{time.Millisecond, DragPollInterval, time.Second} {
		watch, err := watchDrags(newDragBoard().source(), interval)
		if err != nil {
			t.Errorf("watchDrags every %v: %v", interval, err)
			continue
		}
		watch.Close()
	}
}

type endingScreen struct {
	held        bool
	cursor      Point
	cursorLater Point
	did         []any
}

func (s *endingScreen) ButtonDown(number int) bool {
	return number == leftMouseButton && s.held
}

func (s *endingScreen) Cursor() Point {
	return s.cursor
}

func (s *endingScreen) PostKey(key KeyPress) {
	s.did = append(s.did, key)
}

func (s *endingScreen) PostMouse(mouse MouseEvent) {
	s.did = append(s.did, mouse)
}

func (s *endingScreen) pause(gap time.Duration) {
	s.did = append(s.did, gap)
	s.cursor = s.cursorLater
}

func postingAllowed() bool {
	return true
}

func TestEndingASourceDragPostsEscapeThenALeftMouseUpWhereTheCursorIsAfterTheGap(t *testing.T) {
	screen := &endingScreen{held: true, cursor: Point{X: 501, Y: 165}, cursorLater: Point{X: 503, Y: 166}}

	err := endSourceDrag(screen, postingAllowed, screen.pause)

	if err != nil {
		t.Fatalf("endSourceDrag: %v", err)
	}
	want := []any{
		KeyPress{Code: 0x35, Down: true},
		KeyPress{Code: 0x35},
		20 * time.Millisecond,
		MouseEvent{Action: MouseUp, At: Point{X: 503, Y: 166}, Button: 0, Clicks: 1},
	}
	if !slices.Equal(screen.did, want) {
		t.Errorf("did %+v, want %+v", screen.did, want)
	}
}

func TestNothingIsPostedToEndADragWhenTheLeftButtonIsNotHeld(t *testing.T) {
	screen := &endingScreen{cursor: Point{X: 501, Y: 165}}

	err := endSourceDrag(screen, postingAllowed, screen.pause)

	if !errors.Is(err, ErrNoDragToEnd) || len(screen.did) != 0 {
		t.Errorf("endSourceDrag = %v after %+v, want %v and nothing posted", err, screen.did, ErrNoDragToEnd)
	}
}

func TestNothingIsPostedToEndADragWithoutThePermissionToPost(t *testing.T) {
	screen := &endingScreen{held: true, cursor: Point{X: 501, Y: 165}}

	err := endSourceDrag(screen, func() bool { return false }, screen.pause)

	if !errors.Is(err, ErrCannotEndDrag) || len(screen.did) != 0 {
		t.Errorf("endSourceDrag = %v after %+v, want %v and nothing posted", err, screen.did, ErrCannotEndDrag)
	}
}
