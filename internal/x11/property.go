//go:build linux

package x11

import (
	"encoding/binary"
	"slices"
	"sync"
	"time"
)

type transfer struct {
	requestor uint32
	property  uint32
	kind      uint32
	data      []byte
	started   time.Time
}

type propertyWriter struct {
	c         *conn
	chunk     int
	incr      uint32
	state     sync.Mutex
	transfers []*transfer
}

func newPropertyWriter(c *conn, incr uint32) *propertyWriter {
	return &propertyWriter{c: c, chunk: min(largestChunk, c.maxRequestBytes-64), incr: incr}
}

func (w *propertyWriter) change(window, property, kind uint32, format byte, data []byte) {
	w.changeMode(window, property, kind, format, data, modeReplace)
}

func (w *propertyWriter) changeMode(window, property, kind uint32, format byte, data []byte, mode byte) {
	units := len(data) / int(format/8)
	w.c.send(newRequest(opChangeProperty, mode).u32(window).u32(property).u32(kind).u8(format).u8(0).u8(0).u8(0).u32(uint32(units)).raw(data).done())
}

func (w *propertyWriter) give(requestor, property, kind uint32, data []byte) {
	if len(data) <= w.chunk {
		w.change(requestor, property, kind, 8, data)
		return
	}
	w.startTransfer(requestor, property, kind, data)
}

func (w *propertyWriter) startTransfer(requestor, property, kind uint32, data []byte) {
	w.c.send(newRequest(opChangeWindowAttributes, 0).u32(requestor).u32(attributeEventMask).u32(propertyChangeMask).done())
	now := time.Now()
	w.state.Lock()
	w.transfers = slices.DeleteFunc(w.transfers, func(t *transfer) bool {
		return now.Sub(t.started) > transferLifetime || (t.requestor == requestor && t.property == property)
	})
	w.transfers = append(w.transfers, &transfer{requestor: requestor, property: property, kind: kind, data: data, started: now})
	w.state.Unlock()
	w.change(requestor, property, w.incr, 32, binary.LittleEndian.AppendUint32(nil, uint32(len(data))))
}

func (w *propertyWriter) propertyDeleted(requestor, property uint32) {
	w.state.Lock()
	index := slices.IndexFunc(w.transfers, func(t *transfer) bool {
		return t.requestor == requestor && t.property == property
	})
	if index < 0 {
		w.state.Unlock()
		return
	}
	current := w.transfers[index]
	part := current.data[:min(w.chunk, len(current.data))]
	current.data = current.data[len(part):]
	lastToThisWindow := false
	if len(part) == 0 {
		w.transfers = slices.Delete(w.transfers, index, index+1)
		lastToThisWindow = !slices.ContainsFunc(w.transfers, func(t *transfer) bool { return t.requestor == requestor })
	}
	w.state.Unlock()
	w.change(requestor, property, current.kind, 8, part)
	if lastToThisWindow {
		w.c.send(newRequest(opChangeWindowAttributes, 0).u32(requestor).u32(attributeEventMask).u32(0).done())
	}
}

func notifySelection(c *conn, when, requestor, selection, target, property uint32) {
	le := binary.LittleEndian
	notify := make([]byte, 32)
	notify[0] = eventSelectionNotify
	le.PutUint32(notify[4:8], when)
	le.PutUint32(notify[8:12], requestor)
	le.PutUint32(notify[12:16], selection)
	le.PutUint32(notify[16:20], target)
	le.PutUint32(notify[20:24], property)
	c.send(newRequest(opSendEvent, 0).u32(requestor).u32(0).raw(notify).done())
}
