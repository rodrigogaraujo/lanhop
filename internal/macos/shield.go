package macos

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

const (
	shieldLimit = time.Second
	shieldPoll  = 20 * time.Millisecond
)

var (
	ErrShieldMissed = errors.New("macos: the window of lanhop did not take the mouse under the cursor, so nothing shields it")
	ErrShieldClosed = errors.New("macos: the shield was closed before it took the mouse")
)

type shieldWindow interface {
	takeTheMouseAt(at Point) (bool, error)
	close() error
}

type Shield struct {
	window  shieldWindow
	closed  *latch
	closing sync.Once
	err     error
}

func newShield(window shieldWindow) *Shield {
	return &Shield{window: window, closed: newLatch()}
}

func (s *Shield) raise(ctx context.Context, cursor func() Point, limit, poll time.Duration) error {
	err := s.waitUntilItTakesTheMouse(ctx, cursor, limit, poll)
	if err != nil {
		s.Close()
	}
	return err
}

func (s *Shield) waitUntilItTakesTheMouse(ctx context.Context, cursor func() Point, limit, poll time.Duration) error {
	deadline := time.Now().Add(limit)
	for {
		if err := s.stopped(ctx); err != nil {
			return err
		}
		taken, err := s.window.takeTheMouseAt(cursor())
		if taken && err == nil {
			return nil
		}
		if !time.Now().Before(deadline) {
			if err != nil {
				return fmt.Errorf("%w: %v", ErrShieldMissed, err)
			}
			return ErrShieldMissed
		}
		timer := time.NewTimer(poll)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-s.closed.done:
			timer.Stop()
			return ErrShieldClosed
		}
	}
}

func (s *Shield) stopped(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.closed.done:
		return ErrShieldClosed
	default:
		return nil
	}
}

func (s *Shield) Close() error {
	if s == nil {
		return nil
	}
	s.closing.Do(func() {
		s.closed.fire()
		s.err = s.window.close()
	})
	return s.err
}
