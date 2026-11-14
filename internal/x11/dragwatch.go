//go:build linux

package x11

import (
	"encoding/binary"
	"errors"
	"slices"
	"time"
)

const xfixesSetSelectionOwner = 0

type DragEventKind int

const (
	DragStarted DragEventKind = iota + 1
	DragEnded
)

type DragEvent struct {
	Kind  DragEventKind
	Paths []string
}

type ownerChange struct {
	reason byte
	owner  uint32
	since  uint32
}

func (o ownerChange) tookTheSelection() bool {
	return o.reason == xfixesSetSelectionOwner && o.owner != atomNone
}

type DragWatch struct {
	c             *conn
	selection     uint32
	targets       uint32
	uriList       uint32
	lanhopsSource uint32
	xfixesEvent   byte
	reader        *selectionReader
	wholeRead     time.Duration
	owners        chan ownerChange
	events        chan DragEvent
	done          chan struct{}
}

func (d *Drags) WatchDrags() (*DragWatch, error) {
	xfixes, firstEvent, err := queryXFixes(d.c)
	if err != nil {
		return nil, err
	}
	w := &DragWatch{
		selection:     d.atoms.selection,
		targets:       d.atoms.targets,
		uriList:       d.atoms.uriList,
		lanhopsSource: d.window,
		xfixesEvent:   firstEvent,
		reader:        newSelectionReader(),
		wholeRead:     d.limits.read,
		owners:        make(chan ownerChange, 1),
		events:        make(chan DragEvent),
		done:          make(chan struct{}),
	}
	c, err := dial(d.display, d.cookie, w.handle)
	if err != nil {
		return nil, err
	}
	w.c = c
	if err := w.watch(xfixes, d.atoms.listProperty, d.atoms.incr); err != nil {
		c.close()
		return nil, err
	}
	go w.run()
	return w, nil
}

func (w *DragWatch) watch(xfixes byte, property, incr uint32) error {
	if err := agreeXFixesVersion(w.c, xfixes); err != nil {
		return err
	}
	if err := w.reader.attach(w.c, w.selection, property, incr); err != nil {
		return err
	}
	if err := w.c.send(newRequest(xfixes, xfixesSelectSelectionInput).u32(w.c.root).u32(w.selection).u32(selectionEventsMask).done()); err != nil {
		return err
	}
	_, err := ownerOf(w.c, w.selection)
	return err
}

func (w *DragWatch) Events() <-chan DragEvent {
	return w.events
}

func (w *DragWatch) Close() error {
	w.c.close()
	<-w.done
	return nil
}

func (w *DragWatch) run() {
	defer close(w.done)
	defer close(w.events)
	started := false
	for {
		select {
		case change := <-w.owners:
			if started {
				if !w.tell(DragEvent{Kind: DragEnded}) {
					return
				}
				started = false
			}
			if !change.tookTheSelection() || change.owner == w.lanhopsSource {
				continue
			}
			paths, err := w.filesOffered(change.since)
			if err != nil {
				continue
			}
			if !w.tell(DragEvent{Kind: DragStarted, Paths: paths}) {
				return
			}
			started = true
		case <-w.c.closed:
			return
		}
	}
}

func (w *DragWatch) tell(event DragEvent) bool {
	select {
	case w.events <- event:
		return true
	case <-w.c.closed:
		return false
	}
}

var errNoAddressList = errors.New("x11: the drag offers no text/uri-list")

func (w *DragWatch) filesOffered(since uint32) ([]string, error) {
	until := time.Now().Add(w.wholeRead)
	offered, err := w.reader.offeredTargets(w.targets, since, until)
	switch {
	case errors.Is(err, errTargetRefused):
	case err != nil:
		return nil, err
	case !slices.Contains(offered, w.uriList):
		return nil, errNoAddressList
	}
	return w.reader.readPaths(w.uriList, since, false, until)
}

func (w *DragWatch) handle(event []byte) {
	le := binary.LittleEndian
	switch event[0] &^ sentEventFlag {
	case w.xfixesEvent:
		if le.Uint32(event[12:16]) == w.selection {
			replace(w.owners, ownerChange{reason: event[1], owner: le.Uint32(event[8:12]), since: le.Uint32(event[20:24])})
		}
	case eventSelectionNotify:
		w.reader.notified(event)
	case eventPropertyNotify:
		w.reader.propertyChanged(le.Uint32(event[4:8]), le.Uint32(event[8:12]), event[16])
	}
}
