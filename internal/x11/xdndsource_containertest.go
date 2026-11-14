//go:build linux && containertest

package x11

import (
	"encoding/binary"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

const xdndTestFinishLimit = 10 * time.Second

var xdndTestSourceAtoms = []string{
	"XdndSelection", "XdndAware", "XdndProxy", "XdndEnter", "XdndPosition", "XdndStatus", "XdndLeave", "XdndDrop", "XdndFinished",
	"XdndActionCopy", "XdndActionMove", "XdndActionList", "text/uri-list", "TARGETS", "TIMESTAMP",
	"application/vnd.portal.filetransfer", "application/vnd.portal.files",
}

type XdndTestSourceConfig struct {
	File   string
	X, Y   int
	Width  int
	Height int
	Log    io.Writer
}

type XdndTestSource struct {
	h       *harnessConn
	config  XdndTestSourceConfig
	window  uint32
	list    []byte
	events  chan []byte
	stopped chan struct{}

	dragging    bool
	ownedSince  uint32
	target      uint32
	destination uint32
	version     uint32
	accepts     bool
	action      uint32
	dropped     bool
	finishBy    <-chan time.Time
}

func StartXdndTestSource(config XdndTestSourceConfig) (*XdndTestSource, error) {
	path, err := filepath.Abs(config.File)
	if err != nil {
		return nil, err
	}
	config.File = path
	if config.Log == nil {
		config.Log = io.Discard
	}
	return openSession(func(d display, cookie []byte) (*XdndTestSource, error) {
		return startXdndTestSource(d, cookie, config)
	})
}

func startXdndTestSource(d display, cookie []byte, config XdndTestSourceConfig) (*XdndTestSource, error) {
	s := &XdndTestSource{
		config:  config,
		list:    []byte((&url.URL{Scheme: "file", Path: config.File}).String() + "\r\n"),
		events:  make(chan []byte, 4096),
		stopped: make(chan struct{}),
	}
	h, err := dialHarness(d, cookie, s.queue, xdndTestSourceAtoms...)
	if err != nil {
		return nil, err
	}
	s.h = h
	s.window, err = h.createWindow(config.X, config.Y, config.Width, config.Height, buttonPressMask|buttonReleaseMask|pointerMotionMask)
	if err == nil {
		err = h.mapAndWait(s.window)
	}
	if err != nil {
		h.close()
		return nil, err
	}
	s.logf("window=%d x=%d y=%d width=%d height=%d file=%s", s.window, config.X, config.Y, config.Width, config.Height, config.File)
	go s.run()
	return s, nil
}

func (s *XdndTestSource) Window() uint32 {
	return s.window
}

func (s *XdndTestSource) Close() error {
	err := s.h.close()
	<-s.stopped
	return err
}

func (s *XdndTestSource) queue(event []byte) {
	select {
	case s.events <- event:
	default:
	}
}

func (s *XdndTestSource) logf(format string, values ...any) {
	fmt.Fprintf(s.config.Log, format+"\n", values...)
}

func (s *XdndTestSource) atom(name string) uint32 {
	return s.h.atoms[name]
}

func (s *XdndTestSource) run() {
	defer close(s.stopped)
	for {
		select {
		case event := <-s.events:
			s.handle(event)
		case <-s.finishBy:
			s.logf("no-finish window=%d", s.target)
			s.logf("kept path=%s", s.config.File)
			s.end()
		case <-s.h.closed:
			return
		}
	}
}

func (s *XdndTestSource) handle(event []byte) {
	le := binary.LittleEndian
	switch event[0] &^ sentEventFlag {
	case eventButtonPress:
		if event[1] == dragButton && !s.dragging && le.Uint32(event[12:16]) == s.window {
			s.start(le.Uint32(event[4:8]))
		}
	case eventMotionNotify:
		if s.dragging && !s.dropped {
			s.moveTo(int(int16(le.Uint16(event[20:22]))), int(int16(le.Uint16(event[22:24]))), le.Uint32(event[4:8]))
		}
	case eventButtonRelease:
		if event[1] == dragButton && s.dragging && !s.dropped {
			s.release(le.Uint32(event[4:8]))
		}
	case eventClientMessage:
		s.message(le.Uint32(event[8:12]), xdndTestMessageData(event))
	case eventSelectionRequest:
		s.answer(event)
	case eventSelectionClear:
		if s.dragging && le.Uint32(event[12:16]) == s.atom("XdndSelection") {
			s.logf("lost-selection")
		}
	}
}

func xdndTestMessageData(event []byte) [5]uint32 {
	var data [5]uint32
	for i := range data {
		data[i] = binary.LittleEndian.Uint32(event[12+4*i:])
	}
	return data
}

func (s *XdndTestSource) start(when uint32) {
	selection := s.atom("XdndSelection")
	s.h.send(newRequest(opSetSelectionOwner, 0).u32(s.window).u32(selection).u32(when).done())
	owner, err := s.h.selectionOwner(selection)
	if err != nil || owner != s.window {
		s.logf("drag-not-started owner=%d error=%v", owner, err)
		return
	}
	s.h.setAtoms(s.window, s.atom("XdndActionList"), s.atom("XdndActionMove"), s.atom("XdndActionCopy"))
	s.dragging, s.ownedSince = true, when
	s.logf("drag-started window=%d", s.window)
}

func (s *XdndTestSource) moveTo(x, y int, when uint32) {
	window, destination, version, err := s.h.awareWindowAt(x, y, s.atom("XdndAware"), s.atom("XdndProxy"))
	if err != nil {
		s.logf("lookup-failed error=%v", err)
		return
	}
	if window == s.window || version < xdndOldestVersion {
		window = atomNone
	}
	if window != s.target {
		s.leave()
		if window != atomNone {
			s.enter(window, destination, min(version, xdndVersion))
		}
	}
	if s.target == atomNone {
		return
	}
	place := uint32(uint16(int16(x)))<<16 | uint32(uint16(int16(y)))
	s.h.sendMessage(s.destination, s.target, s.atom("XdndPosition"), [5]uint32{s.window, 0, place, when, s.atom("XdndActionMove")})
}

func (s *XdndTestSource) enter(window, destination, version uint32) {
	s.target, s.destination, s.version = window, destination, version
	s.accepts, s.action = false, atomNone
	offered := [5]uint32{s.window, version << 24, s.atom("application/vnd.portal.filetransfer"), s.atom("application/vnd.portal.files"), s.atom("text/uri-list")}
	s.h.sendMessage(destination, window, s.atom("XdndEnter"), offered)
	s.logf("enter window=%d version=%d", window, version)
}

func (s *XdndTestSource) leave() {
	if s.target == atomNone {
		return
	}
	s.h.sendMessage(s.destination, s.target, s.atom("XdndLeave"), [5]uint32{s.window})
	s.logf("leave window=%d", s.target)
	s.target, s.destination, s.accepts, s.action = atomNone, atomNone, false, atomNone
}

func (s *XdndTestSource) fromTarget(window uint32) bool {
	return s.target != atomNone && (window == s.target || window == s.destination)
}

func (s *XdndTestSource) message(kind uint32, data [5]uint32) {
	switch kind {
	case s.atom("XdndStatus"):
		if !s.dragging || s.dropped || !s.fromTarget(data[0]) {
			return
		}
		accepts, action := data[1]&1 != 0, data[4]
		if accepts != s.accepts || action != s.action {
			s.logf("status window=%d accept=%t action=%s", data[0], accepts, s.h.nameOf(action))
		}
		s.accepts, s.action = accepts, action
	case s.atom("XdndFinished"):
		if !s.dropped || !s.fromTarget(data[0]) {
			return
		}
		accepted, action := true, s.action
		if s.version >= 5 {
			accepted, action = data[1]&1 != 0, data[2]
		}
		s.logf("finished window=%d accepted=%t action=%s", data[0], accepted, s.h.nameOf(action))
		s.settle(accepted && action == s.atom("XdndActionMove"))
		s.end()
	}
}

func (s *XdndTestSource) settle(moved bool) {
	if !moved {
		s.logf("kept path=%s", s.config.File)
		return
	}
	if err := os.Remove(s.config.File); err != nil {
		s.logf("delete-failed path=%s error=%v", s.config.File, err)
		return
	}
	s.logf("deleted path=%s", s.config.File)
}

func (s *XdndTestSource) release(when uint32) {
	switch {
	case s.target == atomNone:
		s.logf("released-without-drop reason=no-target")
	case !s.accepts:
		s.logf("released-without-drop reason=not-accepted window=%d", s.target)
		s.leave()
	default:
		s.h.sendMessage(s.destination, s.target, s.atom("XdndDrop"), [5]uint32{s.window, 0, when})
		s.dropped = true
		s.finishBy = time.After(xdndTestFinishLimit)
		s.logf("drop window=%d action=%s", s.target, s.h.nameOf(s.action))
		return
	}
	s.logf("kept path=%s", s.config.File)
	s.end()
}

func (s *XdndTestSource) end() {
	s.h.send(newRequest(opSetSelectionOwner, 0).u32(atomNone).u32(s.atom("XdndSelection")).u32(s.ownedSince).done())
	s.dragging, s.dropped, s.finishBy = false, false, nil
	s.target, s.destination, s.accepts, s.action = atomNone, atomNone, false, atomNone
	s.logf("drag-ended")
}

func (s *XdndTestSource) answer(event []byte) {
	le := binary.LittleEndian
	when, requestor := le.Uint32(event[4:8]), le.Uint32(event[12:16])
	selection, target, property := le.Uint32(event[16:20]), le.Uint32(event[20:24]), le.Uint32(event[24:28])
	if property == atomNone {
		property = target
	}
	given := uint32(atomNone)
	inTime := when == currentTime || int32(when-s.ownedSince) >= 0
	if s.dragging && inTime && selection == s.atom("XdndSelection") {
		switch target {
		case s.atom("TARGETS"):
			s.h.setAtoms(requestor, property, s.atom("TARGETS"), s.atom("TIMESTAMP"),
				s.atom("application/vnd.portal.filetransfer"), s.atom("application/vnd.portal.files"), s.atom("text/uri-list"))
			given = property
		case s.atom("TIMESTAMP"):
			s.h.setProperty(requestor, property, atomInteger, 32, le.AppendUint32(nil, s.ownedSince))
			given = property
		case s.atom("text/uri-list"):
			s.h.setProperty(requestor, property, target, 8, s.list)
			given = property
		}
	}
	s.logf("asked target=%s given=%t", s.h.nameOf(target), given != atomNone)
	s.h.notifySelection(when, requestor, selection, target, given)
}
