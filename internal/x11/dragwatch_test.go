//go:build linux

package x11

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

var pathsDraggedAfter = []string{"/home/someone/Desktop/dragged after.txt"}

func watchOf(t *testing.T, drags *Drags) *DragWatch {
	t.Helper()
	watch, err := drags.WatchDrags()
	if err != nil {
		t.Fatalf("WatchDrags: %v", err)
	}
	t.Cleanup(func() { watch.Close() })
	return watch
}

func watchOn(t *testing.T, display string) *DragWatch {
	t.Helper()
	return watchOf(t, openDragsOn(t, display))
}

func nextDragEvent(t *testing.T, watch *DragWatch) DragEvent {
	t.Helper()
	select {
	case event, open := <-watch.Events():
		if !open {
			t.Fatal("the watch ended")
		}
		return event
	case <-time.After(5 * time.Second):
		t.Fatal("the watcher reported nothing")
		return DragEvent{}
	}
}

func checkStarted(t *testing.T, event DragEvent, paths []string) {
	t.Helper()
	if event.Kind != DragStarted || !slices.Equal(event.Paths, paths) {
		t.Errorf("the watcher reported %+v, want a started drag of %q", event, paths)
	}
}

func checkEnded(t *testing.T, event DragEvent) {
	t.Helper()
	if event.Kind != DragEnded || event.Paths != nil {
		t.Errorf("the watcher reported %+v, want the end of the drag", event)
	}
}

func checkWatchEnds(t *testing.T, watch *DragWatch) {
	t.Helper()
	select {
	case event, open := <-watch.Events():
		if open {
			t.Errorf("the watcher reported %+v, want its events closed", event)
		}
	case <-time.After(5 * time.Second):
		t.Error("the events of the watcher were not closed")
	}
}

func TestTheWatcherSeesAFileDragFromItsStartToItsEnd(t *testing.T) {
	endings := map[string]func(t *testing.T, display string, source *xdndSource){
		"the source lets XdndSelection go":    func(t *testing.T, _ string, source *xdndSource) { source.letGo(t) },
		"the source window is destroyed":      func(t *testing.T, _ string, source *xdndSource) { source.destroyWindow(t) },
		"the source program quits":            func(_ *testing.T, _ string, source *xdndSource) { source.quit() },
		"another program takes XdndSelection": func(t *testing.T, display string, _ *xdndSource) { takeXdndSelection(t, display) },
	}
	for name, end := range endings {
		t.Run(name, func(t *testing.T) {
			display := startXvfb(t)
			watch := watchOn(t, display)
			paths := filesWithAwkwardNames(t)
			source := startXdndSource(t, display, filesOffer(paths...))

			source.own(t)
			checkStarted(t, nextDragEvent(t, watch), paths)
			end(t, display, source)

			checkEnded(t, nextDragEvent(t, watch))
		})
	}
}

func TestTheWatcherAsksTheSourceAsOfTheTimeItTookTheDrag(t *testing.T) {
	display := startXvfb(t)
	watch := watchOn(t, display)
	source := startXdndSource(t, display, filesOffer(pathsDraggedAfter...))

	source.own(t)
	checkStarted(t, nextDragEvent(t, watch), pathsDraggedAfter)

	for _, target := range []string{"TARGETS", "text/uri-list"} {
		if asked := source.wasAsked(t, target); asked.time != source.since {
			t.Errorf("%s was asked for at time %d, want %d, when the source took XdndSelection", target, asked.time, source.since)
		}
	}
}

func TestTheWatcherReportsNoDragOfAnythingButLocalFiles(t *testing.T) {
	offers := map[string]struct {
		offer     sourceOffer
		lastAsked string
	}{
		"a web address":            {sourceOffer{types: nautilusTypes, list: "https://example.com/report.pdf\r\n"}, "text/uri-list"},
		"a file and a web address": {sourceOffer{types: nautilusTypes, list: listOf("/home/someone/a.txt") + "http://example.com/b.txt\r\n"}, "text/uri-list"},
		"a file on another host":   {sourceOffer{types: nautilusTypes, list: "file://elsewhere/home/someone/a.txt\r\n"}, "text/uri-list"},
		"a list with no address":   {sourceOffer{types: nautilusTypes, list: "# only a comment\r\n"}, "text/uri-list"},
		"no address list at all":   {sourceOffer{types: []string{"UTF8_STRING", "text/plain;charset=utf-8"}}, "TARGETS"},
	}
	for name, test := range offers {
		t.Run(name, func(t *testing.T) {
			display := startXvfb(t)
			watch := watchOn(t, display)
			other := startXdndSource(t, display, test.offer)
			other.own(t)
			other.wasAsked(t, test.lastAsked)
			other.letGo(t)

			source := startXdndSource(t, display, filesOffer(pathsDraggedAfter...))
			source.own(t)

			checkStarted(t, nextDragEvent(t, watch), pathsDraggedAfter)
		})
	}
}

func TestTheWatcherIgnoresTheDragOfLanhopItself(t *testing.T) {
	display := startXvfb(t)
	drags := openDragsOn(t, display)
	watch := watchOf(t, drags)
	drag, err := drags.Start()
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(drag.Cancel)
	if err := drag.FilesArrived(arrivedPaths); err != nil {
		t.Fatalf("FilesArrived: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for dragSelectionOwner(t, drags) != drags.window {
		if time.Now().After(deadline) {
			t.Fatal("lanhop never took XdndSelection for its drag")
		}
		time.Sleep(5 * time.Millisecond)
	}

	select {
	case event := <-watch.Events():
		t.Errorf("while lanhop dragged, the watcher reported %+v", event)
	case <-time.After(300 * time.Millisecond):
	}
	drag.Cancel()
	source := startXdndSource(t, display, filesOffer(pathsDraggedAfter...))
	source.own(t)

	checkStarted(t, nextDragEvent(t, watch), pathsDraggedAfter)
}

func TestTheWatcherGivesUpOnASourceThatNeverAnswersAndWatchesOn(t *testing.T) {
	display := startXvfb(t)
	drags := openDragsOn(t, display)
	drags.limits.read = 300 * time.Millisecond
	watch := watchOf(t, drags)
	silent := startXdndSource(t, display, sourceOffer{types: nautilusTypes, silent: true})

	asked := time.Now()
	silent.own(t)
	silent.wasAsked(t, "TARGETS")
	source := startXdndSource(t, display, filesOffer(pathsDraggedAfter...))
	source.own(t)

	checkStarted(t, nextDragEvent(t, watch), pathsDraggedAfter)
	if waited := time.Since(asked); waited > drags.limits.read+2*time.Second {
		t.Errorf("the next drag was reported %v after the silent one started, want about %v", waited, drags.limits.read)
	}
}

func TestTheWatcherReadsAListLongerThanOneProperty(t *testing.T) {
	display := startXvfb(t)
	watch := watchOn(t, display)
	paths := make([]string, 1500)
	for i := range paths {
		paths[i] = fmt.Sprintf("/home/someone/%s%04d.txt", strings.Repeat("a-long-name-", 6), i)
	}
	source := startXdndSource(t, display, filesOffer(paths...))
	if size := len(listOf(paths...)); size <= largestChunk {
		t.Fatalf("the list has %d bytes, too few to need several parts", size)
	}

	source.own(t)

	checkStarted(t, nextDragEvent(t, watch), paths)
}

func TestANewDragFromTheSameSourceEndsThePreviousOneFirst(t *testing.T) {
	display := startXvfb(t)
	watch := watchOn(t, display)
	source := startXdndSource(t, display, filesOffer(pathsDraggedAfter...))

	source.own(t)
	checkStarted(t, nextDragEvent(t, watch), pathsDraggedAfter)
	source.own(t)

	checkEnded(t, nextDragEvent(t, watch))
	checkStarted(t, nextDragEvent(t, watch), pathsDraggedAfter)
}

func TestClosingTheWatcherEndsItsEvents(t *testing.T) {
	display := startXvfb(t)
	watch := watchOn(t, display)

	if err := watch.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}

	checkWatchEnds(t, watch)
	if err := watch.Close(); err != nil {
		t.Errorf("a second Close: %v", err)
	}
}

func TestLosingTheXServerEndsTheWatcher(t *testing.T) {
	display, stop := startXvfbThatCanStop(t)
	watch := watchOn(t, display)

	stop()

	checkWatchEnds(t, watch)
}
