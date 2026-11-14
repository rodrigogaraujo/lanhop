package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/clipshare"
)

const (
	copyChunk         = 1 << 20
	promisesKeptLimit = 2 * time.Minute
)

var (
	errDropsClosed       = errors.New("lanhop is stopping, so it starts no more drops")
	errNoSuchDroppedFile = errors.New("the destination asked for a file that the drop does not hold")
	errNotFileOrFolder   = errors.New("an entry that arrived is neither a file nor a folder")
)

type writePromise func(ctx context.Context, file int, destination string) error

type promisedDrop interface {
	Ended() <-chan struct{}
	Accepted() bool
	Kept() <-chan struct{}
	Close()
}

type startPromisedDrop func(ctx context.Context, files []clipshare.DroppedFile, write writePromise) (promisedDrop, error)

type markCopy func(source, destination string) error

type promiseDrops struct {
	start     startPromisedDrop
	hand      *dropReplay
	mark      markCopy
	log       io.Writer
	keptLimit time.Duration
	starting  sync.Mutex
	mutex     sync.Mutex
	open      map[*promiseSession]struct{}
	closed    bool
}

func newPromiseDrops(start startPromisedDrop, hand *dropReplay, mark markCopy, log io.Writer) *promiseDrops {
	return &promiseDrops{start: start, hand: hand, mark: mark, log: log, keptLimit: promisesKeptLimit, open: make(map[*promiseSession]struct{})}
}

func (p *promiseDrops) StartDrop(ctx context.Context, files []clipshare.DroppedFile) (clipshare.DropSession, error) {
	p.starting.Lock()
	defer p.starting.Unlock()
	for _, earlier := range p.sessions() {
		if !earlier.wasTaken() {
			earlier.end()
		}
	}
	if p.isClosed() {
		return nil, errDropsClosed
	}
	session := newPromiseSession(p.hand, p.mark, p.forget)
	session.log, session.keptLimit = p.log, p.keptLimit
	p.hand.dropStarts()
	held := p.hand.holdBack()
	drop, err := p.start(ctx, files, session.write)
	if err != nil {
		held.resume()
		p.hand.dropSettled()
		return nil, err
	}
	if held.resumeHoldingTheLeftButton(false) {
		fmt.Fprintln(p.log, "The button was released before the drop on this machine began, so the drop ends where it began.")
	}
	session.drop = drop
	if !p.keep(session) {
		session.end()
		return nil, errDropsClosed
	}
	go session.follow()
	return session, nil
}

func (p *promiseDrops) sessions() []*promiseSession {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	open := make([]*promiseSession, 0, len(p.open))
	for session := range p.open {
		open = append(open, session)
	}
	return open
}

func (p *promiseDrops) isClosed() bool {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return p.closed
}

func (p *promiseDrops) keep(session *promiseSession) bool {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if p.closed {
		return false
	}
	p.open[session] = struct{}{}
	return true
}

func (p *promiseDrops) forget(session *promiseSession) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	delete(p.open, session)
}

func (p *promiseDrops) close() {
	p.mutex.Lock()
	p.closed = true
	p.mutex.Unlock()
	for _, session := range p.sessions() {
		session.end()
	}
}

type promiseSession struct {
	hand        *dropReplay
	mark        markCopy
	forget      func(*promiseSession)
	log         io.Writer
	keptLimit   time.Duration
	drop        promisedDrop
	arrived     chan struct{}
	arriving    sync.Once
	paths       []string
	taken       chan struct{}
	refused     chan struct{}
	ended       chan struct{}
	ending      sync.Once
	state       sync.Mutex
	tookTheDrop bool
	unwritten   []string
}

func newPromiseSession(hand *dropReplay, mark markCopy, forget func(*promiseSession)) *promiseSession {
	return &promiseSession{
		hand:    hand,
		mark:    mark,
		forget:  forget,
		arrived: make(chan struct{}),
		taken:   make(chan struct{}),
		refused: make(chan struct{}),
		ended:   make(chan struct{}),
	}
}

func (s *promiseSession) Arrived(paths []string) {
	s.arriving.Do(func() {
		s.paths = slices.Clone(paths)
		close(s.arrived)
	})
}

func (s *promiseSession) Cancel() {
	s.end()
}

func (s *promiseSession) Taken() <-chan struct{} {
	return s.taken
}

func (s *promiseSession) EndedWithoutDrop() <-chan struct{} {
	return s.refused
}

func (s *promiseSession) wasTaken() bool {
	s.state.Lock()
	defer s.state.Unlock()
	return s.tookTheDrop
}

func (s *promiseSession) write(ctx context.Context, file int, destination string) error {
	select {
	case <-s.arrived:
	case <-ctx.Done():
		return fmt.Errorf("the dragged files did not arrive: %w", ctx.Err())
	}
	if file < 0 || file >= len(s.paths) {
		return fmt.Errorf("%w: %d of %d", errNoSuchDroppedFile, file, len(s.paths))
	}
	err := copyEntry(ctx, s.paths[file], destination, s.mark)
	if err != nil {
		s.state.Lock()
		s.unwritten = append(s.unwritten, fmt.Sprintf("%s (%s)", filepath.Base(s.paths[file]), forTheLog(err)))
		s.state.Unlock()
	}
	return err
}

func (s *promiseSession) follow() {
	select {
	case <-s.drop.Ended():
	case <-s.ended:
		return
	}
	if !s.drop.Accepted() {
		close(s.refused)
		s.end()
		return
	}
	s.state.Lock()
	s.tookTheDrop = true
	s.state.Unlock()
	s.hand.dropSettled()
	close(s.taken)
	bound := time.NewTimer(s.keptLimit)
	defer bound.Stop()
	select {
	case <-s.drop.Kept():
	case <-s.ended:
		return
	case <-bound.C:
		s.tellUnwritten()
	}
	s.end()
}

func (s *promiseSession) tellUnwritten() {
	s.state.Lock()
	unwritten := slices.Clone(s.unwritten)
	s.state.Unlock()
	if s.log == nil {
		return
	}
	if len(unwritten) == 0 {
		fmt.Fprintln(s.log, "The drop on this machine was taken, and its files were not all written within 2 minutes, so lanhop ends it.")
		return
	}
	fmt.Fprintf(s.log, "The drop on this machine was taken, and its files were not all written within 2 minutes, so lanhop ends it. These files failed: %s\n", strings.Join(unwritten, ", "))
}

func (s *promiseSession) end() {
	s.ending.Do(func() {
		close(s.ended)
		held := s.hand.holdBack()
		s.drop.Close()
		if !s.wasTaken() {
			s.hand.letGoOfTheDropButton()
		}
		s.hand.dropSettled()
		held.resume()
		s.forget(s)
	})
}

func copyEntry(ctx context.Context, from, to string, mark markCopy) error {
	info, err := os.Lstat(from)
	if err != nil {
		return err
	}
	switch {
	case info.IsDir():
		return copyFolder(ctx, from, to, info, mark)
	case info.Mode().IsRegular():
		return copyFile(ctx, from, to, info, mark)
	}
	return fmt.Errorf("%w: %s", errNotFileOrFolder, from)
}

func copyFolder(ctx context.Context, from, to string, info os.FileInfo, mark markCopy) error {
	if err := os.Mkdir(to, info.Mode().Perm()|0o700); err != nil {
		return err
	}
	err := fillFolder(ctx, from, to, mark)
	if err == nil {
		err = finishCopy(from, to, info, mark)
	}
	if err != nil {
		os.RemoveAll(to)
	}
	return err
}

func fillFolder(ctx context.Context, from, to string, mark markCopy) error {
	entries, err := os.ReadDir(from)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := copyEntry(ctx, filepath.Join(from, entry.Name()), filepath.Join(to, entry.Name()), mark); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(ctx context.Context, from, to string, info os.FileInfo, mark markCopy) error {
	source, err := os.Open(from)
	if err != nil {
		return err
	}
	defer source.Close()
	target, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return err
	}
	err = copyUnlessDone(ctx, target, source)
	err = errors.Join(err, target.Close())
	if err == nil {
		err = finishCopy(from, to, info, mark)
	}
	if err != nil {
		os.Remove(to)
	}
	return err
}

func copyUnlessDone(ctx context.Context, to io.Writer, from io.Reader) error {
	buffer := make([]byte, copyChunk)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		read, err := from.Read(buffer)
		if read > 0 {
			if _, werr := to.Write(buffer[:read]); werr != nil {
				return werr
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func finishCopy(from, to string, info os.FileInfo, mark markCopy) error {
	if err := os.Chmod(to, info.Mode().Perm()); err != nil {
		return err
	}
	if err := os.Chtimes(to, info.ModTime(), info.ModTime()); err != nil {
		return err
	}
	if mark == nil {
		return nil
	}
	return mark(from, to)
}
