//go:build linux && containertest

package x11

import (
	"encoding/binary"
	"fmt"
)

type harnessConn struct {
	*conn
	atoms map[string]uint32
}

func dialHarness(d display, cookie []byte, onEvent func(event []byte), atomNames ...string) (*harnessConn, error) {
	c, err := dial(d, cookie, onEvent)
	if err != nil {
		return nil, err
	}
	h := &harnessConn{conn: c, atoms: make(map[string]uint32, len(atomNames))}
	for _, name := range atomNames {
		reply, err := c.call(newRequest(opInternAtom, 0).u16(uint16(len(name))).u16(0).raw([]byte(name)).done())
		if err != nil {
			c.close()
			return nil, fmt.Errorf("x11: cannot name atom %s: %w", name, err)
		}
		h.atoms[name] = binary.LittleEndian.Uint32(reply[8:12])
	}
	return h, nil
}

func (h *harnessConn) nameOf(atom uint32) string {
	for name, known := range h.atoms {
		if known == atom {
			return name
		}
	}
	if atom == atomNone {
		return "none"
	}
	return fmt.Sprintf("atom-%d", atom)
}

func (h *harnessConn) pointer() (x, y int, buttons uint16, err error) {
	reply, err := h.call(newRequest(opQueryPointer, 0).u32(h.root).done())
	if err != nil {
		return 0, 0, 0, err
	}
	le := binary.LittleEndian
	return int(int16(le.Uint16(reply[16:18]))), int(int16(le.Uint16(reply[18:20]))), le.Uint16(reply[24:26]), nil
}

func (h *harnessConn) screenSize() (width, height int, err error) {
	reply, err := h.call(newRequest(opGetGeometry, 0).u32(h.root).done())
	if err != nil {
		return 0, 0, err
	}
	le := binary.LittleEndian
	return int(le.Uint16(reply[16:18])), int(le.Uint16(reply[18:20])), nil
}

func (h *harnessConn) extensionOpcode(name string) (byte, error) {
	reply, err := h.call(newRequest(opQueryExtension, 0).u16(uint16(len(name))).u16(0).raw([]byte(name)).done())
	if err != nil {
		return 0, err
	}
	if reply[8] == 0 {
		return 0, fmt.Errorf("x11: the X server has no %s extension", name)
	}
	return reply[9], nil
}

func (h *harnessConn) selectionOwner(selection uint32) (uint32, error) {
	reply, err := h.call(newRequest(opGetSelectionOwner, 0).u32(selection).done())
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(reply[8:12]), nil
}

func (h *harnessConn) createWindow(x, y, width, height int, eventMask uint32) (uint32, error) {
	const classInputOutputWindow = 1
	window := h.newID()
	request := newRequest(opCreateWindow, 0).u32(window).u32(h.root).
		u16(uint16(int16(x))).u16(uint16(int16(y))).u16(uint16(width)).u16(uint16(height)).u16(0).u16(classInputOutputWindow).u32(0).
		u32(attributeEventMask).u32(eventMask).done()
	if err := h.send(request); err != nil {
		return 0, err
	}
	return window, nil
}

func (h *harnessConn) mapAndWait(window uint32) error {
	const opMapWindowRequest = 8
	if err := h.send(newRequest(opMapWindowRequest, 0).u32(window).done()); err != nil {
		return err
	}
	_, _, _, err := h.pointer()
	return err
}

func (h *harnessConn) setAtoms(window, property uint32, values ...uint32) error {
	return h.setProperty(window, property, atomAtom, 32, harnessAtomValues(values))
}

func (h *harnessConn) setProperty(window, property, kind uint32, format byte, data []byte) error {
	units := len(data) / int(format/8)
	return h.send(newRequest(opChangeProperty, modeReplace).u32(window).u32(property).u32(kind).u8(format).u8(0).u8(0).u8(0).u32(uint32(units)).raw(data).done())
}

func (h *harnessConn) property32(window, property, kind uint32, longest int) ([]uint32, error) {
	reply, err := h.call(newRequest(opGetProperty, 0).u32(window).u32(property).u32(kind).u32(0).u32(uint32(longest)).done())
	if err != nil {
		return nil, err
	}
	le := binary.LittleEndian
	if reply[1] != 32 || le.Uint32(reply[8:12]) != kind {
		return nil, nil
	}
	count := min(int(le.Uint32(reply[16:20])), (len(reply)-32)/4)
	values := make([]uint32, count)
	for i := range values {
		values[i] = le.Uint32(reply[32+4*i:])
	}
	return values, nil
}

func (h *harnessConn) takeProperty(window, property uint32) (kind uint32, data []byte, err error) {
	reply, err := h.call(newRequest(opGetProperty, 1).u32(window).u32(property).u32(0).u32(0).u32(1 << 20).done())
	if err != nil {
		return 0, nil, err
	}
	le := binary.LittleEndian
	format := int(reply[1])
	if format == 0 {
		return le.Uint32(reply[8:12]), nil, nil
	}
	length := int(le.Uint32(reply[16:20])) * (format / 8)
	return le.Uint32(reply[8:12]), reply[32 : 32+min(length, len(reply)-32)], nil
}

func (h *harnessConn) sendMessage(destination, window, kind uint32, data [5]uint32) error {
	le := binary.LittleEndian
	message := make([]byte, 32)
	message[0] = eventClientMessage
	message[1] = 32
	le.PutUint32(message[4:8], window)
	le.PutUint32(message[8:12], kind)
	for i, value := range data {
		le.PutUint32(message[12+4*i:], value)
	}
	return h.send(newRequest(opSendEvent, 0).u32(destination).u32(0).raw(message).done())
}

func (h *harnessConn) notifySelection(when, requestor, selection, target, property uint32) error {
	le := binary.LittleEndian
	notify := make([]byte, 32)
	notify[0] = eventSelectionNotify
	le.PutUint32(notify[4:8], when)
	le.PutUint32(notify[8:12], requestor)
	le.PutUint32(notify[12:16], selection)
	le.PutUint32(notify[16:20], target)
	le.PutUint32(notify[20:24], property)
	return h.send(newRequest(opSendEvent, 0).u32(requestor).u32(0).raw(notify).done())
}

func (h *harnessConn) awareWindowAt(x, y int, aware, proxy uint32) (window, destination, version uint32, err error) {
	const deepest = 32
	parent := h.root
	for range deepest {
		reply, err := h.call(newRequest(opTranslateCoordinates, 0).u32(h.root).u32(parent).u16(uint16(int16(x))).u16(uint16(int16(y))).done())
		if err != nil {
			return 0, 0, 0, err
		}
		child := binary.LittleEndian.Uint32(reply[8:12])
		if child == atomNone {
			return 0, 0, 0, nil
		}
		receiver := child
		if proxies, _ := h.property32(child, proxy, atomWindow, 1); len(proxies) == 1 && proxies[0] != atomNone {
			receiver = proxies[0]
		}
		if versions, _ := h.property32(receiver, aware, atomAtom, 1); len(versions) == 1 {
			return child, receiver, versions[0], nil
		}
		parent = child
	}
	return 0, 0, 0, nil
}

func harnessAtomValues(values []uint32) []byte {
	data := make([]byte, 0, 4*len(values))
	for _, value := range values {
		data = binary.LittleEndian.AppendUint32(data, value)
	}
	return data
}

type DisplayProbe struct {
	h *harnessConn
}

func OpenDisplayProbe() (*DisplayProbe, error) {
	return openSession(func(d display, cookie []byte) (*DisplayProbe, error) {
		h, err := dialHarness(d, cookie, func([]byte) {})
		if err != nil {
			return nil, err
		}
		return &DisplayProbe{h: h}, nil
	})
}

func (p *DisplayProbe) Pointer() (x, y int, err error) {
	x, y, _, err = p.h.pointer()
	return x, y, err
}

func (p *DisplayProbe) Warp(x, y int) error {
	const opWarpPointerRequest = 41
	request := newRequest(opWarpPointerRequest, 0).u32(atomNone).u32(p.h.root).u16(0).u16(0).u16(0).u16(0).
		u16(uint16(int16(x))).u16(uint16(int16(y))).done()
	if err := p.h.send(request); err != nil {
		return err
	}
	_, _, err := p.Pointer()
	return err
}

func (p *DisplayProbe) SelectionOwner(selection string) (uint32, error) {
	reply, err := p.h.call(newRequest(opInternAtom, 1).u16(uint16(len(selection))).u16(0).raw([]byte(selection)).done())
	if err != nil {
		return 0, err
	}
	atom := binary.LittleEndian.Uint32(reply[8:12])
	if atom == atomNone {
		return atomNone, nil
	}
	return p.h.selectionOwner(atom)
}

func (p *DisplayProbe) Close() error {
	return p.h.close()
}
