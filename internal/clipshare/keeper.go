package clipshare

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"time"
)

const (
	firstRetryAfter   = time.Second
	longestRetryAfter = 30 * time.Second
)

type readResult struct {
	text      string
	err       error
	unchanged bool
}

type keeper struct {
	open            func() (Clipboard, error)
	log             io.Writer
	reachable       func(bool)
	jobs            *mailbox[func()]
	board           Clipboard
	gone            <-chan struct{}
	readMark        int64
	untouchedMark   int64
	saidUnreachable bool
	retryAfter      time.Duration
	nextAttempt     time.Time
	now             func() time.Time
	readMarkWaiting atomic.Bool
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
	k.readMarkWaiting.Store(false)
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

func (k *keeper) tryToReach() {
	k.queue(k.reachIfDue)
}

func (k *keeper) reachIfDue() {
	if k.board == nil && !k.now().Before(k.nextAttempt) && k.connect() {
		k.reachable(true)
	}
}

func (k *keeper) takeReadMark() {
	if k.readMarkWaiting.Swap(true) {
		return
	}
	k.jobs.put(func() {
		k.readMarkWaiting.Store(false)
		if k.board != nil {
			k.readMark = k.board.Mark()
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

func (k *keeper) read(done func(readResult)) {
	k.queue(func() { done(k.readNow()) })
}

func (k *keeper) write(text string) {
	k.queue(func() { k.writeNow(text) })
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
	k.tryAgainAtOnce()
	if ending, endsSometimes := board.(interface{ Done() <-chan struct{} }); endsSometimes {
		k.gone = ending.Done()
	}
	fresh := board.Mark()
	k.readMark, k.untouchedMark = fresh, fresh
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
}

func (k *keeper) readNow() readResult {
	if k.board == nil {
		return readResult{err: ErrUnreachable}
	}
	now := k.board.Mark()
	if now == k.readMark {
		return readResult{unchanged: true}
	}
	text, err := k.board.Read()
	k.readMark = now
	return readResult{text: text, err: err}
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
	k.readMark, k.untouchedMark = mark, mark
}
