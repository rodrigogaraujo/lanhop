//go:build darwin && cgo

package macos

import (
	"sync"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/edge"
	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/keymap"
)

const (
	handOffLimit        = 10 * time.Second
	permissionCheckTime = time.Second
)

type Capture struct {
	tap     *Tap
	hold    *hold
	state   sync.Mutex
	closed  bool
	events  chan event.Event
	stop    chan struct{}
	closing sync.Once
}

func StartCapture(keys keymap.Translator, hotkey Flags) (*Capture, error) {
	return startCapture(keys, hotkey, notPostedByLanhop)
}

func startCapture(keys keymap.Translator, hotkey Flags, accepts func(Observation) bool) (*Capture, error) {
	capture := &Capture{
		hold:   &hold{holdingScreen: Quartz{}, accepts: accepts, hotkey: newHotkeyGuard(hotkey)},
		events: make(chan event.Event),
		stop:   make(chan struct{}),
	}
	tap, err := startTap(seesInputFirst, capture.hold.keeps)
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

func (c *Capture) Events() <-chan event.Event {
	return c.events
}

func (c *Capture) Take() error {
	c.state.Lock()
	defer c.state.Unlock()
	if c.closed {
		return nil
	}
	c.hold.take()
	return nil
}

func (c *Capture) GiveBack() error {
	c.state.Lock()
	defer c.state.Unlock()
	c.hold.letGo()
	return nil
}

func (c *Capture) Cursor() (x, y int, desktop edge.Bounds, known bool) {
	return cursorOn(Quartz{})
}

func (c *Capture) PlaceCursor(x, y int) {
	placeCursorOn(c.hold, x, y)
}

func (c *Capture) giveUp() {
	c.state.Lock()
	defer c.state.Unlock()
	c.closed = true
	c.hold.letGo()
}

func (c *Capture) Close() error {
	c.closing.Do(func() {
		c.state.Lock()
		c.closed = true
		c.hold.letGo()
		c.state.Unlock()
		close(c.stop)
		c.tap.Close()
	})
	return nil
}
