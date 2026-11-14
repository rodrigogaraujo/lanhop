//go:build linux

package x11

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
)

const targetsLimit = 4096

var errTargetRefused = errors.New("x11: the owner of the selection refused that format")

type selectionNotify struct {
	target   uint32
	property uint32
}

type retiredWindow struct {
	window uint32
	since  time.Time
}

type selectionReader struct {
	c           *conn
	selection   uint32
	property    uint32
	incr        uint32
	notifies    chan selectionNotify
	newValues   chan uint32
	readTimeout time.Duration
	reader      atomic.Uint32
	retired     []retiredWindow
}

func newSelectionReader() *selectionReader {
	return &selectionReader{
		notifies:    make(chan selectionNotify, 8),
		newValues:   make(chan uint32, 64),
		readTimeout: answerTimeout,
	}
}

func (r *selectionReader) attach(c *conn, selection, property, incr uint32) error {
	r.c, r.selection, r.property, r.incr = c, selection, property, incr
	return r.openReader()
}

func (r *selectionReader) notified(event []byte) {
	le := binary.LittleEndian
	push(r.notifies, selectionNotify{target: le.Uint32(event[16:20]), property: le.Uint32(event[20:24])})
}

func (r *selectionReader) propertyChanged(window, property uint32, state byte) bool {
	if window != r.reader.Load() {
		return false
	}
	if state == propertyNewValue {
		push(r.newValues, property)
	}
	return true
}

func (r *selectionReader) offeredTargets(targets, when uint32, until time.Time) ([]uint32, error) {
	data, err := r.convertAt(targets, when, targetsLimit, until)
	if err != nil {
		return nil, err
	}
	return atomsIn(data), nil
}

func (r *selectionReader) readPaths(target, when uint32, startsWithCopyOrCut bool, until time.Time) ([]string, error) {
	list, err := r.convertAt(target, when, fileListLimit, until)
	switch {
	case errors.Is(err, errTargetRefused):
		return nil, clipboard.ErrNotFiles
	case errors.Is(err, clipboard.ErrTooLarge):
		return nil, fmt.Errorf("%w: the list is longer than %d bytes", clipboard.ErrTooManyFiles, fileListLimit)
	case err != nil:
		return nil, err
	}
	return pathsInList(string(list), startsWithCopyOrCut)
}

func atomsIn(data []byte) []uint32 {
	atoms := make([]uint32, 0, len(data)/4)
	for i := 0; i+4 <= len(data); i += 4 {
		atoms = append(atoms, binary.LittleEndian.Uint32(data[i:i+4]))
	}
	return atoms
}

func (r *selectionReader) openReader() error {
	window := r.c.newID()
	create := newRequest(opCreateWindow, 0).u32(window).u32(r.c.root).
		u16(0).u16(0).u16(1).u16(1).u16(0).u16(classInputOnly).u32(0).
		u32(attributeEventMask).u32(propertyChangeMask).done()
	if err := r.c.send(create); err != nil {
		return err
	}
	r.reader.Store(window)
	return nil
}

func (r *selectionReader) leaveTheOwnerNothingToWriteInto() {
	abandoned := r.reader.Load()
	if err := r.openReader(); err != nil {
		return
	}
	now := time.Now()
	for len(r.retired) > 0 && (now.Sub(r.retired[0].since) > transferLifetime || len(r.retired) >= retiredAtMost) {
		r.c.send(newRequest(opDestroyWindow, 0).u32(r.retired[0].window).done())
		r.retired = r.retired[1:]
	}
	r.retired = append(r.retired, retiredWindow{window: abandoned, since: now})
}

func (r *selectionReader) connectionLost() bool {
	select {
	case <-r.c.closed:
		return true
	default:
		return false
	}
}

func (r *selectionReader) convert(target uint32, limit int, until time.Time) ([]byte, error) {
	return r.convertAt(target, currentTime, limit, until)
}

func (r *selectionReader) convertAt(target, when uint32, limit int, until time.Time) ([]byte, error) {
	if !time.Now().Before(until) {
		return nil, ErrNoAnswer
	}
	data, err := r.askTheOwner(target, when, limit, until)
	if err != nil && !errors.Is(err, errTargetRefused) && !r.connectionLost() {
		r.leaveTheOwnerNothingToWriteInto()
	}
	return data, err
}

func (r *selectionReader) askTheOwner(target, when uint32, limit int, until time.Time) ([]byte, error) {
	reader := r.reader.Load()
	drain(r.notifies)
	if err := r.c.send(newRequest(opDeleteProperty, 0).u32(reader).u32(r.property).done()); err != nil {
		return nil, err
	}
	request := newRequest(opConvertSelection, 0).u32(reader).u32(r.selection).u32(target).u32(r.property).u32(when).done()
	if err := r.c.send(request); err != nil {
		return nil, err
	}
	deadline := r.nextStepDeadline(until)
	for {
		select {
		case got := <-r.notifies:
			if got.target != target {
				continue
			}
			if got.property == atomNone {
				return nil, errTargetRefused
			}
			drain(r.newValues)
			return r.receive(limit, until)
		case <-deadline:
			return nil, ErrNoAnswer
		case <-r.c.closed:
			return nil, r.c.err()
		}
	}
}

func (r *selectionReader) receive(limit int, until time.Time) ([]byte, error) {
	kind, data, tooLarge, err := r.takeProperty(limit)
	if err != nil {
		return nil, err
	}
	if kind != r.incr {
		if tooLarge {
			return nil, clipboard.ErrTooLarge
		}
		return data, nil
	}
	var whole []byte
	for {
		if err := r.waitForNewValue(r.nextStepDeadline(until)); err != nil {
			return nil, err
		}
		_, part, tooLarge, err := r.takeProperty(limit - len(whole))
		if err != nil {
			return nil, err
		}
		if tooLarge {
			return nil, clipboard.ErrTooLarge
		}
		if len(part) == 0 {
			return whole, nil
		}
		whole = append(whole, part...)
	}
}

func (r *selectionReader) nextStepDeadline(until time.Time) <-chan time.Time {
	return time.After(min(r.readTimeout, time.Until(until)))
}

func (r *selectionReader) waitForNewValue(deadline <-chan time.Time) error {
	for {
		select {
		case property := <-r.newValues:
			if property == r.property {
				return nil
			}
		case <-deadline:
			return ErrNoAnswer
		case <-r.c.closed:
			return r.c.err()
		}
	}
}

func (r *selectionReader) takeProperty(limit int) (kind uint32, data []byte, tooLarge bool, err error) {
	units := uint32(limit/4 + 1)
	reply, err := r.c.call(newRequest(opGetProperty, 1).u32(r.reader.Load()).u32(r.property).u32(0).u32(0).u32(units).done())
	if err != nil {
		return 0, nil, false, err
	}
	format := int(reply[1])
	kind = binary.LittleEndian.Uint32(reply[8:12])
	after := binary.LittleEndian.Uint32(reply[12:16])
	length := int(binary.LittleEndian.Uint32(reply[16:20])) * max(format/8, 1)
	if format == 0 {
		length = 0
	}
	data = reply[32 : 32+min(length, len(reply)-32)]
	return kind, data, after > 0 || len(data) > limit, nil
}

func ownerOf(c *conn, selection uint32) (uint32, error) {
	reply, err := c.call(newRequest(opGetSelectionOwner, 0).u32(selection).done())
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(reply[8:12]), nil
}

func queryXFixes(c *conn) (opcode, firstEvent byte, err error) {
	reply, err := c.call(newRequest(opQueryExtension, 0).u16(uint16(len("XFIXES"))).u16(0).raw([]byte("XFIXES")).done())
	if err != nil {
		return 0, 0, err
	}
	if reply[8] == 0 {
		return 0, 0, ErrNoXFixes
	}
	return reply[9], reply[10], nil
}

func agreeXFixesVersion(c *conn, opcode byte) error {
	if _, err := c.call(newRequest(opcode, xfixesQueryVersion).u32(5).u32(0).done()); err != nil {
		return fmt.Errorf("x11: XFIXES did not answer: %w", err)
	}
	return nil
}
