//go:build linux && containertest

package x11

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/url"
	"slices"
	"strings"
	"time"
)

const xdndTestFetchLimit = 3 * time.Second

var xdndTestTargetAtoms = []string{
	"XdndSelection", "XdndAware", "XdndEnter", "XdndPosition", "XdndStatus", "XdndLeave", "XdndDrop", "XdndFinished",
	"XdndActionCopy", "XdndActionMove", "XdndTypeList", "text/uri-list", "INCR", "LANHOP_TEST_DROPPED",
}

type XdndTestTargetConfig struct {
	X, Y       int
	Width      int
	Height     int
	AnswerMove bool
	Take       func(paths []string) error
	Log        io.Writer
}

type XdndTestTarget struct {
	h       *harnessConn
	config  XdndTestTargetConfig
	window  uint32
	events  chan []byte
	backlog [][]byte
	stopped chan struct{}
	source  uint32
	offered bool
}

func StartXdndTestTarget(config XdndTestTargetConfig) (*XdndTestTarget, error) {
	if config.Log == nil {
		config.Log = io.Discard
	}
	if config.Take == nil {
		config.Take = func([]string) error { return nil }
	}
	return openSession(func(d display, cookie []byte) (*XdndTestTarget, error) {
		return startXdndTestTarget(d, cookie, config)
	})
}

func startXdndTestTarget(d display, cookie []byte, config XdndTestTargetConfig) (*XdndTestTarget, error) {
	t := &XdndTestTarget{config: config, events: make(chan []byte, 4096), stopped: make(chan struct{})}
	h, err := dialHarness(d, cookie, t.queue, xdndTestTargetAtoms...)
	if err != nil {
		return nil, err
	}
	t.h = h
	t.window, err = h.createWindow(config.X, config.Y, config.Width, config.Height, propertyChangeMask)
	if err == nil {
		err = h.setAtoms(t.window, h.atoms["XdndAware"], xdndVersion)
	}
	if err == nil {
		err = h.mapAndWait(t.window)
	}
	if err != nil {
		h.close()
		return nil, err
	}
	t.logf("window=%d x=%d y=%d width=%d height=%d", t.window, config.X, config.Y, config.Width, config.Height)
	go t.run()
	return t, nil
}

func (t *XdndTestTarget) Window() uint32 {
	return t.window
}

func (t *XdndTestTarget) Close() error {
	err := t.h.close()
	<-t.stopped
	return err
}

func (t *XdndTestTarget) queue(event []byte) {
	select {
	case t.events <- event:
	default:
	}
}

func (t *XdndTestTarget) logf(format string, values ...any) {
	fmt.Fprintf(t.config.Log, format+"\n", values...)
}

func (t *XdndTestTarget) sent(request string, err error) {
	if err != nil {
		t.logf("request-failed request=%s error=%v", request, err)
	}
}

func (t *XdndTestTarget) atom(name string) uint32 {
	return t.h.atoms[name]
}

func (t *XdndTestTarget) answeredAction() uint32 {
	if t.config.AnswerMove {
		return t.atom("XdndActionMove")
	}
	return t.atom("XdndActionCopy")
}

func (t *XdndTestTarget) run() {
	defer close(t.stopped)
	for {
		if len(t.backlog) > 0 {
			event := t.backlog[0]
			t.backlog = t.backlog[1:]
			t.handle(event)
			continue
		}
		select {
		case event := <-t.events:
			t.handle(event)
		case <-t.h.closed:
			return
		}
	}
}

func (t *XdndTestTarget) handle(event []byte) {
	if event[0]&^sentEventFlag != eventClientMessage {
		return
	}
	data := xdndTestMessageData(event)
	switch binary.LittleEndian.Uint32(event[8:12]) {
	case t.atom("XdndEnter"):
		t.source = data[0]
		types := slices.DeleteFunc(slices.Clone(data[2:5]), func(offered uint32) bool { return offered == atomNone })
		if data[1]&1 != 0 {
			if listed, err := t.h.property32(t.source, t.atom("XdndTypeList"), atomAtom, 1024); err == nil {
				types = listed
			}
		}
		t.offered = slices.Contains(types, t.atom("text/uri-list"))
		t.logf("enter source=%d version=%d uri-list=%t", t.source, data[1]>>24, t.offered)
	case t.atom("XdndPosition"):
		if data[0] != t.source {
			return
		}
		flags, action := uint32(2), uint32(atomNone)
		if t.offered {
			flags, action = 3, t.answeredAction()
		}
		t.sent("XdndStatus", t.h.sendMessage(t.source, t.source, t.atom("XdndStatus"), [5]uint32{t.window, flags, 0, 0, action}))
	case t.atom("XdndLeave"):
		if data[0] == t.source {
			t.logf("leave source=%d", t.source)
			t.source, t.offered = atomNone, false
		}
	case t.atom("XdndDrop"):
		if data[0] == t.source {
			t.drop(data[2])
		}
	}
}

func (t *XdndTestTarget) drop(when uint32) {
	source := t.source
	t.source, t.offered = atomNone, false
	paths, err := t.fetch(when)
	if err == nil {
		t.logf("dropped paths=%q", paths)
		err = t.config.Take(paths)
	}
	finished := [5]uint32{t.window}
	if err != nil {
		t.logf("drop-failed error=%v", err)
	} else {
		finished[1], finished[2] = 1, t.answeredAction()
		t.logf("took paths=%q action=%s", paths, t.h.nameOf(finished[2]))
	}
	t.sent("XdndFinished", t.h.sendMessage(source, source, t.atom("XdndFinished"), finished))
}

func (t *XdndTestTarget) fetch(when uint32) ([]string, error) {
	le := binary.LittleEndian
	property, list := t.atom("LANHOP_TEST_DROPPED"), t.atom("text/uri-list")
	if err := t.h.send(newRequest(opDeleteProperty, 0).u32(t.window).u32(property).done()); err != nil {
		return nil, err
	}
	if err := t.h.send(newRequest(opConvertSelection, 0).u32(t.window).u32(t.atom("XdndSelection")).u32(list).u32(property).u32(when).done()); err != nil {
		return nil, err
	}
	deadline := time.After(xdndTestFetchLimit)
	for {
		select {
		case event := <-t.events:
			isAnswer := event[0]&^sentEventFlag == eventSelectionNotify && le.Uint32(event[8:12]) == t.window && le.Uint32(event[16:20]) == list
			if !isAnswer {
				t.backlog = append(t.backlog, event)
				continue
			}
			if le.Uint32(event[20:24]) == atomNone {
				return nil, errors.New("x11: the source refused text/uri-list")
			}
			kind, data, err := t.h.takeProperty(t.window, property)
			if err != nil {
				return nil, err
			}
			if kind == t.atom("INCR") {
				return nil, errors.New("x11: the source sent text/uri-list in parts, which this test target does not read")
			}
			return xdndTestPaths(data)
		case <-deadline:
			return nil, errors.New("x11: the source did not give text/uri-list in time")
		case <-t.h.closed:
			return nil, t.h.err()
		}
	}
}

func xdndTestPaths(list []byte) ([]string, error) {
	var paths []string
	for _, line := range strings.Split(string(list), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		address, err := url.Parse(line)
		local := err == nil && address.Scheme == "file" && (address.Host == "" || address.Host == "localhost") && strings.HasPrefix(address.Path, "/")
		if !local {
			return nil, fmt.Errorf("x11: %q is not the address of a local file", line)
		}
		paths = append(paths, address.Path)
	}
	if len(paths) == 0 {
		return nil, errors.New("x11: the dropped list holds no file")
	}
	return paths, nil
}
