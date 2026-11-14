//go:build darwin && cgo

package macos

import (
	"sync/atomic"
	"testing"
	"time"
)

const testDragPoll = 5 * time.Millisecond

type countedReads struct {
	Pasteboard
	reads atomic.Int64
}

func (c *countedReads) ReadFiles() ([]string, error) {
	c.reads.Add(1)
	return c.Pasteboard.ReadFiles()
}

func watchOfThisTestsPasteboard(t *testing.T) (*countedReads, *atomic.Bool, *DragWatch) {
	t.Helper()
	board := &countedReads{Pasteboard: pasteboardOfThisTest(t)}
	held := new(atomic.Bool)
	watch, err := watchDrags(dragSource{board: board, leftHeld: held.Load}, testDragPoll)
	if err != nil {
		t.Fatalf("watchDrags: %v", err)
	}
	t.Cleanup(func() { watch.Close() })
	return board, held, watch
}

func TestAWatchReadsTheDraggedFilesFromThePasteboardAndSeesTheButtonComeUp(t *testing.T) {
	board, held, watch := watchOfThisTestsPasteboard(t)
	paths := []string{"/tmp/report.pdf", "/tmp/photos"}
	held.Store(true)
	if _, err := board.WriteFiles(paths); err != nil {
		t.Fatalf("WriteFiles: %v", err)
	}

	started := nextDragEvent(t, watch)
	held.Store(false)
	end := nextDragEvent(t, watch)

	expectDragEvents(t, []DragEvent{started, end}, startedWith(paths...), dragEnd)
}

func TestAWatchStartsNoDragOfSomethingOtherThanLocalFiles(t *testing.T) {
	copies := map[string]func(Pasteboard) error{
		"a web address": func(p Pasteboard) error {
			return p.writeURLs("https://example.com/report.pdf")
		},
		"a file and a web address": func(p Pasteboard) error {
			return p.writeURLs("file:///tmp/report.pdf", "https://example.com/notes.txt")
		},
		"a file on another host": func(p Pasteboard) error {
			return p.writeTyped("public.file-url", "file://server/share/report.pdf", nil)
		},
		"a text that names a file": func(p Pasteboard) error {
			_, err := p.Write("/tmp/dragged.txt")
			return err
		},
	}
	for name, copyOnto := range copies {
		t.Run(name, func(t *testing.T) {
			board, held, watch := watchOfThisTestsPasteboard(t)
			held.Store(true)
			if err := copyOnto(board.Pasteboard); err != nil {
				t.Fatalf("copy: %v", err)
			}
			read := time.Now().Add(watchPatience)
			for board.reads.Load() == 0 {
				if time.Now().After(read) {
					t.Fatalf("the watch did not read the pasteboard within %v", watchPatience)
				}
				time.Sleep(testDragPoll)
			}
			if _, err := board.WriteFiles([]string{"/tmp/dragged.txt"}); err != nil {
				t.Fatalf("WriteFiles: %v", err)
			}

			first := nextDragEvent(t, watch)

			expectDragEvents(t, []DragEvent{first}, startedWith("/tmp/dragged.txt"))
		})
	}
}

func TestTheDragPasteboardOfTheSystemHasAName(t *testing.T) {
	board, err := systemDragPasteboard()

	if err != nil || board.name == "" {
		t.Errorf("systemDragPasteboard = %q, %v, want a name", board.name, err)
	}
}

func TestAWatchOfTheSystemDragPasteboardStartsAndStops(t *testing.T) {
	watch, err := WatchDrags(DragPollInterval)
	if err != nil {
		t.Fatalf("WatchDrags: %v", err)
	}
	time.Sleep(5 * DragPollInterval)

	watch.Close()

	for range watch.Events() {
	}
	if _, err := WatchDrags(0); err == nil {
		t.Error("WatchDrags with no interval was accepted")
	}
}
