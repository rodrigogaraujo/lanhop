//go:build darwin && cgo

package macos

import (
	"sync"
	"sync/atomic"

	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/keymap"
)

type Capture struct {
	tap     *Tap
	accepts func(Observation) bool
	taken   atomic.Bool
	events  chan event.Event
	stop    chan struct{}
	stopped chan struct{}
	closing sync.Once
}

func StartCapture(keys keymap.Translator) (*Capture, error) {
	return startCapture(keys, func(seen Observation) bool { return seen.Marker != Marker })
}

func startCapture(keys keymap.Translator, accepts func(Observation) bool) (*Capture, error) {
	capture := &Capture{
		accepts: accepts,
		events:  make(chan event.Event),
		stop:    make(chan struct{}),
		stopped: make(chan struct{}),
	}
	tap, err := startTap(seesInputFirst, capture.keeps)
	if err != nil {
		return nil, err
	}
	capture.tap = tap
	go capture.deliver(newTranslator(keys))
	return capture, nil
}

func (c *Capture) keeps(seen Observation) bool {
	return c.taken.Load() && c.accepts(seen)
}

func (c *Capture) deliver(translator *translator) {
	defer close(c.stopped)
	defer close(c.events)
	for {
		select {
		case <-c.stop:
			return
		case seen, open := <-c.tap.Observed():
			if !open {
				return
			}
			if !c.accepts(seen) {
				continue
			}
			for _, translated := range translator.translate(seen) {
				select {
				case c.events <- translated:
				case <-c.stop:
					return
				}
			}
		}
	}
}

func (c *Capture) Events() <-chan event.Event {
	return c.events
}

func (c *Capture) Take() error {
	c.taken.Store(true)
	followMouse(false)
	return nil
}

func (c *Capture) GiveBack() error {
	c.taken.Store(false)
	followMouse(true)
	return nil
}

func (c *Capture) Close() error {
	c.closing.Do(func() {
		c.GiveBack()
		close(c.stop)
		c.tap.Close()
		<-c.stopped
	})
	return nil
}
