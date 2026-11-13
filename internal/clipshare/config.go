package clipshare

import (
	"errors"
	"io"
	"sync"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/features"
	"github.com/rodrigogaraujo/lanhop/internal/transport"
)

var ErrUnreachable = errors.New("clipshare: lanhop cannot reach the clipboard of this machine")

type Clipboard interface {
	Mark() int64
	Read() (string, error)
	Write(text string) (mark int64, err error)
}

type Config struct {
	Open         func() (Clipboard, error)
	SwitchedOn   func() bool
	SilenceLimit time.Duration
	Log          io.Writer
	Features     *features.Set
}

func (c Config) withDefaults() Config {
	if c.Open == nil {
		c.Open = func() (Clipboard, error) { return nil, ErrUnreachable }
	}
	if c.SwitchedOn == nil {
		c.SwitchedOn = func() bool { return true }
	}
	if c.SilenceLimit == 0 {
		c.SilenceLimit = transport.DefaultSilenceLimit
	}
	if c.Log == nil {
		c.Log = io.Discard
	}
	if c.Features == nil {
		c.Features = new(features.Set)
	}
	return c
}

func (c Config) announcement(on bool) event.Event {
	return event.Features(c.Features.Turn(features.Clipboard, on))
}

func takesCopies(features event.Event) bool {
	return features.Value&event.FeatureClipboard != 0
}

type mailbox[T any] struct {
	mutex  sync.Mutex
	queued []T
	closed bool
	ready  chan struct{}
}

func newMailbox[T any]() *mailbox[T] {
	return &mailbox[T]{ready: make(chan struct{}, 1)}
}

func (m *mailbox[T]) put(item T) {
	m.mutex.Lock()
	if m.closed {
		m.mutex.Unlock()
		return
	}
	m.queued = append(m.queued, item)
	m.mutex.Unlock()
	select {
	case m.ready <- struct{}{}:
	default:
	}
}

func (m *mailbox[T]) take() []T {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	taken := m.queued
	m.queued = nil
	return taken
}

func (m *mailbox[T]) close() {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.closed = true
	m.queued = nil
}
