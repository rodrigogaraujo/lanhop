package main

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/clipshare"
	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/receiver"
)

const (
	leftButtonOfTheReplay = 0
	longestHoldBack       = 10 * time.Second
	releaseBeforeTheDrop  = 3 * time.Second
)

type localDrag struct {
	Started bool
	Paths   []string
}

func openDrops(central bool) (clipshare.Drops, func(), error) {
	return openDropsFor(central, nil, os.Stdout)
}

type buttonKeeper interface {
	HoldButton(button int)
	ForgetButton(button int)
}

type dropReplay struct {
	mutex           sync.Mutex
	replay          receiver.Replay
	replayed        func()
	now             func() time.Time
	holdLimit       time.Duration
	holding         int
	queued          []event.Event
	failure         error
	closed          bool
	dropPending     bool
	dropHolds       bool
	dropPosts       bool
	leftByPerson    bool
	orphanRelease   time.Time
	escapeSwallowed bool
}

func newDropReplay(replay receiver.Replay, replayed func()) *dropReplay {
	return &dropReplay{replay: replay, replayed: replayed, now: time.Now, holdLimit: longestHoldBack}
}

func (r *dropReplay) Emit(events ...event.Event) error {
	r.mutex.Lock()
	passing := make([]event.Event, 0, len(events))
	for _, e := range events {
		if !r.swallows(e) {
			passing = append(passing, e)
		}
	}
	var reset bool
	var err error
	if r.holding > 0 {
		r.queued = append(r.queued, passing...)
	} else {
		reset, err = r.pass(passing, true)
	}
	if err == nil {
		err, r.failure = r.failure, nil
	}
	r.mutex.Unlock()
	if reset && r.replayed != nil {
		r.replayed()
	}
	return err
}

func (r *dropReplay) Close() error {
	r.mutex.Lock()
	r.closed, r.queued = true, nil
	r.mutex.Unlock()
	return r.replay.Close()
}

func (r *dropReplay) swallows(e event.Event) bool {
	if e.Type != event.TypeKey || e.Code != event.KeyEsc {
		return false
	}
	switch {
	case r.escapeSwallowed && e.Value == event.KeyReleased:
		r.escapeSwallowed = false
		return true
	case r.escapeSwallowed:
		return true
	case r.dropPending && e.Value == event.KeyPressed:
		r.escapeSwallowed = true
		return true
	}
	return false
}

func (r *dropReplay) pass(events []event.Event, fromThePerson bool) (reset bool, err error) {
	if len(events) == 0 || r.closed {
		return false, nil
	}
	for _, e := range events {
		if r.follow(e) && fromThePerson {
			reset = true
		}
	}
	return reset, r.replay.Emit(events...)
}

func (r *dropReplay) follow(e event.Event) (resetsLocalDrags bool) {
	if e.Type != event.TypeKey {
		return false
	}
	switch {
	case e.Code == event.ButtonLeft && e.Value == event.KeyPressed:
		r.leftByPerson, r.dropHolds, r.orphanRelease = true, false, time.Time{}
		return true
	case e.Code == event.ButtonLeft && e.Value == event.KeyReleased:
		switch {
		case r.dropHolds:
			r.dropHolds = false
		case !r.leftByPerson:
			r.orphanRelease = r.now()
		}
		r.leftByPerson = false
		return true
	case e.Code == event.KeyEsc && e.Value == event.KeyPressed:
		return true
	}
	return false
}

type heldBack struct {
	r      *dropReplay
	once   sync.Once
	timing sync.Mutex
	timer  *time.Timer
}

func (r *dropReplay) holdBack() *heldBack {
	if r == nil {
		return nil
	}
	r.mutex.Lock()
	r.holding++
	r.mutex.Unlock()
	h := &heldBack{r: r}
	h.timing.Lock()
	h.timer = time.AfterFunc(r.holdLimit, h.resume)
	h.timing.Unlock()
	return h
}

func (h *heldBack) resume() {
	h.end(nil)
}

func (h *heldBack) resumeHoldingTheLeftButton(post bool) (releasedEarlier bool) {
	adopted := false
	h.end(func(r *dropReplay) {
		adopted = true
		releasedEarlier = r.holdTheLeftButton(post)
	})
	if adopted || h == nil {
		return releasedEarlier
	}
	h.r.mutex.Lock()
	defer h.r.mutex.Unlock()
	return h.r.holdTheLeftButton(post)
}

func (h *heldBack) end(first func(r *dropReplay)) {
	if h == nil {
		return
	}
	h.once.Do(func() {
		h.timing.Lock()
		h.timer.Stop()
		h.timing.Unlock()
		r := h.r
		r.mutex.Lock()
		if first != nil {
			first(r)
		}
		r.holding--
		var reset bool
		if r.holding == 0 {
			queued := r.queued
			r.queued = nil
			var err error
			reset, err = r.pass(queued, true)
			r.keep(err)
		}
		r.mutex.Unlock()
		if reset && r.replayed != nil {
			r.replayed()
		}
	})
}

func (r *dropReplay) keep(err error) {
	if err != nil && r.failure == nil {
		r.failure = err
	}
}

func (r *dropReplay) holdTheLeftButton(post bool) (releasedEarlier bool) {
	if r.closed {
		return false
	}
	if post {
		r.keep(r.replay.Emit(event.Key(event.ButtonLeft, event.KeyPressed), event.Sync()))
	} else if keeper, ok := r.replay.(buttonKeeper); ok {
		keeper.HoldButton(leftButtonOfTheReplay)
	}
	r.dropHolds, r.dropPosts = true, post
	if r.orphanRelease.IsZero() || r.now().Sub(r.orphanRelease) > releaseBeforeTheDrop {
		return false
	}
	r.orphanRelease, r.dropHolds = time.Time{}, false
	r.keep(r.replay.Emit(event.Key(event.ButtonLeft, event.KeyReleased), event.Sync()))
	return true
}

func (r *dropReplay) dropStarts() {
	if r == nil {
		return
	}
	r.mutex.Lock()
	r.dropPending = true
	r.mutex.Unlock()
}

func (r *dropReplay) dropSettled() {
	if r == nil {
		return
	}
	r.mutex.Lock()
	r.dropPending = false
	r.mutex.Unlock()
}

func (r *dropReplay) letGoOfTheDropButton() {
	if r == nil {
		return
	}
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if !r.dropHolds {
		return
	}
	r.dropHolds = false
	if r.closed {
		return
	}
	if r.dropPosts {
		r.keep(r.replay.Emit(event.Key(event.ButtonLeft, event.KeyReleased), event.Sync()))
	} else if keeper, ok := r.replay.(buttonKeeper); ok {
		keeper.ForgetButton(leftButtonOfTheReplay)
	}
}

func (r *dropReplay) holdsTheDropButton() bool {
	if r == nil {
		return false
	}
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return r.dropHolds
}

func (r *dropReplay) forgetTheLeftButton() {
	if r == nil {
		return
	}
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.leftByPerson, r.dropHolds = false, false
	if keeper, ok := r.replay.(buttonKeeper); ok {
		keeper.ForgetButton(leftButtonOfTheReplay)
	}
}

func (r *dropReplay) emitForLanhop(events ...event.Event) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	_, err := r.pass(events, false)
	return err
}

func forwardDrags[E any](ctx context.Context, events <-chan E, convert func(E) localDrag, closeWatch func()) (<-chan localDrag, func()) {
	drags := make(chan localDrag)
	stop := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		defer close(drags)
		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case seen, open := <-events:
				if !open {
					return
				}
				select {
				case drags <- convert(seen):
				case <-ctx.Done():
					return
				case <-stop:
					return
				}
			}
		}
	}()
	var stopping sync.Once
	return drags, func() {
		stopping.Do(func() {
			close(stop)
			closeWatch()
			<-stopped
		})
	}
}
