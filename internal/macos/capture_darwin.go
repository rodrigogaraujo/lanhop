//go:build darwin && cgo

package macos

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/keymap"
)

const (
	handOffLimit        = 10 * time.Second
	permissionCheckTime = time.Second
)

type Capture struct {
	tap     *Tap
	accepts func(Observation) bool
	taken   atomic.Bool
	state   sync.Mutex
	closed  bool
	events  chan event.Event
	stop    chan struct{}
	closing sync.Once
}

func StartCapture(keys keymap.Translator) (*Capture, error) {
	return startCapture(keys, notPostedByLanhop)
}

func startCapture(keys keymap.Translator, accepts func(Observation) bool) (*Capture, error) {
	capture := &Capture{
		accepts: accepts,
		events:  make(chan event.Event),
		stop:    make(chan struct{}),
	}
	tap, err := startTap(seesInputFirst, capture.keeps)
	if err != nil {
		return nil, err
	}
	capture.tap = tap
	delivery := &pump{
		observed:     tap.Observed(),
		lostInput:    tap.Overflowed(),
		stop:         capture.stop,
		accepts:      accepts,
		translator:   newTranslator(keys, Quartz{}),
		events:       capture.events,
		handOffLimit: handOffLimit,
		checkEvery:   permissionCheckTime,
		stillAllowed: func() bool { return IsTrusted() && CanListen() && CanPost() },
		giveBack:     capture.giveUp,
	}
	go delivery.run()
	return capture, nil
}

func (c *Capture) keeps(seen Observation) bool {
	return c.taken.Load() && c.accepts(seen)
}

func (c *Capture) Events() <-chan event.Event {
	return c.events
}

func (c *Capture) Take() error {
	c.state.Lock()
	defer c.state.Unlock()
	if c.closed {
		return nil
	}
	c.taken.Store(true)
	followMouse(false)
	return nil
}

func (c *Capture) GiveBack() error {
	c.state.Lock()
	defer c.state.Unlock()
	c.taken.Store(false)
	followMouse(true)
	return nil
}

func (c *Capture) giveUp() {
	c.state.Lock()
	defer c.state.Unlock()
	c.closed = true
	c.taken.Store(false)
	followMouse(true)
}

func (c *Capture) Close() error {
	c.closing.Do(func() {
		c.state.Lock()
		c.closed = true
		c.taken.Store(false)
		followMouse(true)
		c.state.Unlock()
		close(c.stop)
		c.tap.Close()
	})
	return nil
}
