package clipshare

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
	"github.com/rodrigogaraujo/lanhop/internal/filestream"
)

const (
	firstRetryAfter   = time.Second
	longestRetryAfter = 30 * time.Second
)

var filesNotShared time.Time

type readResult struct {
	text       string
	holdsFiles bool
	files      clipboard.FileCopy
	paths      []string
	skipped    int
	err        error
	unchanged  bool
}

func (r readResult) kind() clipboard.Kinds {
	if r.holdsFiles {
		return clipboard.Files
	}
	return clipboard.Text
}

type readMarks struct {
	text  int64
	files int64
}

func bothMarks(mark int64) readMarks {
	return readMarks{text: mark, files: mark}
}

func (m *readMarks) take(kinds clipboard.Kinds, mark int64) {
	if kinds.Has(clipboard.Text) {
		m.text = mark
	}
	if kinds.Has(clipboard.Files) {
		m.files = mark
	}
}

func (m readMarks) of(kind clipboard.Kinds) int64 {
	if kind == clipboard.Files {
		return m.files
	}
	return m.text
}

type arrived interface {
	Paths() []string
	GiveUp()
}

type keeper struct {
	open            func() (Clipboard, error)
	log             io.Writer
	reachable       func(bool)
	jobs            *mailbox[func()]
	board           Clipboard
	gone            <-chan struct{}
	readMarks       readMarks
	untouchedMark   int64
	saidUnreachable bool
	retryAfter      time.Duration
	nextAttempt     time.Time
	now             func() time.Time
	holdsFiles      atomic.Bool

	marksMutex sync.Mutex
	marksDue   *clipboard.Kinds
}

func newKeeper(config Config, reachable func(bool)) *keeper {
	return &keeper{
		open:       config.Open,
		log:        config.Log,
		reachable:  reachable,
		jobs:       newMailbox[func()](),
		retryAfter: firstRetryAfter,
		now:        time.Now,
	}
}

func (k *keeper) queue(job func()) {
	k.marksMutex.Lock()
	k.marksDue = nil
	k.marksMutex.Unlock()
	k.jobs.put(job)
}

func (k *keeper) run(ctx context.Context, reachedAtStart chan<- bool) {
	defer k.disconnect()
	reachedAtStart <- k.connect()
	for {
		select {
		case <-ctx.Done():
			k.jobs.close()
			return
		case <-k.jobs.ready:
			for _, job := range k.jobs.take() {
				job()
			}
		case <-k.gone:
			k.lose()
		}
	}
}

func (k *keeper) canHoldFiles() bool {
	return k.holdsFiles.Load()
}

func (k *keeper) tryToReach() {
	k.queue(k.reachIfDue)
}

func (k *keeper) reachIfDue() {
	if k.board == nil && !k.now().Before(k.nextAttempt) && k.connect() {
		k.reachable(true)
	}
}

func (k *keeper) takeReadMark(kinds clipboard.Kinds) {
	k.marksMutex.Lock()
	if k.marksDue != nil {
		*k.marksDue |= kinds
		k.marksMutex.Unlock()
		return
	}
	due := &kinds
	k.marksDue = due
	k.marksMutex.Unlock()
	k.jobs.put(func() {
		k.marksMutex.Lock()
		if k.marksDue == due {
			k.marksDue = nil
		}
		taking := *due
		k.marksMutex.Unlock()
		if k.board != nil {
			k.readMarks.take(taking, k.board.Mark())
		}
	})
}

func (k *keeper) takeArrivalMark() {
	k.queue(func() {
		if k.board != nil {
			k.untouchedMark = k.board.Mark()
		}
	})
}

func (k *keeper) read(listFilesBy time.Time, done func(readResult)) {
	k.queue(func() { done(k.readNow(listFilesBy)) })
}

func (k *keeper) write(text string) {
	k.queue(func() { k.writeNow(text) })
}

func (k *keeper) writeFiles(files arrived) {
	k.queue(func() { k.writeFilesNow(files) })
}

func (k *keeper) connect() bool {
	board, err := k.open()
	if err != nil {
		k.nextAttempt = k.now().Add(k.retryAfter)
		k.retryAfter = min(2*k.retryAfter, longestRetryAfter)
		if !k.saidUnreachable {
			fmt.Fprintf(k.log, "Clipboard sharing is off on this machine, because lanhop cannot reach its clipboard: %s\n", onOneLine(err))
			k.saidUnreachable = true
		}
		return false
	}
	k.board, k.gone = board, nil
	_, holdsFiles := board.(FileClipboard)
	k.holdsFiles.Store(holdsFiles)
	k.tryAgainAtOnce()
	if ending, endsSometimes := board.(interface{ Done() <-chan struct{} }); endsSometimes {
		k.gone = ending.Done()
	}
	fresh := board.Mark()
	k.readMarks, k.untouchedMark = bothMarks(fresh), fresh
	if k.saidUnreachable {
		fmt.Fprintln(k.log, "lanhop reaches the clipboard of this machine again.")
		k.saidUnreachable = false
	}
	return true
}

func onOneLine(err error) string {
	return strings.ReplaceAll(err.Error(), "\n", "; ")
}

func (k *keeper) tryAgainAtOnce() {
	k.nextAttempt, k.retryAfter = time.Time{}, firstRetryAfter
}

func (k *keeper) lose() {
	k.disconnect()
	k.tryAgainAtOnce()
	fmt.Fprintln(k.log, "lanhop lost the clipboard of this machine, so clipboard sharing is off on it until lanhop reaches it again.")
	k.saidUnreachable = true
	k.reachable(false)
}

func (k *keeper) disconnect() {
	if closer, closes := k.board.(io.Closer); closes {
		closer.Close()
	}
	k.board, k.gone = nil, nil
	k.holdsFiles.Store(false)
}

func (k *keeper) readNow(listFilesBy time.Time) readResult {
	if k.board == nil {
		return readResult{err: ErrUnreachable}
	}
	now := k.board.Mark()
	if now == k.readMarks.text && now == k.readMarks.files {
		return readResult{unchanged: true}
	}
	result := k.readCopy(listFilesBy)
	readBefore := k.readMarks.of(result.kind())
	k.readMarks = bothMarks(now)
	if now == readBefore {
		return readResult{unchanged: true}
	}
	return result
}

func (k *keeper) readCopy(listFilesBy time.Time) readResult {
	files, holdsFiles := k.board.(FileClipboard)
	if !holdsFiles {
		text, err := k.board.Read()
		return readResult{text: text, err: err}
	}
	paths, err := files.ReadFiles()
	switch {
	case errors.Is(err, clipboard.ErrNotFiles):
		text, err := k.board.Read()
		if errors.Is(err, clipboard.ErrNotText) {
			err = fmt.Errorf("%w, nor files lanhop can read, such as a copied file that is gone", err)
		}
		return readResult{text: text, err: err}
	case errors.Is(err, clipboard.ErrPrivate):
		return readResult{err: err}
	case err != nil:
		return readResult{holdsFiles: true, err: err}
	case listFilesBy.IsZero():
		return readResult{holdsFiles: true, err: clipboard.ErrFilesNotShared}
	}
	listing, err := filestream.Count(paths, listFilesBy)
	return readResult{holdsFiles: true, files: listing.Copy, paths: paths, skipped: listing.Skipped, err: err}
}

func (k *keeper) writeNow(text string) {
	if k.board == nil {
		return
	}
	if k.board.Mark() != k.untouchedMark {
		fmt.Fprintln(k.log, "A copy arrived, and the newer copy made on this machine after focus arrived was kept.")
		return
	}
	mark, err := k.board.Write(text)
	if err != nil {
		fmt.Fprintf(k.log, "A copy arrived, and lanhop could not write it to the clipboard: %v\n", err)
		return
	}
	k.readMarks, k.untouchedMark = bothMarks(mark), mark
}

func (k *keeper) writeFilesNow(files arrived) {
	board, holdsFiles := k.board.(FileClipboard)
	if !holdsFiles {
		files.GiveUp()
		return
	}
	if board.Mark() != k.untouchedMark {
		fmt.Fprintln(k.log, "Files arrived, and the newer copy made on this machine after focus arrived was kept.")
		files.GiveUp()
		return
	}
	mark, err := board.WriteFiles(files.Paths())
	if err != nil {
		fmt.Fprintf(k.log, "Files arrived, and lanhop could not put them on the clipboard: %v\n", err)
		files.GiveUp()
		return
	}
	k.readMarks, k.untouchedMark = bothMarks(mark), mark
}
