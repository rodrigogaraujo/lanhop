package follow

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/features"
	"github.com/rodrigogaraujo/lanhop/internal/receiver"
	"github.com/rodrigogaraujo/lanhop/internal/update"
)

const DefaultQuietAfterLeave = 6 * time.Second

type Prepared interface {
	Install() error
	Discard()
}

type Updater interface {
	Prepare(ctx context.Context, want update.Version) (Prepared, error)
}

type SideConfig struct {
	Running         update.Version
	Following       func() bool
	Features        *features.Set
	Updater         Updater
	Restart         func()
	Busy            func() bool
	QuietAfterLeave time.Duration
	Log             io.Writer
}

type Side struct {
	config SideConfig
	ctx    context.Context

	mutex     sync.Mutex
	conn      receiver.Connection
	following bool
	focused   bool
	lastLeave time.Time
	wanted    update.Version
	preparing bool
	prepared  Prepared
	waited    bool
	quietTry  *time.Timer
	done      bool
}

func NewSide(ctx context.Context, config SideConfig) *Side {
	if config.Busy == nil {
		config.Busy = func() bool { return false }
	}
	if config.QuietAfterLeave == 0 {
		config.QuietAfterLeave = DefaultQuietAfterLeave
	}
	if config.Log == nil {
		config.Log = io.Discard
	}
	if config.Features == nil {
		config.Features = new(features.Set)
	}
	side := &Side{config: config, ctx: ctx}
	side.following = side.decides()
	config.Features.Turn(features.Follows, side.following)
	return side
}

func (s *Side) decides() bool {
	return s.config.Running != (update.Version{}) && s.config.Following()
}

func (s *Side) Connected(conn receiver.Connection) func(event.Event) {
	s.mutex.Lock()
	s.conn = conn
	s.mutex.Unlock()
	conn.Send(event.Features(s.config.Features.Value()), event.Version(s.config.Running.Value()))
	return s.heard
}

func (s *Side) FocusArrives() {
	s.mutex.Lock()
	s.focused = true
	s.mutex.Unlock()
	s.followTheSwitch()
}

func (s *Side) FocusLeaves() {
	s.mutex.Lock()
	s.focused = false
	s.lastLeave = time.Now()
	s.mutex.Unlock()
	s.followTheSwitch()
	s.tryLater(s.config.QuietAfterLeave)
}

func (s *Side) Lost() {
	s.mutex.Lock()
	s.conn = nil
	s.focused = false
	s.mutex.Unlock()
}

func (s *Side) followTheSwitch() {
	now := s.decides()
	s.mutex.Lock()
	changed := now != s.following
	s.following = now
	conn := s.conn
	s.mutex.Unlock()
	if !changed {
		return
	}
	value := s.config.Features.Turn(features.Follows, now)
	if conn != nil {
		conn.Send(event.Features(value))
	}
}

func (s *Side) heard(frame event.Event) {
	if !frame.IsControl() || frame.Code != event.ControlUpdate {
		return
	}
	want, valid := update.VersionFromValue(frame.Value)
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if !valid || !s.following || s.done || want.Compare(s.config.Running) <= 0 || want == s.wanted {
		return
	}
	s.wanted = want
	s.preparing = true
	go s.prepare(want)
}

func (s *Side) prepare(want update.Version) {
	prepared, err := s.config.Updater.Prepare(s.ctx, want)
	s.mutex.Lock()
	s.preparing = false
	if err != nil {
		s.wanted = update.Version{}
		s.mutex.Unlock()
		fmt.Fprintf(s.config.Log, "The machine with the keyboard asked for lanhop %s, and it was not installed: %v\n", want, err)
		return
	}
	if s.done || want != s.wanted {
		s.mutex.Unlock()
		prepared.Discard()
		return
	}
	s.prepared = prepared
	s.mutex.Unlock()
	s.tryNow()
}

func (s *Side) tryLater(after time.Duration) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.quietTry != nil {
		s.quietTry.Stop()
	}
	s.quietTry = time.AfterFunc(after, s.tryNow)
}

func (s *Side) tryNow() {
	s.mutex.Lock()
	prepared, want := s.prepared, s.wanted
	if prepared == nil || s.done {
		s.mutex.Unlock()
		return
	}
	if wait := s.timeUntilQuiet(); wait > 0 {
		s.waited = true
		s.mutex.Unlock()
		s.tryLater(wait)
		return
	}
	waited := s.waited
	s.prepared = nil
	s.done = true
	s.mutex.Unlock()

	if waited {
		prepared.Discard()
		fresh, err := s.config.Updater.Prepare(s.ctx, want)
		if err != nil {
			s.giveUp(want, err)
			return
		}
		prepared = fresh
	}
	if err := prepared.Install(); err != nil {
		s.giveUp(want, err)
		return
	}
	fmt.Fprintf(s.config.Log, "lanhop %s is installed. lanhop ends now, and its service starts the new version.\n", want)
	s.config.Restart()
}

func (s *Side) timeUntilQuiet() time.Duration {
	if s.focused || s.config.Busy() {
		return s.config.QuietAfterLeave
	}
	if s.lastLeave.IsZero() {
		return 0
	}
	return time.Until(s.lastLeave.Add(s.config.QuietAfterLeave))
}

func (s *Side) giveUp(want update.Version, err error) {
	s.mutex.Lock()
	s.done = false
	s.waited = false
	s.wanted = update.Version{}
	s.mutex.Unlock()
	fmt.Fprintf(s.config.Log, "The machine with the keyboard asked for lanhop %s, and it was not installed: %v\n", want, err)
}
