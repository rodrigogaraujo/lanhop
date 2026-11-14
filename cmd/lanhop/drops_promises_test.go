package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/clipshare"
	"github.com/rodrigogaraujo/lanhop/internal/event"
)

type fakePromisedDrop struct {
	log      *replayLog
	ended    chan struct{}
	kept     chan struct{}
	accepted bool
	state    sync.Mutex
	closes   int
	closing  func()
}

func newFakePromisedDrop(log *replayLog) *fakePromisedDrop {
	return &fakePromisedDrop{log: log, ended: make(chan struct{}), kept: make(chan struct{})}
}

func (d *fakePromisedDrop) Ended() <-chan struct{} { return d.ended }

func (d *fakePromisedDrop) Accepted() bool {
	d.state.Lock()
	defer d.state.Unlock()
	return d.accepted
}

func (d *fakePromisedDrop) Kept() <-chan struct{} { return d.kept }

func (d *fakePromisedDrop) Close() {
	d.state.Lock()
	d.closes++
	closing := d.closing
	d.state.Unlock()
	if closing != nil {
		closing()
	}
	d.log.record("drop closed")
}

func (d *fakePromisedDrop) end(accepted bool) {
	d.state.Lock()
	d.accepted = accepted
	d.state.Unlock()
	close(d.ended)
}

func (d *fakePromisedDrop) timesClosed() int {
	d.state.Lock()
	defer d.state.Unlock()
	return d.closes
}

type promiseDesk struct {
	log    *replayLog
	hand   *dropReplay
	drops  *promiseDrops
	mutex  sync.Mutex
	made   []*fakePromisedDrop
	writes []writePromise
	during func(hand *dropReplay)
	fails  error
}

func newPromiseDesk(t *testing.T) *promiseDesk {
	t.Helper()
	d := &promiseDesk{log: &replayLog{}}
	d.hand = newDropReplay(keepingReplayLog{d.log}, nil)
	d.drops = newPromiseDrops(d.start, d.hand, nil, &screen{})
	t.Cleanup(d.drops.close)
	return d
}

func (d *promiseDesk) start(ctx context.Context, files []clipshare.DroppedFile, write writePromise) (promisedDrop, error) {
	d.log.record("drop starts")
	if d.during != nil {
		d.during(d.hand)
	}
	if d.fails != nil {
		return nil, d.fails
	}
	drop := newFakePromisedDrop(d.log)
	d.mutex.Lock()
	d.made = append(d.made, drop)
	d.writes = append(d.writes, write)
	d.mutex.Unlock()
	return drop, nil
}

func (d *promiseDesk) startADrop(t *testing.T, files ...clipshare.DroppedFile) (clipshare.DropSession, *fakePromisedDrop, writePromise) {
	t.Helper()
	if len(files) == 0 {
		files = []clipshare.DroppedFile{{Name: "report.txt"}}
	}
	session, err := d.drops.StartDrop(context.Background(), files)
	if err != nil {
		t.Fatalf("StartDrop: %v", err)
	}
	d.mutex.Lock()
	defer d.mutex.Unlock()
	return session, d.made[len(d.made)-1], d.writes[len(d.writes)-1]
}

func closedWithin(t *testing.T, what string, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(testPatience):
		t.Fatalf("%s did not happen", what)
	}
}

func stillOpen(t *testing.T, what string, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
		t.Errorf("%s happened", what)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestTheReplayIsHeldBackWhileTheDropOfAMacStartsAndThenDragsWithTheAdoptedButton(t *testing.T) {
	d := newPromiseDesk(t)
	d.during = func(hand *dropReplay) {
		hand.Emit(event.Relative(event.RelX, 5), event.Sync())
	}

	d.startADrop(t)

	expectEntries(t, "the replay", onlyEvents(d.log.seen()), "drop starts", "hold 0", "x +5")
}

func TestADropThatCannotStartLetsTheReplayGoOnWithoutHoldingTheButton(t *testing.T) {
	d := newPromiseDesk(t)
	d.fails = errors.New("no window")
	d.during = func(hand *dropReplay) {
		hand.Emit(event.Relative(event.RelX, 5), event.Sync())
	}

	if _, err := d.drops.StartDrop(context.Background(), []clipshare.DroppedFile{{Name: "a"}}); err == nil {
		t.Fatal("StartDrop succeeded with a drop that could not start")
	}

	expectEntries(t, "the replay", onlyEvents(d.log.seen()), "drop starts", "x +5")
}

func TestAnAcceptedOperationIsTakenAndTheDropClosesOnceEveryFileIsWritten(t *testing.T) {
	d := newPromiseDesk(t)
	session, drop, _ := d.startADrop(t)

	drop.end(true)

	closedWithin(t, "Taken", session.Taken())
	stillOpen(t, "EndedWithoutDrop", session.EndedWithoutDrop())
	if drop.timesClosed() != 0 {
		t.Error("the drop closed before its files were written")
	}
	close(drop.kept)
	waitUntil(t, "the drop to close once its files were written", func() bool { return drop.timesClosed() == 1 })
	if d.log.count("forget 0") != 0 {
		t.Error("a taken drop forgot the button the person released on it")
	}
}

func TestAnEndWithoutAnAcceptedOperationIsADropNotTakenThatLetsGoOfTheButton(t *testing.T) {
	d := newPromiseDesk(t)
	session, drop, _ := d.startADrop(t)

	drop.end(false)

	closedWithin(t, "EndedWithoutDrop", session.EndedWithoutDrop())
	stillOpen(t, "Taken", session.Taken())
	waitUntil(t, "the drop to close", func() bool { return drop.timesClosed() == 1 })
	expectEntries(t, "the replay", onlyEvents(d.log.seen()), "drop starts", "hold 0", "drop closed", "forget 0")
}

func TestCancellingADropHoldsTheReplayBackWhileItClosesThenForgetsTheButton(t *testing.T) {
	d := newPromiseDesk(t)
	session, drop, _ := d.startADrop(t)
	drop.closing = func() { d.hand.Emit(event.Relative(event.RelX, 1), event.Sync()) }

	session.Cancel()
	session.Cancel()

	if drop.timesClosed() != 1 {
		t.Errorf("the drop closed %d times, want once", drop.timesClosed())
	}
	expectEntries(t, "the replay", onlyEvents(d.log.seen()), "drop starts", "hold 0", "drop closed", "forget 0", "x +1")
}

func TestThePreviousDropIsClosedBeforeTheNextStartsUnlessItWasTaken(t *testing.T) {
	d := newPromiseDesk(t)
	_, first, _ := d.startADrop(t)
	secondSession, second, _ := d.startADrop(t)
	if first.timesClosed() != 1 {
		t.Errorf("the first drop closed %d times before the second started, want once", first.timesClosed())
	}
	entries := onlyEvents(d.log.seen())
	if want := []string{"drop starts", "hold 0", "drop closed", "forget 0", "drop starts", "hold 0"}; !slices.Equal(entries, want) {
		t.Errorf("the replay saw %q, want %q", entries, want)
	}

	second.end(true)
	closedWithin(t, "Taken", secondSession.Taken())
	d.startADrop(t)
	if second.timesClosed() != 0 {
		t.Error("a taken drop that still writes its files was closed when the next started")
	}
}

func TestStoppingClosesEveryDropAndStartsNoMore(t *testing.T) {
	d := newPromiseDesk(t)
	_, drop, _ := d.startADrop(t)

	d.drops.close()

	if drop.timesClosed() != 1 {
		t.Errorf("the drop closed %d times when lanhop stopped, want once", drop.timesClosed())
	}
	if _, err := d.drops.StartDrop(context.Background(), []clipshare.DroppedFile{{Name: "a"}}); !errors.Is(err, errDropsClosed) {
		t.Errorf("StartDrop after stopping = %v, want %v", err, errDropsClosed)
	}
}

func TestAPromiseWaitsForTheFilesThenCopiesThemAndKeepsTheArrival(t *testing.T) {
	d := newPromiseDesk(t)
	session, _, write := d.startADrop(t, clipshare.DroppedFile{Name: "notes.txt"}, clipshare.DroppedFile{Name: "photos", Folder: true})
	arrived := t.TempDir()
	note := filepath.Join(arrived, "notes.txt")
	writeTestFile(t, note, "a note")
	photos := filepath.Join(arrived, "photos")
	writeTestFile(t, filepath.Join(photos, "beach", "one.jpg"), "one")
	writeTestFile(t, filepath.Join(photos, "two.jpg"), "two")
	destination := t.TempDir()

	written := make(chan error, 2)
	go func() { written <- write(context.Background(), 0, filepath.Join(destination, "notes.txt")) }()
	go func() { written <- write(context.Background(), 1, filepath.Join(destination, "photos")) }()
	select {
	case err := <-written:
		t.Fatalf("a promise was written before the files arrived: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	session.Arrived([]string{note, photos})
	for range 2 {
		if err := <-written; err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	for path, want := range map[string]string{
		filepath.Join(destination, "notes.txt"):                  "a note",
		filepath.Join(destination, "photos", "beach", "one.jpg"): "one",
		filepath.Join(destination, "photos", "two.jpg"):          "two",
		filepath.Join(photos, "beach", "one.jpg"):                "one",
		note: "a note",
	} {
		if got, err := os.ReadFile(path); err != nil || string(got) != want {
			t.Errorf("%s holds %q, %v, want %q", path, got, err, want)
		}
	}
}

func TestAPromiseThatWaitsInVainEndsWithItsContext(t *testing.T) {
	d := newPromiseDesk(t)
	_, _, write := d.startADrop(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	if err := write(ctx, 0, filepath.Join(t.TempDir(), "report.txt")); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("write = %v, want the end of its context", err)
	}
}

func TestAPromiseNeverReplacesAFileAtItsDestinationNorAsksForAFileItDoesNotHold(t *testing.T) {
	d := newPromiseDesk(t)
	session, _, write := d.startADrop(t)
	arrived := filepath.Join(t.TempDir(), "report.txt")
	writeTestFile(t, arrived, "new")
	destination := filepath.Join(t.TempDir(), "report.txt")
	writeTestFile(t, destination, "old")
	session.Arrived([]string{arrived})

	if err := write(context.Background(), 0, destination); err == nil {
		t.Error("a promise replaced a file at its destination")
	}
	if got, _ := os.ReadFile(destination); string(got) != "old" {
		t.Errorf("the file at the destination holds %q, want it untouched", got)
	}
	if err := write(context.Background(), 3, filepath.Join(t.TempDir(), "x")); !errors.Is(err, errNoSuchDroppedFile) {
		t.Errorf("a promise for a fourth file = %v, want %v", err, errNoSuchDroppedFile)
	}
}

func TestACopyLeavesNothingBehindWhenItFails(t *testing.T) {
	arrived := t.TempDir()
	writeTestFile(t, filepath.Join(arrived, "a.txt"), "a")
	writeTestFile(t, filepath.Join(arrived, "b.txt"), "b")
	destination := filepath.Join(t.TempDir(), "folder")
	failing := errors.New("mark refused")

	err := copyEntry(context.Background(), arrived, destination, func(source, _ string) error {
		if filepath.Base(source) == "b.txt" {
			return failing
		}
		return nil
	})

	if !errors.Is(err, failing) {
		t.Errorf("copyEntry = %v, want %v", err, failing)
	}
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the failed copy left %s behind: %v", destination, err)
	}
}

func TestACopyMarksEveryFileAndFolderItMakes(t *testing.T) {
	arrived := t.TempDir()
	writeTestFile(t, filepath.Join(arrived, "inside", "a.txt"), "a")
	destination := filepath.Join(t.TempDir(), "copy")
	var marked []string

	if err := copyEntry(context.Background(), arrived, destination, func(_, made string) error {
		marked = append(marked, made)
		return nil
	}); err != nil {
		t.Fatalf("copyEntry: %v", err)
	}

	want := []string{filepath.Join(destination, "inside", "a.txt"), filepath.Join(destination, "inside"), destination}
	if !slices.Equal(marked, want) {
		t.Errorf("marked %q, want %q", marked, want)
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func TestAFolderWhoseOwnMarkFailsIsRemovedLikeAFile(t *testing.T) {
	arrived := t.TempDir()
	writeTestFile(t, filepath.Join(arrived, "a.txt"), "a")
	destination := filepath.Join(t.TempDir(), "folder")
	failing := errors.New("mark refused")

	err := copyEntry(context.Background(), arrived, destination, func(_, to string) error {
		if to == destination {
			return failing
		}
		return nil
	})

	if !errors.Is(err, failing) {
		t.Errorf("copyEntry = %v, want %v", err, failing)
	}
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the folder whose mark failed was left behind: %v", err)
	}
}

func TestAFolderIsNeverCopiedIntoOrRemovedWhereAFolderOfTheSameNameIsAlready(t *testing.T) {
	arrived := t.TempDir()
	writeTestFile(t, filepath.Join(arrived, "a.txt"), "a")
	destination := filepath.Join(t.TempDir(), "folder")
	writeTestFile(t, filepath.Join(destination, "mine.txt"), "mine")

	if err := copyEntry(context.Background(), arrived, destination, nil); err == nil {
		t.Error("a folder was copied over a folder of the same name")
	}
	if got, err := os.ReadFile(filepath.Join(destination, "mine.txt")); err != nil || string(got) != "mine" {
		t.Errorf("the folder already there holds %q, %v, want it untouched", got, err)
	}
	if _, err := os.Stat(filepath.Join(destination, "a.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a file was copied into the folder already there: %v", err)
	}
}

func TestATakenDropWhoseFilesAreNeverAllWrittenIsClosedAfterItsBoundAndTellsWhichFailed(t *testing.T) {
	d := newPromiseDesk(t)
	said := &screen{}
	d.drops.log, d.drops.keptLimit = said, 50*time.Millisecond
	session, drop, write := d.startADrop(t, clipshare.DroppedFile{Name: "report.txt"})
	arrived := filepath.Join(t.TempDir(), "report.txt")
	writeTestFile(t, arrived, "new")
	destination := filepath.Join(t.TempDir(), "report.txt")
	writeTestFile(t, destination, "old")
	session.Arrived([]string{arrived})
	if err := write(context.Background(), 0, destination); err == nil {
		t.Fatal("the promise replaced a file at its destination")
	}

	drop.end(true)
	closedWithin(t, "the drop being taken", session.Taken())

	for deadline := time.Now().Add(testPatience); drop.timesClosed() == 0; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the taken drop whose promise failed was never closed")
		}
	}
	if !strings.Contains(said.String(), "these files were not written") || !strings.Contains(said.String(), "report.txt") {
		t.Errorf("the log says %q, want the file that was not written", said.String())
	}
}
