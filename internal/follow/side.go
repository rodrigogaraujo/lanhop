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
	busy      bool
	prepared  Prepared
	fresh     bool
	quietTry  *time.Timer
	ended     bool
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
	s.followTheSwitch()
	s.mutex.Lock()
	s.conn = conn
	s.mutex.Unlock()
	s.announce(conn)
	conn.Send(event.Version(s.config.Running.Value()))
	return s.heard
}

type featuresSender interface {
	SendFeatures(announced *features.Set) error
}

func (s *Side) announce(conn receiver.Connection) {
	if sender, sendsTheLatest := conn.(featuresSender); sendsTheLatest {
		sender.SendFeatures(s.config.Features)
		return
	}
	conn.Send(event.Features(s.config.Features.Value()))
}

func (s *Side) FocusArrives() {
	s.mutex.Lock()
	s.focused = true
	s.mutex.Unlock()
	s.followTheSwitch()
}

func (s *Side) FocusLeaves() {
	s.leaves()
	s.followTheSwitch()
	s.tryLater(s.config.QuietAfterLeave)
}

func (s *Side) Lost() {
	s.leaves()
	s.mutex.Lock()
	s.conn = nil
	s.mutex.Unlock()
	s.tryLater(s.config.QuietAfterLeave)
}

func (s *Side) leaves() {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.focused || s.lastLeave.IsZero() {
		s.lastLeave = time.Now()
	}
	s.focused = false
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
	s.config.Features.Turn(features.Follows, now)
	if conn != nil {
		s.announce(conn)
	}
}

func (s *Side) heard(frame event.Event) {
	if !frame.IsControl() || frame.Code != event.ControlUpdate {
		return
	}
	want, valid := update.VersionFromValue(frame.Value)
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if !valid || !s.following || s.ended || s.busy || s.prepared != nil || want.Compare(s.config.Running) <= 0 {
		return
	}
	s.wanted = want
	s.busy = true
	go s.prepare(want)
}

func (s *Side) prepare(want update.Version) {
	prepared, err := s.config.Updater.Prepare(s.ctx, want)
	s.mutex.Lock()
	s.busy = false
	if err != nil {
		s.wanted = update.Version{}
		s.mutex.Unlock()
		s.log(want, err)
		return
	}
	if s.ended || want != s.wanted {
		s.mutex.Unlock()
		prepared.Discard()
		return
	}
	s.prepared = prepared
	s.fresh = true
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
	following := s.decides()
	busy := s.config.Busy()
	s.mutex.Lock()
	prepared, want := s.prepared, s.wanted
	if prepared == nil || s.ended || s.busy {
		s.mutex.Unlock()
		return
	}
	if s.ctx.Err() != nil || !following {
		s.prepared = nil
		s.wanted = update.Version{}
		s.mutex.Unlock()
		prepared.Discard()
		return
	}
	if wait := s.timeUntilQuiet(busy); wait > 0 {
		s.fresh = false
		s.mutex.Unlock()
		s.tryLater(wait)
		return
	}
	if !s.fresh {
		s.prepared = nil
		s.busy = true
		s.mutex.Unlock()
		prepared.Discard()
		go s.prepare(want)
		return
	}
	s.prepared = nil
	s.ended = true
	s.mutex.Unlock()

	if err := prepared.Install(); err != nil {
		s.mutex.Lock()
		s.ended = false
		s.wanted = update.Version{}
		s.mutex.Unlock()
		s.log(want, err)
		return
	}
	fmt.Fprintf(s.config.Log, "lanhop %s is installed. lanhop ends now, and its service starts the new version.\n", want)
	s.config.Restart()
}

func (s *Side) timeUntilQuiet(busy bool) time.Duration {
	if s.focused || busy {
		return s.config.QuietAfterLeave
	}
	if s.lastLeave.IsZero() {
		return 0
	}
	return time.Until(s.lastLeave.Add(s.config.QuietAfterLeave))
}

func (s *Side) log(want update.Version, err error) {
	fmt.Fprintf(s.config.Log, "The machine with the keyboard asked for lanhop %s, and it was not installed: %v\n", want, err)
}
