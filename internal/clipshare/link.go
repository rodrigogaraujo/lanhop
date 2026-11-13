package clipshare

import (
	"fmt"
	"io"
	"slices"
	"sync"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
	"github.com/rodrigogaraujo/lanhop/internal/event"
)

type Conn interface {
	Send(events ...event.Event) error
	Close() error
}

type delivery struct {
	slices      [][]event.Event
	inTheWindow bool
	isACopy     bool
	stopped     bool
	done        bool
}

type link struct {
	conn         Conn
	name         string
	silenceLimit time.Duration
	log          io.Writer

	mutex           sync.Mutex
	window          clipboard.Window
	waiting         []*delivery
	current         *delivery
	closed          bool
	leavesAnnounced int
	leavesHandled   int
	wake            chan struct{}
	closing         chan struct{}

	assembler clipboard.Assembler
}

func startLink(conn Conn, name string, silenceLimit time.Duration, log io.Writer) *link {
	l := &link{
		conn:         conn,
		name:         name,
		silenceLimit: silenceLimit,
		log:          log,
		wake:         make(chan struct{}, 1),
		closing:      make(chan struct{}),
	}
	go l.deliver()
	return l
}

func (l *link) sendFrame(frame event.Event) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	l.queue(&delivery{slices: [][]event.Event{{frame}}, inTheWindow: clipboard.IsCopyFrame(frame)})
}

func (l *link) sendCopy(text string) (queued bool) {
	parts, err := clipboard.Slices(text)
	if err != nil {
		return false
	}
	l.mutex.Lock()
	defer l.mutex.Unlock()
	if l.leavesAnnounced != l.leavesHandled {
		return false
	}
	return l.queue(&delivery{slices: parts, inTheWindow: true, isACopy: true})
}

func (l *link) queue(next *delivery) bool {
	if l.closed {
		return false
	}
	l.waiting = append(l.waiting, next)
	l.poke()
	return true
}

func (l *link) leave() (copyCutShort bool) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	l.leavesAnnounced++
	return l.stopCopiesLocked()
}

func (l *link) leaveHandled() {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	l.leavesHandled++
}

func (l *link) stopCopies() (copyCutShort bool) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	return l.stopCopiesLocked()
}

func (l *link) stopCopiesLocked() (copyCutShort bool) {
	if l.current != nil && l.current.isACopy && !l.current.done {
		l.current.stopped = true
		copyCutShort = true
	}
	unsent := len(l.waiting)
	l.waiting = slices.DeleteFunc(l.waiting, func(d *delivery) bool { return d.isACopy })
	if len(l.waiting) < unsent {
		copyCutShort = true
	}
	l.poke()
	return copyCutShort
}

func (l *link) acknowledged(framesRead int32) {
	l.mutex.Lock()
	err := l.window.Acked(framesRead)
	l.poke()
	l.mutex.Unlock()
	if err != nil {
		fmt.Fprintf(l.log, "Closing the connection to %s: %v\n", l.name, err)
		l.breakOff()
	}
}

func (l *link) take(frame event.Event) (text string, progress clipboard.Progress, err error) {
	text, progress, err = l.assembler.Take(frame)
	if ack, due := l.assembler.Ack(); due && l.conn.Send(ack) != nil {
		l.breakOff()
	}
	return text, progress, err
}

func (l *link) close() {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	if !l.closed {
		l.closed = true
		close(l.closing)
	}
}

func (l *link) breakOff() {
	l.conn.Close()
	l.close()
}

func (l *link) poke() {
	select {
	case l.wake <- struct{}{}:
	default:
	}
}

func (l *link) deliver() {
	for {
		next := l.next()
		if next == nil {
			return
		}
		l.carryOut(next)
	}
}

func (l *link) next() *delivery {
	for {
		l.mutex.Lock()
		if l.closed {
			l.mutex.Unlock()
			return nil
		}
		if len(l.waiting) > 0 {
			next := l.waiting[0]
			l.current, l.waiting = next, l.waiting[1:]
			l.mutex.Unlock()
			return next
		}
		l.current = nil
		l.mutex.Unlock()
		select {
		case <-l.wake:
		case <-l.closing:
			return nil
		}
	}
}

func (l *link) carryOut(d *delivery) {
	for i, part := range d.slices {
		if !l.claimRoomFor(d, len(part)) {
			return
		}
		if l.conn.Send(part...) != nil {
			l.breakOff()
			return
		}
		if i == len(d.slices)-1 {
			l.mutex.Lock()
			d.done = true
			l.mutex.Unlock()
		}
	}
}

func (l *link) claimRoomFor(d *delivery, frames int) bool {
	var patience *time.Timer
	defer func() {
		if patience != nil {
			patience.Stop()
		}
	}()
	for {
		l.mutex.Lock()
		switch {
		case d.stopped || l.closed:
			l.mutex.Unlock()
			return false
		case !d.inTheWindow:
			l.mutex.Unlock()
			return true
		case l.window.Room() >= frames:
			l.window.Sent(frames)
			l.mutex.Unlock()
			return true
		}
		l.mutex.Unlock()
		if patience == nil {
			patience = time.NewTimer(l.silenceLimit)
		}
		select {
		case <-l.wake:
		case <-l.closing:
			return false
		case <-patience.C:
			fmt.Fprintf(l.log, "Closing the connection to %s: it acknowledged no copy frame for %s\n", l.name, l.silenceLimit)
			l.breakOff()
			return false
		}
	}
}
