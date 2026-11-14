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

const (
	watchPatience = 2 * time.Second
	quietWindow   = 20 * time.Millisecond
)

type dragBoard struct {
	mu    sync.Mutex
	mark  int64
	paths []string
	err   error
	held  bool
	reads int
	marks int
}

func newDragBoard() *dragBoard {
	return &dragBoard{mark: 700, err: clipboard.ErrNotFiles}
}

func (b *dragBoard) Mark() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.marks++
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

func (b *dragBoard) finishWritingNoFiles(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.paths, b.err = nil, err
}

func (b *dragBoard) readCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.reads
}

func (b *dragBoard) markCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.marks
}

func waitForPolls(t *testing.T, board *dragBoard, polls int) {
	t.Helper()
	until := board.markCount() + polls
	deadline := time.Now().Add(watchPatience)
	for board.markCount() < until {
		if time.Now().After(deadline) {
			t.Fatalf("the watch did not poll %d times within %v", polls, watchPatience)
		}
		time.Sleep(shortestDragPoll)
	}
}

func pollTimes(tracker *dragTracker, source dragSource, times int) []DragEvent {
	var seen []DragEvent
	for range times {
		seen = append(seen, tracker.poll(source)...)
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

	expectDragEvents(t, pollTimes(tracker, board.source(), 2), startedWith("/Users/someone/report.pdf", "/Users/someone/photos"))
}

func TestADragStartsOnlyOnceTwoReadsInARowGiveTheSameFiles(t *testing.T) {
	board := newDragBoard()
	tracker := board.tracker()
	board.press()
	board.holdFiles("/tmp/a.txt")
	partial := pollTimes(tracker, board.source(), 1)

	board.finishWriting("/tmp/a.txt", "/tmp/b.txt")

	expectDragEvents(t, append(partial, pollTimes(tracker, board.source(), 3)...), startedWith("/tmp/a.txt", "/tmp/b.txt"))
	if got := board.readCount(); got != 3 {
		t.Errorf("the drag pasteboard was read %d times, want 3", got)
	}
}

func TestFilesThatTurnIntoACopyWithoutFilesStartNoDrag(t *testing.T) {
	board := newDragBoard()
	tracker := board.tracker()
	board.press()
	board.holdFiles("/tmp/report.pdf")
	partial := pollTimes(tracker, board.source(), 1)

	board.finishWritingNoFiles(clipboard.ErrNotFiles)

	expectDragEvents(t, append(partial, pollTimes(tracker, board.source(), 6)...))
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

func TestAChangeDuringADragWithTheSameFilesIsReadOnceAndReportsNothing(t *testing.T) {
	board := newDragBoard()
	tracker := board.tracker()
	board.press()
	board.holdFiles("/tmp/report.pdf")
	pollTimes(tracker, board.source(), 2)

	board.holdFiles("/tmp/report.pdf")

	expectDragEvents(t, pollTimes(tracker, board.source(), 3))
	if got := board.readCount(); got != 3 {
		t.Errorf("the drag pasteboard was read %d times, want 3", got)
	}
}

func TestADropAndANewDragBetweenTwoPollsEndTheFirstDragAndStartTheSecond(t *testing.T) {
	board := newDragBoard()
	tracker := board.tracker()
	board.press()
	board.holdFiles("/tmp/first.pdf")
	first := pollTimes(tracker, board.source(), 2)

	board.release()
	board.press()
	board.holdFiles("/tmp/second.pdf")

	expectDragEvents(t, append(first, pollTimes(tracker, board.source(), 3)...), startedWith("/tmp/first.pdf"), dragEnd, startedWith("/tmp/second.pdf"))
}

func TestAChangeDuringADragToNoFilesEndsItAtTheSecondReadAndLaterFilesStartANewOne(t *testing.T) {
	board := newDragBoard()
	tracker := board.tracker()
	board.press()
	board.holdFiles("/tmp/first.pdf")
	pollTimes(tracker, board.source(), 2)
	board.holdNoFiles(clipboard.ErrNotFiles)
	first := pollTimes(tracker, board.source(), 1)
	ending := pollTimes(tracker, board.source(), 1)

	board.finishWriting("/tmp/second.pdf")

	expectDragEvents(t, first)
	expectDragEvents(t, append(ending, pollTimes(tracker, board.source(), 2)...), dragEnd, startedWith("/tmp/second.pdf"))
}

func TestAHalfWrittenReadDuringADragDoesNotEndIt(t *testing.T) {
	board := newDragBoard()
	tracker := board.tracker()
	board.press()
	board.holdFiles("/tmp/a.txt", "/tmp/b.txt")
	started := pollTimes(tracker, board.source(), 2)
	board.holdFiles("/tmp/a.txt")
	started = append(started, pollTimes(tracker, board.source(), 1)...)

	board.finishWriting("/tmp/a.txt", "/tmp/b.txt")

	expectDragEvents(t, append(started, pollTimes(tracker, board.source(), 4)...), startedWith("/tmp/a.txt", "/tmp/b.txt"))
}

func TestAReadFromBeforeAChangeDoesNotConfirmAReadAfterIt(t *testing.T) {
	board := newDragBoard()
	tracker := board.tracker()
	board.press()
	board.holdFiles("/tmp/a.txt")
	seen := pollTimes(tracker, board.source(), 1)
	board.holdFiles("/tmp/a.txt")
	seen = append(seen, pollTimes(tracker, board.source(), 1)...)

	board.finishWriting("/tmp/a.txt", "/tmp/c.txt")

	expectDragEvents(t, append(seen, pollTimes(tracker, board.source(), 3)...), startedWith("/tmp/a.txt", "/tmp/c.txt"))
}

func TestAReadWithoutFilesBetweenTwoEqualReadsStartsNothing(t *testing.T) {
	board := newDragBoard()
	tracker := board.tracker()
	board.press()
	board.holdFiles("/tmp/a.txt")
	seen := pollTimes(tracker, board.source(), 1)
	board.finishWritingNoFiles(clipboard.ErrNotFiles)
	seen = append(seen, pollTimes(tracker, board.source(), 1)...)

	board.finishWriting("/tmp/a.txt")

	expectDragEvents(t, append(seen, pollTimes(tracker, board.source(), 1)...))
}

func TestAChangeDuringADragToAPrivateCopyEndsItAtTheSecondReadAndReadsNoMore(t *testing.T) {
	board := newDragBoard()
	tracker := board.tracker()
	board.press()
	board.holdFiles("/tmp/report.pdf")
	pollTimes(tracker, board.source(), 2)

	board.holdNoFiles(clipboard.ErrPrivate)

	expectDragEvents(t, pollTimes(tracker, board.source(), 8), dragEnd)
	if got := board.readCount(); got != 4 {
		t.Errorf("the drag pasteboard was read %d times, want 4", got)
	}
}

func TestTheStartedPathsBelongToWhoeverReadsThem(t *testing.T) {
	board := newDragBoard()
	tracker := board.tracker()
	board.press()
	board.holdFiles("/tmp/report.pdf")
	started := pollTimes(tracker, board.source(), 2)
	started[0].Paths[0] = "/tmp/changed.pdf"

	board.holdFiles("/tmp/report.pdf")

	expectDragEvents(t, pollTimes(tracker, board.source(), 2))
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

	expectDragEvents(t, pollTimes(tracker, board.source(), 2), startedWith("/tmp/report.pdf"))
}

func TestADragOfSomethingOtherThanFilesIsReadSixTimes(t *testing.T) {
	board := newDragBoard()
	tracker := board.tracker()
	board.press()
	board.holdNoFiles(clipboard.ErrNotFiles)

	expectDragEvents(t, pollTimes(tracker, board.source(), 18))
	if got := board.readCount(); got != 6 {
		t.Errorf("the drag pasteboard was read %d times, want 6", got)
	}
}

func TestFilesWrittenAfterFourEmptyReadsStillStartTheDrag(t *testing.T) {
	board := newDragBoard()
	tracker := board.tracker()
	board.press()
	board.holdNoFiles(clipboard.ErrNotFiles)
	empty := pollTimes(tracker, board.source(), 4)

	board.finishWriting("/tmp/report.pdf")

	expectDragEvents(t, append(empty, pollTimes(tracker, board.source(), 2)...), startedWith("/tmp/report.pdf"))
}

func TestFilesWrittenAfterFiveEmptyReadsStartNothing(t *testing.T) {
	board := newDragBoard()
	tracker := board.tracker()
	board.press()
	board.holdNoFiles(clipboard.ErrNotFiles)
	empty := pollTimes(tracker, board.source(), 5)

	board.finishWriting("/tmp/report.pdf")

	expectDragEvents(t, append(empty, pollTimes(tracker, board.source(), 6)...))
}

func TestADragOfPrivateFilesOrOfTooManyIsReadOnce(t *testing.T) {
	for _, refusal := range []error{clipboard.ErrPrivate, fmt.Errorf("%w: %d", clipboard.ErrTooManyFiles, clipboard.FilesAndFoldersLimit+1)} {
		t.Run(refusal.Error(), func(t *testing.T) {
			board := newDragBoard()
			tracker := board.tracker()
			board.press()
			board.holdNoFiles(refusal)

			expectDragEvents(t, pollTimes(tracker, board.source(), 15))
			if got := board.readCount(); got != 1 {
				t.Errorf("the drag pasteboard was read %d times, want once", got)
			}
		})
	}
}

func TestAReleaseForgetsTheReadsLeftForAChangeWithoutFiles(t *testing.T) {
	board := newDragBoard()
	tracker := board.tracker()
	board.press()
	board.holdNoFiles(clipboard.ErrNotFiles)
	pollTimes(tracker, board.source(), 1)
	board.release()
	pollTimes(tracker, board.source(), 1)
	board.finishWriting("/tmp/report.pdf")

	board.press()

	expectDragEvents(t, pollTimes(tracker, board.source(), 3))
	if got := board.readCount(); got != 1 {
		t.Errorf("the drag pasteboard was read %d times, want once", got)
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
	pollTimes(tracker, board.source(), 2)
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

func expectNoDragEvent(t *testing.T, watch *DragWatch) {
	t.Helper()
	select {
	case seen := <-watch.Events():
		t.Errorf("the watch sent %+v, want nothing", seen)
	case <-time.After(quietWindow):
	}
}

func startWatch(t *testing.T, board *dragBoard) *DragWatch {
	t.Helper()
	watch, err := watchDrags(board.source(), shortestDragPoll)
	if err != nil {
		t.Fatalf("watchDrags: %v", err)
	}
	t.Cleanup(func() { watch.Close() })
	return watch
}

func TestAWatchSendsADragAndNothingElseAndClosesItsEventsWhenClosed(t *testing.T) {
	board := newDragBoard()
	watch := startWatch(t, board)
	board.press()
	board.holdFiles("/tmp/report.pdf")
	started := nextDragEvent(t, watch)
	waitForPolls(t, board, 5)
	expectNoDragEvent(t, watch)
	board.release()
	end := nextDragEvent(t, watch)
	waitForPolls(t, board, 5)
	expectNoDragEvent(t, watch)

	watch.Close()

	expectDragEvents(t, []DragEvent{started, end}, startedWith("/tmp/report.pdf"), dragEnd)
	select {
	case seen, open := <-watch.Events():
		if open {
			t.Errorf("the closed watch still sent %+v", seen)
		}
	default:
		t.Error("the events of the watch were not closed when Close returned")
	}
}

func TestAWatchStartedDuringAnEarlierDragDoesNotReadIt(t *testing.T) {
	board := newDragBoard()
	board.press()
	board.holdFiles("/tmp/earlier.pdf")
	watch := startWatch(t, board)

	waitForPolls(t, board, 5)

	expectNoDragEvent(t, watch)
	if got := board.readCount(); got != 0 {
		t.Errorf("the drag pasteboard was read %d times", got)
	}
}

func TestAResetDropsTheEventsNotYetReadAndForgetsTheDrag(t *testing.T) {
	board := newDragBoard()
	watch := startWatch(t, board)
	board.press()
	board.holdFiles("/tmp/first.pdf")
	deadline := time.Now().Add(watchPatience)
	for board.readCount() < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("the watch did not read the drag within %v", watchPatience)
		}
		time.Sleep(shortestDragPoll)
	}

	watch.Reset()
	board.release()
	waitForPolls(t, board, 5)
	expectNoDragEvent(t, watch)
	board.press()
	board.holdFiles("/tmp/second.pdf")

	expectDragEvents(t, []DragEvent{nextDragEvent(t, watch)}, startedWith("/tmp/second.pdf"))
}

func TestAResetLetsTheSameFilesStartAgainAtTheirNextChange(t *testing.T) {
	board := newDragBoard()
	watch := startWatch(t, board)
	board.press()
	board.holdFiles("/tmp/report.pdf")
	first := nextDragEvent(t, watch)
	reads := board.readCount()

	watch.Reset()
	ended := nextDragEvent(t, watch)
	waitForPolls(t, board, 5)
	expectNoDragEvent(t, watch)
	if got := board.readCount(); got != reads {
		t.Errorf("the reset watch read the drag pasteboard %d more times without a change", got-reads)
	}
	board.holdFiles("/tmp/report.pdf")

	expectDragEvents(t, []DragEvent{first, ended, nextDragEvent(t, watch)}, startedWith("/tmp/report.pdf"), dragEnd, startedWith("/tmp/report.pdf"))
}

type flickeringDrag struct {
	polls atomic.Int64
}

func (f *flickeringDrag) Mark() int64 {
	return (f.polls.Add(1) + 1) / 3
}

func (f *flickeringDrag) ReadFiles() ([]string, error) {
	return []string{fmt.Sprintf("/tmp/drag-%d", (f.polls.Load()+1)/3)}, nil
}

func (f *flickeringDrag) leftHeld() bool {
	return f.polls.Load()%3 != 1
}

const marksWhenTheQueueIsFull = 1 + 3*dragEventQueue/2

func watchThatNobodyReads(t *testing.T) (*flickeringDrag, *DragWatch) {
	t.Helper()
	flicker := &flickeringDrag{}
	watch, err := watchDrags(dragSource{board: flicker, leftHeld: flicker.leftHeld}, shortestDragPoll)
	if err != nil {
		t.Fatalf("watchDrags: %v", err)
	}
	t.Cleanup(func() { watch.Close() })
	deadline := time.Now().Add(watchPatience)
	for flicker.polls.Load() < marksWhenTheQueueIsFull {
		if time.Now().After(deadline) {
			t.Fatalf("the watch polled %d times, want %d", flicker.polls.Load(), marksWhenTheQueueIsFull)
		}
		time.Sleep(shortestDragPoll)
	}
	time.Sleep(quietWindow)
	if polls := flicker.polls.Load(); polls != marksWhenTheQueueIsFull {
		t.Fatalf("the watch polled %d times with a full queue, want it to stop at %d", polls, marksWhenTheQueueIsFull)
	}
	return flicker, watch
}

func TestClosingAWatchWithAFullQueueThatNobodyReadsReturns(t *testing.T) {
	_, watch := watchThatNobodyReads(t)
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

func TestAWatchThatNobodyReadsLosesNoEvent(t *testing.T) {
	_, watch := watchThatNobodyReads(t)
	var got []DragEvent

	for range 12 {
		got = append(got, nextDragEvent(t, watch))
	}

	var want []DragEvent
	for drag := 1; len(want) < 12; drag++ {
		want = append(want, startedWith(fmt.Sprintf("/tmp/drag-%d", drag)), dragEnd)
	}
	expectDragEvents(t, got, want...)
}

type slowDragBoard struct {
	marks    atomic.Int64
	polling  atomic.Int64
	entered  chan struct{}
	duration time.Duration
}

func (s *slowDragBoard) Mark() int64 {
	if s.marks.Add(1) == 1 {
		return 0
	}
	s.polling.Add(1)
	defer s.polling.Add(-1)
	select {
	case s.entered <- struct{}{}:
	default:
	}
	time.Sleep(s.duration)
	return 0
}

func (s *slowDragBoard) ReadFiles() ([]string, error) {
	return nil, clipboard.ErrNotFiles
}

func TestCloseReturnsOnlyOnceTheWatchHasStoppedPolling(t *testing.T) {
	slow := &slowDragBoard{entered: make(chan struct{}, 1), duration: 30 * time.Millisecond}
	watch, err := watchDrags(dragSource{board: slow, leftHeld: func() bool { return false }}, shortestDragPoll)
	if err != nil {
		t.Fatalf("watchDrags: %v", err)
	}
	select {
	case <-slow.entered:
	case <-time.After(watchPatience):
		t.Fatalf("the watch did not poll within %v", watchPatience)
	}

	watch.Close()

	if polling := slow.polling.Load(); polling != 0 {
		t.Errorf("Close returned while %d polls were still running", polling)
	}
	select {
	case _, open := <-watch.Events():
		if open {
			t.Error("the closed watch still sent an event")
		}
	default:
		t.Error("the events of the watch were not closed when Close returned")
	}
	reset := make(chan struct{})
	go func() {
		watch.Reset()
		close(reset)
	}()
	select {
	case <-reset:
	case <-time.After(watchPatience):
		t.Errorf("Reset of a closed watch did not return within %v", watchPatience)
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

func (s *endingScreen) PostMouseAtSessionTap(mouse MouseEvent) {
	s.did = append(s.did, atSessionTap(mouse))
}

func (s *endingScreen) pause(gap time.Duration) {
	s.did = append(s.did, gap)
	s.cursor = s.cursorLater
}

func postingAllowed() bool {
	return true
}

type atSessionTap MouseEvent

func endingAt(at Point) []any {
	return []any{
		KeyPress{Code: 0x35, Down: true},
		KeyPress{Code: 0x35},
		20 * time.Millisecond,
		atSessionTap(MouseEvent{Action: MouseUp, At: at, Button: 0, Clicks: 1}),
	}
}

func TestEndingASourceDragPostsEscapeThenALeftMouseUpAtTheSessionTapWhereTheCursorIsAfterTheGap(t *testing.T) {
	screen := &endingScreen{held: true, cursor: Point{X: 501, Y: 165}, cursorLater: Point{X: 503, Y: 166}}

	err := endSourceDrag(screen, postingAllowed, screen.pause)

	if err != nil {
		t.Fatalf("endSourceDrag: %v", err)
	}
	if want := endingAt(Point{X: 503, Y: 166}); !slices.Equal(screen.did, want) {
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

type sharedEndingScreen struct {
	mu     sync.Mutex
	did    []any
	pauses int
	paused chan struct{}
	resume chan struct{}
}

func (s *sharedEndingScreen) ButtonDown(int) bool {
	return true
}

func (s *sharedEndingScreen) Cursor() Point {
	return Point{X: 501, Y: 165}
}

func (s *sharedEndingScreen) PostKey(key KeyPress) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.did = append(s.did, key)
}

func (s *sharedEndingScreen) PostMouseAtSessionTap(mouse MouseEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.did = append(s.did, atSessionTap(mouse))
}

func (s *sharedEndingScreen) pause(gap time.Duration) {
	s.mu.Lock()
	s.did = append(s.did, gap)
	s.pauses++
	first := s.pauses == 1
	s.mu.Unlock()
	if first {
		close(s.paused)
		<-s.resume
	}
}

func (s *sharedEndingScreen) posted() []any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.did)
}

func TestTwoEndsOfASourceDragDoNotInterleave(t *testing.T) {
	screen := &sharedEndingScreen{paused: make(chan struct{}), resume: make(chan struct{})}
	ends := make(chan error, 2)
	go func() { ends <- endSourceDrag(screen, postingAllowed, screen.pause) }()
	select {
	case <-screen.paused:
	case <-time.After(watchPatience):
		t.Fatalf("the first end did not reach its gap within %v", watchPatience)
	}

	go func() { ends <- endSourceDrag(screen, postingAllowed, screen.pause) }()
	time.Sleep(50 * time.Millisecond)
	during := screen.posted()
	close(screen.resume)

	for range 2 {
		select {
		case err := <-ends:
			if err != nil {
				t.Errorf("endSourceDrag: %v", err)
			}
		case <-time.After(watchPatience):
			t.Fatalf("an end did not return within %v", watchPatience)
		}
	}
	if len(during) != 3 {
		t.Errorf("while the first end waited, %+v was posted, want only its Escape and its gap", during)
	}
	at := Point{X: 501, Y: 165}
	if want := append(endingAt(at), endingAt(at)...); !slices.Equal(screen.posted(), want) {
		t.Errorf("posted %+v, want %+v", screen.posted(), want)
	}
}

type stalledDragBoard struct {
	*dragBoard
	stall   chan struct{}
	stalled chan struct{}
	once    sync.Once
}

func (b *stalledDragBoard) ReadFiles() ([]string, error) {
	if b.readCount() == 1 {
		b.once.Do(func() { close(b.stalled) })
		<-b.stall
	}
	return b.dragBoard.ReadFiles()
}

func TestAResetReturnsAtOnceDuringASlowReadAndNothingReadBeforeItIsSent(t *testing.T) {
	for round := range 20 {
		board := &stalledDragBoard{dragBoard: newDragBoard(), stall: make(chan struct{}), stalled: make(chan struct{})}
		watch, err := watchDrags(dragSource{board: board, leftHeld: board.leftHeld}, shortestDragPoll)
		if err != nil {
			t.Fatalf("watchDrags: %v", err)
		}
		board.press()
		board.holdFiles("/tmp/report.pdf")
		select {
		case <-board.stalled:
		case <-time.After(watchPatience):
			t.Fatal("the watch never read the drag pasteboard a second time")
		}
		sent := make(chan DragEvent, 1)
		go func() {
			select {
			case seen := <-watch.Events():
				sent <- seen
			case <-time.After(10 * quietWindow):
			}
		}()
		time.Sleep(quietWindow / 4)

		returned := make(chan struct{})
		go func() {
			defer close(returned)
			watch.Reset()
			watch.Reset()
		}()
		select {
		case <-returned:
		case <-time.After(quietWindow):
			t.Fatalf("round %d: Reset waited for the read in progress", round)
		}
		close(board.stall)

		select {
		case seen := <-sent:
			t.Fatalf("round %d: the watch sent %+v, read before the reset", round, seen)
		case <-time.After(2 * quietWindow):
		}
		watch.Close()
	}
}

func TestAResetEndsADragAlreadyToldAndOnlyThat(t *testing.T) {
	board := newDragBoard()
	watch := startWatch(t, board)
	board.press()
	board.holdFiles("/tmp/report.pdf")
	started := nextDragEvent(t, watch)

	watch.Reset()
	ended := nextDragEvent(t, watch)
	watch.Reset()

	expectDragEvents(t, []DragEvent{started, ended}, startedWith("/tmp/report.pdf"), dragEnd)
	expectNoDragEvent(t, watch)
}

type replayedEndingScreen struct {
	endingScreen
}

func (s *replayedEndingScreen) PostMouse(mouse MouseEvent) {
	s.did = append(s.did, mouse)
}

func TestEndingTheDragOfTheReplayAlsoReleasesTheButtonAtTheHIDTapUnderTheShield(t *testing.T) {
	shield := Point{X: 640, Y: 12}
	for name, held := range map[string]bool{"the button reads held": true, "the button reads up although the replay pressed it": false} {
		t.Run(name, func(t *testing.T) {
			screen := &replayedEndingScreen{endingScreen{held: held, cursor: Point{X: 641, Y: 12}, cursorLater: Point{X: 641, Y: 12}}}

			err := endReplayedSourceDrag(screen, postingAllowed, screen.pause, shield)

			if err != nil {
				t.Fatalf("endReplayedSourceDrag: %v", err)
			}
			want := endingAt(Point{X: 641, Y: 12})
			if held {
				want = append(want, 150*time.Millisecond)
			}
			want = append(want, MouseEvent{Action: MouseUp, At: shield, Button: 0, Clicks: 1})
			if !slices.Equal(screen.did, want) {
				t.Errorf("did %+v, want %+v", screen.did, want)
			}
		})
	}
}

func TestNothingIsPostedToEndTheDragOfTheReplayWithoutThePermissionToPost(t *testing.T) {
	screen := &replayedEndingScreen{endingScreen{held: true}}

	err := endReplayedSourceDrag(screen, func() bool { return false }, screen.pause, Point{})

	if !errors.Is(err, ErrCannotEndDrag) || len(screen.did) != 0 {
		t.Errorf("endReplayedSourceDrag = %v after %+v, want %v and nothing posted", err, screen.did, ErrCannotEndDrag)
	}
}
