package receiver

import (
	"context"
	"errors"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/keystate"
	"github.com/rodrigogaraujo/lanhop/internal/transport"
	"github.com/rodrigogaraujo/lanhop/internal/trust"
)

type Replay interface {
	Emit(events ...event.Event) error
	Close() error
}

type Connection interface {
	Send(events ...event.Event) error
	Close() error
}

type Clipboard interface {
	Connected(conn Connection) (received func(frame event.Event))
	FocusArrives()
	FocusLeaves()
	Lost()
}

type noClipboard struct{}

func (noClipboard) Connected(Connection) func(event.Event) { return func(event.Event) {} }
func (noClipboard) FocusArrives()                          {}
func (noClipboard) FocusLeaves()                           {}
func (noClipboard) Lost()                                  {}

type together []Clipboard

func Together(listeners ...Clipboard) Clipboard {
	var present together
	for _, listener := range listeners {
		if listener != nil {
			present = append(present, listener)
		}
	}
	return present
}

func (t together) Connected(conn Connection) func(event.Event) {
	received := make([]func(event.Event), len(t))
	for i, listener := range t {
		received[i] = listener.Connected(conn)
	}
	return func(frame event.Event) {
		for _, each := range received {
			each(frame)
		}
	}
}

func (t together) FocusArrives() {
	for _, listener := range t {
		listener.FocusArrives()
	}
}

func (t together) FocusLeaves() {
	for _, listener := range t {
		listener.FocusLeaves()
	}
}

func (t together) Lost() {
	for _, listener := range t {
		listener.Lost()
	}
}

type Config struct {
	Transport transport.Config
	Sharer    trust.Peer
	Replay    Replay
	Clipboard Clipboard
	OnConnect func()
	OnFailure func(reason error)
}

func (c Config) withDefaults() Config {
	if c.OnConnect == nil {
		c.OnConnect = func() {}
	}
	if c.OnFailure == nil {
		c.OnFailure = func(error) {}
	}
	if c.Clipboard == nil {
		c.Clipboard = noClipboard{}
	}
	return c
}

type retryDelays struct {
	first   time.Duration
	longest time.Duration
}

var defaultRetryDelays = retryDelays{first: time.Second, longest: 5 * time.Second}

func (d retryDelays) after(previous time.Duration) time.Duration {
	return min(2*previous, d.longest)
}

func Run(ctx context.Context, config Config) error {
	return run(ctx, config, defaultRetryDelays)
}

func run(ctx context.Context, config Config, delays retryDelays) error {
	config = config.withDefaults()
	local := &machine{replay: config.Replay, clipboard: config.Clipboard}
	err := local.receiveUntilStopped(ctx, config, delays)
	return errors.Join(err, config.Replay.Close())
}

type machine struct {
	replay      Replay
	clipboard   Clipboard
	toClipboard func(frame event.Event)
	held        keystate.Tracker
	focused     bool
	reportOpen  bool
}

func (m *machine) receiveUntilStopped(ctx context.Context, config Config, delays retryDelays) error {
	delay := delays.first
	for {
		conn, failure := transport.Dial(ctx, config.Transport, config.Sharer)
		if failure == nil {
			config.OnConnect()
			delay = delays.first
			var broken error
			failure, broken = m.replayFrom(ctx, conn)
			if broken != nil {
				return broken
			}
		}
		if ctx.Err() != nil {
			return nil
		}
		config.OnFailure(failure)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(delay):
		}
		delay = delays.after(delay)
	}
}

func (m *machine) replayFrom(ctx context.Context, conn *transport.Conn) (lost, broken error) {
	stopWatching := context.AfterFunc(ctx, func() { conn.Close() })
	defer stopWatching()
	defer conn.Close()
	m.toClipboard = m.clipboard.Connected(conn)
	defer m.clipboard.Lost()
	for {
		received, err := conn.Receive()
		if err != nil {
			return err, m.loseFocus()
		}
		if err := m.handle(received); err != nil {
			return nil, err
		}
	}
}

func (m *machine) handle(received event.Event) error {
	if !received.IsControl() {
		return m.replayIfFocused(received)
	}
	switch received.Code {
	case event.ControlEnter:
		m.focused = true
		m.clipboard.FocusArrives()
	case event.ControlLeave:
		err := m.loseFocus()
		m.clipboard.FocusLeaves()
		return err
	default:
		m.toClipboard(received)
	}
	return nil
}

func (m *machine) replayIfFocused(input event.Event) error {
	if !m.focused {
		return nil
	}
	m.held.Observe(input)
	m.reportOpen = input != event.Sync()
	return m.replay.Emit(input)
}

func (m *machine) loseFocus() error {
	m.focused = false
	ending := m.held.ReleaseAll()
	if len(ending) == 0 && m.reportOpen {
		ending = []event.Event{event.Sync()}
	}
	m.reportOpen = false
	return m.replay.Emit(ending...)
}
