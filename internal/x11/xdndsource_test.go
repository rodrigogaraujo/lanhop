//go:build linux

package x11

import (
	"encoding/binary"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

var nautilusTypes = []string{"application/vnd.portal.filetransfer", "application/vnd.portal.files", "text/uri-list", "x-special/gnome-icon-list"}

type sourceOffer struct {
	types         []string
	list          string
	silent        bool
	refuseTargets bool
}

func filesOffer(paths ...string) sourceOffer {
	return sourceOffer{types: nautilusTypes, list: listOf(paths...)}
}

func listOf(paths ...string) string {
	var list strings.Builder
	for _, path := range paths {
		list.WriteString(fileAddress(path) + "\r\n")
	}
	return list.String()
}

type sourceRequest struct {
	target string
	time   uint32
}

type sourceReply struct {
	accepted bool
	action   string
}

type xdndSource struct {
	c        *conn
	window   uint32
	atoms    map[string]uint32
	names    map[uint32]string
	writer   *propertyWriter
	times    chan uint32
	asked    chan sourceRequest
	messages chan clientMessage
	state    sync.Mutex
	offer    sourceOffer
	since    uint32
	file     string
}

func startXdndSource(t *testing.T, display string, offer sourceOffer) *xdndSource {
	t.Helper()
	source := &xdndSource{
		atoms:    make(map[string]uint32),
		names:    make(map[uint32]string),
		times:    make(chan uint32, 8),
		asked:    make(chan sourceRequest, 64),
		messages: make(chan clientMessage, 256),
		offer:    offer,
	}
	source.c = dialForTest(t, display, source.handle)
	names := []string{"XdndSelection", "XdndAware", "XdndTypeList", "XdndEnter", "XdndPosition", "XdndStatus", "XdndLeave", "XdndDrop", "XdndFinished",
		"XdndActionCopy", "XdndActionMove", "XdndActionLink", "XdndActionAsk", "TARGETS", "TIMESTAMP", "INCR", "LANHOP_TEST_SOURCE_TIME"}
	for _, name := range append(names, offer.types...) {
		atom, err := internAtom(source.c, name)
		if err != nil {
			t.Fatalf("intern %s: %v", name, err)
		}
		source.atoms[name] = atom
		source.names[atom] = name
	}
	source.writer = newPropertyWriter(source.c, source.atoms["INCR"])
	source.window = source.c.newID()
	source.c.send(newRequest(opCreateWindow, 0).u32(source.window).u32(source.c.root).
		u16(0).u16(0).u16(1).u16(1).u16(0).u16(classInputOnly).u32(0).
		u32(attributeEventMask).u32(propertyChangeMask).done())
	syncWith(t, source.c)
	source.writer.change(source.window, source.atoms["XdndTypeList"], atomAtom, 32, source.typeAtoms(offer.types))
	syncWith(t, source.c)
	return source
}

func (s *xdndSource) typeAtoms(types []string) []byte {
	var data []byte
	for _, name := range types {
		data = binary.LittleEndian.AppendUint32(data, s.atoms[name])
	}
	return data
}

func (s *xdndSource) deletesOnMove(path string) {
	s.state.Lock()
	s.file = path
	s.state.Unlock()
}

func (s *xdndSource) serverTime(t *testing.T) uint32 {
	t.Helper()
	drain(s.times)
	s.writer.changeMode(s.window, s.atoms["LANHOP_TEST_SOURCE_TIME"], atomString, 8, nil, modeAppend)
	select {
	case when := <-s.times:
		return when
	case <-time.After(3 * time.Second):
		t.Fatal("the X server did not tell the test source its time")
		return 0
	}
}

func (s *xdndSource) own(t *testing.T) {
	t.Helper()
	when := s.serverTime(t)
	s.state.Lock()
	s.since = when
	s.state.Unlock()
	s.c.send(newRequest(opSetSelectionOwner, 0).u32(s.window).u32(s.atoms["XdndSelection"]).u32(when).done())
	if owner, err := ownerOf(s.c, s.atoms["XdndSelection"]); err != nil || owner != s.window {
		t.Fatalf("the test source does not own XdndSelection: owner 0x%x, %v", owner, err)
	}
}

func (s *xdndSource) letGo(t *testing.T) {
	t.Helper()
	s.state.Lock()
	since := s.since
	s.state.Unlock()
	s.c.send(newRequest(opSetSelectionOwner, 0).u32(atomNone).u32(s.atoms["XdndSelection"]).u32(since).done())
	syncWith(t, s.c)
}

func (s *xdndSource) destroyWindow(t *testing.T) {
	t.Helper()
	s.c.send(newRequest(opDestroyWindow, 0).u32(s.window).done())
	syncWith(t, s.c)
}

func (s *xdndSource) quit() {
	s.c.close()
}

func (s *xdndSource) wasAsked(t *testing.T, target string) sourceRequest {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case request := <-s.asked:
			if request.target == target {
				return request
			}
		case <-deadline:
			t.Fatalf("the test source was never asked for %s", target)
			return sourceRequest{}
		}
	}
}

func (s *xdndSource) send(target, kind uint32, data [5]uint32) {
	data[0] = s.window
	sendClientMessage(s.c, target, target, kind, data)
}

func (s *xdndSource) enter(target uint32) {
	s.state.Lock()
	types := s.offer.types
	s.state.Unlock()
	data := [5]uint32{0, xdndVersion << 24}
	if len(types) > 3 {
		data[1] |= enterHasTypeList
	}
	for i, name := range types[:min(3, len(types))] {
		data[2+i] = s.atoms[name]
	}
	s.send(target, s.atoms["XdndEnter"], data)
}

func (s *xdndSource) position(target uint32, x, y int, when uint32, action string) {
	place := uint32(uint16(int16(x)))<<16 | uint32(uint16(int16(y)))
	s.send(target, s.atoms["XdndPosition"], [5]uint32{0, 0, place, when, s.atoms[action]})
}

func (s *xdndSource) leave(target uint32) {
	s.send(target, s.atoms["XdndLeave"], [5]uint32{})
}

func (s *xdndSource) drop(target, when uint32) {
	s.send(target, s.atoms["XdndDrop"], [5]uint32{0, 0, when})
}

func (s *xdndSource) replyFrom(target uint32, kind string, within time.Duration) (sourceReply, bool) {
	deadline := time.After(within)
	for {
		select {
		case message := <-s.messages:
			if message.kind != s.atoms[kind] || message.data[0] != target {
				continue
			}
			if kind == "XdndStatus" {
				return sourceReply{accepted: message.data[1]&statusAccepts != 0, action: s.names[message.data[4]]}, true
			}
			return sourceReply{accepted: message.data[1]&finishedAccepted != 0, action: s.names[message.data[2]]}, true
		case <-deadline:
			return sourceReply{}, false
		}
	}
}

func (s *xdndSource) status(t *testing.T, target uint32) sourceReply {
	t.Helper()
	status, answered := s.replyFrom(target, "XdndStatus", 5*time.Second)
	if !answered {
		t.Fatal("the target did not answer XdndPosition with XdndStatus")
	}
	return status
}

func (s *xdndSource) finished(t *testing.T, target uint32) sourceReply {
	t.Helper()
	finished, answered := s.replyFrom(target, "XdndFinished", 5*time.Second)
	if !answered {
		t.Fatal("the target did not answer XdndDrop with XdndFinished")
	}
	s.state.Lock()
	file := s.file
	s.state.Unlock()
	if finished.accepted && finished.action == "XdndActionMove" && file != "" {
		os.Remove(file)
	}
	return finished
}

func (s *xdndSource) handle(event []byte) {
	le := binary.LittleEndian
	switch event[0] &^ sentEventFlag {
	case eventSelectionRequest:
		s.answer(event)
	case eventClientMessage:
		push(s.messages, clientMessageFrom(event))
	case eventPropertyNotify:
		window, property, state := le.Uint32(event[4:8]), le.Uint32(event[8:12]), event[16]
		switch {
		case window == s.window && property == s.atoms["LANHOP_TEST_SOURCE_TIME"] && state == propertyNewValue:
			push(s.times, le.Uint32(event[12:16]))
		case window != s.window && state == propertyDeleted:
			s.writer.propertyDeleted(window, property)
		}
	}
}

func (s *xdndSource) answer(event []byte) {
	le := binary.LittleEndian
	when := le.Uint32(event[4:8])
	requestor := le.Uint32(event[12:16])
	selection := le.Uint32(event[16:20])
	target := le.Uint32(event[20:24])
	property := le.Uint32(event[24:28])
	if property == atomNone {
		property = target
	}
	s.state.Lock()
	offer := s.offer
	s.state.Unlock()
	push(s.asked, sourceRequest{target: s.names[target], time: when})
	if offer.silent {
		return
	}
	answered := uint32(atomNone)
	switch name := s.names[target]; {
	case name == "TARGETS" && !offer.refuseTargets:
		offered := append([]string{"TARGETS", "TIMESTAMP"}, offer.types...)
		s.writer.change(requestor, property, atomAtom, 32, s.typeAtoms(offered))
		answered = property
	case name == "text/uri-list" && slices.Contains(offer.types, name):
		s.writer.give(requestor, property, target, []byte(offer.list))
		answered = property
	}
	notifySelection(s.c, when, requestor, selection, target, answered)
}
