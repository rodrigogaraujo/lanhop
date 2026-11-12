//go:build darwin && cgo

package macos

import (
	"sync"
	"sync/atomic"
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
	accepts func(Observation) bool
	hotkey  *hotkeyGuard
	taken   atomic.Bool
	state   sync.Mutex
	closed  bool
	tapGone bool
	events  chan event.Event
	stop    chan struct{}
	closing sync.Once
}

func StartCapture(keys keymap.Translator, hotkey Flags) (*Capture, error) {
	return startCapture(keys, hotkey, notPostedByLanhop)
}

func startCapture(keys keymap.Translator, hotkey Flags, accepts func(Observation) bool) (*Capture, error) {
	capture := &Capture{
		accepts: accepts,
		hotkey:  newHotkeyGuard(hotkey),
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
	if !c.accepts(seen) {
		return false
	}
	return c.hotkey.keeps(seen) || c.taken.Load()
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
	c.tap.holdCursor(true)
	c.taken.Store(true)
	return nil
}

func (c *Capture) letGo() {
	c.taken.Store(false)
	if !c.tapGone {
		c.tap.holdCursor(false)
	}
}

func (c *Capture) GiveBack() error {
	c.state.Lock()
	defer c.state.Unlock()
	c.letGo()
	return nil
}

func (c *Capture) Cursor() (x, y int, desktop edge.Bounds, known bool) {
	return cursorOn(Quartz{})
}

func (c *Capture) PlaceCursor(x, y int) {
	placeCursorOn(Quartz{}, x, y)
}

func (c *Capture) giveUp() {
	c.state.Lock()
	defer c.state.Unlock()
	c.closed = true
	c.letGo()
}

func (c *Capture) Close() error {
	c.closing.Do(func() {
		c.state.Lock()
		c.closed = true
		c.letGo()
		c.tapGone = true
		c.state.Unlock()
		close(c.stop)
		c.tap.Close()
	})
	return nil
}
