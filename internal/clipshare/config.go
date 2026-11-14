package clipshare

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/arrival"
	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
	"github.com/rodrigogaraujo/lanhop/internal/features"
	"github.com/rodrigogaraujo/lanhop/internal/identity"
	"github.com/rodrigogaraujo/lanhop/internal/transport"
)

const defaultDataSilenceLimit = 10 * time.Second

var (
	ErrUnreachable = errors.New("clipshare: lanhop cannot reach the clipboard of this machine")

	errClipboardHoldsNoFiles = errors.New("lanhop cannot read or write files on its clipboard")
	errNoArrivalFolder       = errors.New("lanhop has no folder to keep the files that arrive")
	errNoDataConnections     = errors.New("lanhop cannot open connections for files")
)

type Clipboard interface {
	Mark() int64
	Read() (string, error)
	Write(text string) (mark int64, err error)
}

type FileClipboard interface {
	Clipboard
	ReadFiles() ([]string, error)
	WriteFiles(paths []string) (mark int64, err error)
}

type AwaitData func(ctx context.Context, side identity.Fingerprint, announce func(transfer int32) error) (*transport.DataConn, error)

type DialData func(ctx context.Context, transfer int32) (*transport.DataConn, error)

type Config struct {
	Open             func() (Clipboard, error)
	SwitchedOn       func() bool
	FilesOn          func() bool
	Arrivals         *arrival.Folder
	AwaitData        AwaitData
	DialData         DialData
	SilenceLimit     time.Duration
	DataSilenceLimit time.Duration
	Log              io.Writer
	Features         *features.Set
}

func (c Config) withDefaults() Config {
	if c.Open == nil {
		c.Open = func() (Clipboard, error) { return nil, ErrUnreachable }
	}
	if c.SwitchedOn == nil {
		c.SwitchedOn = func() bool { return true }
	}
	if c.FilesOn == nil {
		c.FilesOn = func() bool { return false }
	}
	if c.SilenceLimit == 0 {
		c.SilenceLimit = transport.DefaultSilenceLimit
	}
	if c.DataSilenceLimit == 0 {
		c.DataSilenceLimit = defaultDataSilenceLimit
	}
	if c.Log == nil {
		c.Log = io.Discard
	}
	if c.Features == nil {
		c.Features = new(features.Set)
	}
	return c
}

func (c Config) announcing(kinds clipboard.Kinds) *features.Set {
	c.Features.Turn(features.Clipboard, kinds.Has(clipboard.Text))
	c.Features.Turn(features.Files, kinds.Has(clipboard.Files))
	return c.Features
}

func (c Config) whyNoFiles(clipboardHoldsFiles, opensDataConnections bool) error {
	switch {
	case !clipboardHoldsFiles:
		return errClipboardHoldsNoFiles
	case c.Arrivals == nil:
		return errNoArrivalFolder
	case !opensDataConnections:
		return errNoDataConnections
	}
	return nil
}

type filesNotice struct {
	said bool
}

func (n *filesNotice) possible(log io.Writer, why error) bool {
	if why == nil {
		return true
	}
	if !n.said {
		fmt.Fprintf(log, "File sharing is off on this machine, because %v.\n", why)
		n.said = true
	}
	return false
}

func kindsOn(reachable, textOn, filesOn bool, filesPossible func() bool) clipboard.Kinds {
	if !reachable {
		return 0
	}
	var on clipboard.Kinds
	if textOn {
		on |= clipboard.Text
	}
	if filesOn && filesPossible() {
		on |= clipboard.Files
	}
	return on
}

func listFilesBy(shared clipboard.Kinds, silenceLimit time.Duration) time.Time {
	if !shared.Has(clipboard.Files) {
		return filesNotShared
	}
	return time.Now().Add(silenceLimit / 2)
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
