//go:build linux

package x11

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
)

const (
	opCreateWindow           = 1
	opChangeWindowAttributes = 2
	opInternAtom             = 16
	opChangeProperty         = 18
	opDeleteProperty         = 19
	opGetProperty            = 20
	opSetSelectionOwner      = 22
	opGetSelectionOwner      = 23
	opConvertSelection       = 24
	opSendEvent              = 25
	opQueryExtension         = 98

	xfixesQueryVersion         = 0
	xfixesSelectSelectionInput = 2

	eventPropertyNotify   = 28
	eventSelectionClear   = 29
	eventSelectionRequest = 30
	eventSelectionNotify  = 31

	propertyNewValue    = 0
	propertyDeleted     = 1
	propertyChangeMask  = 0x400000
	attributeEventMask  = 0x800
	classInputOnly      = 2
	modeReplace         = 0
	modeAppend          = 2
	currentTime         = 0
	atomNone            = 0
	atomAtom            = 4
	atomInteger         = 19
	atomString          = 31
	selectionEventsMask = 1 | 2 | 4

	answerTimeout    = time.Second
	transferLifetime = 10 * time.Second
	largestChunk     = 64 << 10
	privateHintValue = "secret"
)

var (
	ErrNoAnswer = errors.New("x11: the program that owns the clipboard did not answer")
	ErrRefused  = errors.New("x11: the X server did not let lanhop own the clipboard")
	ErrNoXFixes = errors.New("x11: the X server has no XFIXES extension")
)

type atoms struct {
	clipboard, targets, timestamp, utf8String, text, plain, plainUTF8, incr, passwordHint uint32
	ownProperty, timeProperty                                                             uint32
}

type selectionNotify struct {
	target   uint32
	property uint32
}

type transfer struct {
	requestor uint32
	property  uint32
	kind      uint32
	data      []byte
	started   time.Time
}

type Clipboard struct {
	c            *conn
	window       uint32
	atoms        atoms
	xfixesEvent  byte
	chunk        int
	notifies     chan selectionNotify
	newValues    chan uint32
	times        chan uint32
	state        sync.Mutex
	mark         int64
	owned        bool
	ownedSince   uint32
	text         []byte
	privateHint  bool
	transfers    []*transfer
	readTimeout  time.Duration
	handleEvents func(event []byte)
}

func Open() (*Clipboard, error) {
	if value := os.Getenv("DISPLAY"); value != "" {
		return openNamed(value, authorityFile())
	}
	if output, err := exec.Command("systemctl", "--user", "show-environment").Output(); err == nil {
		if value, authority := sessionEnvironment(output); value != "" {
			if board, err := openNamed(value, authority); err == nil {
				return board, nil
			}
		}
	}
	var failures []error
	for _, d := range displaysOnThisMachine(socketDirectory, 0, uint32(os.Getuid())) {
		board, err := open(d, cookieFromFile(d.number, authorityFile()))
		if err == nil {
			return board, nil
		}
		failures = append(failures, err)
	}
	return nil, errors.Join(append([]error{ErrNoDisplay}, failures...)...)
}

func openNamed(value, authority string) (*Clipboard, error) {
	d, err := parseDisplay(value)
	if err != nil {
		return nil, err
	}
	return open(d, cookieFromFile(d.number, authority))
}

func open(d display, cookie []byte) (*Clipboard, error) {
	board := &Clipboard{
		notifies:    make(chan selectionNotify, 8),
		newValues:   make(chan uint32, 64),
		times:       make(chan uint32, 8),
		readTimeout: answerTimeout,
	}
	c, err := dial(d, cookie, board.handle)
	if err != nil {
		return nil, err
	}
	board.c = c
	board.chunk = min(largestChunk, c.maxRequestBytes-64)
	if err := board.prepare(); err != nil {
		c.close()
		return nil, err
	}
	return board, nil
}

func (b *Clipboard) prepare() error {
	names := map[*uint32]string{
		&b.atoms.clipboard:    "CLIPBOARD",
		&b.atoms.targets:      "TARGETS",
		&b.atoms.timestamp:    "TIMESTAMP",
		&b.atoms.timeProperty: "LANHOP_TIME",
		&b.atoms.utf8String:   "UTF8_STRING",
		&b.atoms.text:         "TEXT",
		&b.atoms.plain:        "text/plain",
		&b.atoms.plainUTF8:    "text/plain;charset=utf-8",
		&b.atoms.incr:         "INCR",
		&b.atoms.passwordHint: "x-kde-passwordManagerHint",
		&b.atoms.ownProperty:  "LANHOP_CLIPBOARD",
	}
	for atom, name := range names {
		reply, err := b.c.call(newRequest(opInternAtom, 0).u16(uint16(len(name))).u16(0).raw([]byte(name)).done())
		if err != nil {
			return fmt.Errorf("x11: cannot name atom %s: %w", name, err)
		}
		*atom = binary.LittleEndian.Uint32(reply[8:12])
	}

	reply, err := b.c.call(newRequest(opQueryExtension, 0).u16(uint16(len("XFIXES"))).u16(0).raw([]byte("XFIXES")).done())
	if err != nil {
		return err
	}
	if reply[8] == 0 {
		return ErrNoXFixes
	}
	xfixes := reply[9]
	b.xfixesEvent = reply[10]
	if _, err := b.c.call(newRequest(xfixes, xfixesQueryVersion).u32(5).u32(0).done()); err != nil {
		return fmt.Errorf("x11: XFIXES did not answer: %w", err)
	}

	b.window = b.c.newID()
	create := newRequest(opCreateWindow, 0).u32(b.window).u32(b.c.root).
		u16(0).u16(0).u16(1).u16(1).u16(0).u16(classInputOnly).u32(0).
		u32(attributeEventMask).u32(propertyChangeMask).done()
	if err := b.c.send(create); err != nil {
		return err
	}
	if err := b.c.send(newRequest(xfixes, xfixesSelectSelectionInput).u32(b.window).u32(b.atoms.clipboard).u32(selectionEventsMask).done()); err != nil {
		return err
	}
	if _, err := b.owner(); err != nil {
		return fmt.Errorf("x11: cannot watch the clipboard: %w", err)
	}
	return nil
}

func (b *Clipboard) Close() error {
	return b.c.close()
}

func (b *Clipboard) Mark() int64 {
	b.owner()
	b.state.Lock()
	defer b.state.Unlock()
	return b.mark
}

func (b *Clipboard) Write(text string) (mark int64, err error) {
	return b.write(text, false)
}

func (b *Clipboard) write(text string, markPrivate bool) (int64, error) {
	now, err := b.serverTime()
	if err != nil {
		return 0, err
	}
	b.state.Lock()
	b.owned, b.ownedSince, b.text, b.privateHint = true, now, []byte(text), markPrivate
	b.state.Unlock()
	if err := b.c.send(newRequest(opSetSelectionOwner, 0).u32(b.window).u32(b.atoms.clipboard).u32(now).done()); err != nil {
		return 0, err
	}
	owner, err := b.owner()
	if err != nil {
		return 0, err
	}
	if owner != b.window {
		b.state.Lock()
		b.owned, b.text = false, nil
		b.state.Unlock()
		return 0, ErrRefused
	}
	return b.Mark(), nil
}

func (b *Clipboard) Read() (string, error) {
	b.state.Lock()
	if b.owned {
		private, text := b.privateHint, string(b.text)
		b.state.Unlock()
		if private {
			return "", clipboard.ErrPrivate
		}
		return text, nil
	}
	b.state.Unlock()

	owner, err := b.owner()
	if err != nil {
		return "", err
	}
	if owner == atomNone {
		return "", clipboard.ErrEmpty
	}
	offered, err := b.targetsOffered()
	if err != nil {
		return "", err
	}
	if slices.Contains(offered, b.atoms.passwordHint) {
		hint, err := b.convert(b.atoms.passwordHint, len(privateHintValue)+16)
		if err == nil && string(hint) == privateHintValue {
			return "", clipboard.ErrPrivate
		}
	}
	target, found := b.textTarget(offered)
	if !found {
		return "", clipboard.ErrNotText
	}
	data, err := b.convert(target, clipboard.Limit)
	if err != nil {
		return "", err
	}
	if target == atomString {
		data = latin1ToUTF8(data)
	}
	if !utf8.Valid(data) {
		return "", clipboard.ErrNotText
	}
	return string(data), nil
}

func (b *Clipboard) targetsOffered() ([]uint32, error) {
	data, err := b.convert(b.atoms.targets, 4096)
	if errors.Is(err, errTargetRefused) {
		return []uint32{b.atoms.utf8String}, nil
	}
	if err != nil {
		return nil, err
	}
	offered := make([]uint32, 0, len(data)/4)
	for i := 0; i+4 <= len(data); i += 4 {
		offered = append(offered, binary.LittleEndian.Uint32(data[i:i+4]))
	}
	return offered, nil
}

func (b *Clipboard) textTarget(offered []uint32) (uint32, bool) {
	for _, wanted := range []uint32{b.atoms.utf8String, b.atoms.plainUTF8, atomString} {
		if slices.Contains(offered, wanted) {
			return wanted, true
		}
	}
	return 0, false
}

var errTargetRefused = errors.New("x11: the owner of the clipboard refused that format")

func (b *Clipboard) convert(target uint32, limit int) ([]byte, error) {
	drain(b.notifies)
	if err := b.c.send(newRequest(opDeleteProperty, 0).u32(b.window).u32(b.atoms.ownProperty).done()); err != nil {
		return nil, err
	}
	request := newRequest(opConvertSelection, 0).u32(b.window).u32(b.atoms.clipboard).u32(target).u32(b.atoms.ownProperty).u32(currentTime).done()
	if err := b.c.send(request); err != nil {
		return nil, err
	}
	deadline := time.After(b.readTimeout)
	for {
		select {
		case got := <-b.notifies:
			if got.target != target {
				continue
			}
			if got.property == atomNone {
				return nil, errTargetRefused
			}
			drain(b.newValues)
			return b.receive(limit, deadline)
		case <-deadline:
			return nil, ErrNoAnswer
		case <-b.c.closed:
			return nil, b.c.err()
		}
	}
}

func (b *Clipboard) receive(limit int, deadline <-chan time.Time) ([]byte, error) {
	kind, data, tooLarge, err := b.takeProperty(limit)
	if err != nil {
		return nil, err
	}
	if kind != b.atoms.incr {
		if tooLarge {
			return nil, clipboard.ErrTooLarge
		}
		return data, nil
	}
	var whole []byte
	for {
		if err := b.waitForNewValue(deadline); err != nil {
			return nil, err
		}
		_, part, tooLarge, err := b.takeProperty(limit - len(whole))
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

func (b *Clipboard) waitForNewValue(deadline <-chan time.Time) error {
	for {
		select {
		case property := <-b.newValues:
			if property == b.atoms.ownProperty {
				return nil
			}
		case <-deadline:
			return ErrNoAnswer
		case <-b.c.closed:
			return b.c.err()
		}
	}
}

func (b *Clipboard) takeProperty(limit int) (kind uint32, data []byte, tooLarge bool, err error) {
	units := uint32(limit/4 + 1)
	reply, err := b.c.call(newRequest(opGetProperty, 1).u32(b.window).u32(b.atoms.ownProperty).u32(0).u32(0).u32(units).done())
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

func (b *Clipboard) serverTime() (uint32, error) {
	drain(b.times)
	b.changePropertyMode(b.window, b.atoms.timeProperty, atomString, 8, nil, modeAppend)
	select {
	case when := <-b.times:
		return when, nil
	case <-time.After(replyTimeout):
		return 0, errors.New("x11: the X server did not tell its time")
	case <-b.c.closed:
		return 0, b.c.err()
	}
}

func (b *Clipboard) owner() (uint32, error) {
	reply, err := b.c.call(newRequest(opGetSelectionOwner, 0).u32(b.atoms.clipboard).done())
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(reply[8:12]), nil
}

func (b *Clipboard) handle(event []byte) {
	code := event[0] &^ sentEventFlag
	switch {
	case code == b.xfixesEvent && b.xfixesEvent != 0:
		if binary.LittleEndian.Uint32(event[12:16]) == b.atoms.clipboard {
			b.state.Lock()
			b.mark++
			b.state.Unlock()
		}
	case code == eventSelectionNotify:
		got := selectionNotify{target: binary.LittleEndian.Uint32(event[16:20]), property: binary.LittleEndian.Uint32(event[20:24])}
		select {
		case b.notifies <- got:
		default:
		}
	case code == eventSelectionRequest:
		b.answer(event)
	case code == eventSelectionClear:
		if binary.LittleEndian.Uint32(event[12:16]) == b.atoms.clipboard {
			b.state.Lock()
			b.owned, b.text, b.privateHint, b.transfers = false, nil, false, nil
			b.state.Unlock()
		}
	case code == eventPropertyNotify:
		window := binary.LittleEndian.Uint32(event[4:8])
		property := binary.LittleEndian.Uint32(event[8:12])
		state := event[16]
		if window == b.window {
			if state != propertyNewValue {
				return
			}
			target := b.newValues
			value := property
			if property == b.atoms.timeProperty {
				target, value = b.times, binary.LittleEndian.Uint32(event[12:16])
			}
			select {
			case target <- value:
			default:
			}
			return
		}
		if state == propertyDeleted {
			b.continueTransfer(window, property)
		}
	}
}

func (b *Clipboard) answer(event []byte) {
	when := binary.LittleEndian.Uint32(event[4:8])
	requestor := binary.LittleEndian.Uint32(event[12:16])
	selection := binary.LittleEndian.Uint32(event[16:20])
	target := binary.LittleEndian.Uint32(event[20:24])
	property := binary.LittleEndian.Uint32(event[24:28])
	if property == atomNone {
		property = target
	}

	b.state.Lock()
	owned, since, text, private := b.owned, b.ownedSince, b.text, b.privateHint
	b.state.Unlock()

	answered := uint32(atomNone)
	if owned && selection == b.atoms.clipboard {
		if b.provide(requestor, property, target, since, text, private) {
			answered = property
		}
	}
	notify := make([]byte, 32)
	notify[0] = eventSelectionNotify
	binary.LittleEndian.PutUint32(notify[4:8], when)
	binary.LittleEndian.PutUint32(notify[8:12], requestor)
	binary.LittleEndian.PutUint32(notify[12:16], selection)
	binary.LittleEndian.PutUint32(notify[16:20], target)
	binary.LittleEndian.PutUint32(notify[20:24], answered)
	b.c.send(newRequest(opSendEvent, 0).u32(requestor).u32(0).raw(notify).done())
}

func (b *Clipboard) provide(requestor, property, target, since uint32, text []byte, private bool) bool {
	latin1, fitsLatin1 := utf8ToLatin1(text)
	switch target {
	case b.atoms.targets:
		offered := []uint32{b.atoms.targets, b.atoms.timestamp, b.atoms.utf8String, b.atoms.text, b.atoms.plainUTF8, b.atoms.plain}
		if fitsLatin1 {
			offered = append(offered, atomString)
		}
		if private {
			offered = append(offered, b.atoms.passwordHint)
		}
		data := make([]byte, 0, len(offered)*4)
		for _, atom := range offered {
			data = binary.LittleEndian.AppendUint32(data, atom)
		}
		b.changeProperty(requestor, property, atomAtom, 32, data)
		return true
	case b.atoms.timestamp:
		b.changeProperty(requestor, property, atomInteger, 32, binary.LittleEndian.AppendUint32(nil, since))
		return true
	case atomString:
		if !fitsLatin1 {
			return false
		}
		return b.give(requestor, property, atomString, latin1)
	case b.atoms.passwordHint:
		if !private {
			return false
		}
		b.changeProperty(requestor, property, b.atoms.utf8String, 8, []byte(privateHintValue))
		return true
	case b.atoms.utf8String, b.atoms.text, b.atoms.plainUTF8, b.atoms.plain:
		kind := target
		if target == b.atoms.text {
			kind = b.atoms.utf8String
		}
		return b.give(requestor, property, kind, text)
	}
	return false
}

func (b *Clipboard) give(requestor, property, kind uint32, data []byte) bool {
	if len(data) <= b.chunk {
		b.changeProperty(requestor, property, kind, 8, data)
		return true
	}
	b.startTransfer(requestor, property, kind, data)
	return true
}

func (b *Clipboard) startTransfer(requestor, property, kind uint32, text []byte) {
	b.c.send(newRequest(opChangeWindowAttributes, 0).u32(requestor).u32(attributeEventMask).u32(propertyChangeMask).done())
	now := time.Now()
	b.state.Lock()
	b.transfers = slices.DeleteFunc(b.transfers, func(t *transfer) bool {
		return now.Sub(t.started) > transferLifetime || (t.requestor == requestor && t.property == property)
	})
	b.transfers = append(b.transfers, &transfer{requestor: requestor, property: property, kind: kind, data: text, started: now})
	b.state.Unlock()
	b.changeProperty(requestor, property, b.atoms.incr, 32, binary.LittleEndian.AppendUint32(nil, uint32(len(text))))
}

func (b *Clipboard) continueTransfer(requestor, property uint32) {
	b.state.Lock()
	index := slices.IndexFunc(b.transfers, func(t *transfer) bool {
		return t.requestor == requestor && t.property == property
	})
	if index < 0 {
		b.state.Unlock()
		return
	}
	current := b.transfers[index]
	part := current.data[:min(b.chunk, len(current.data))]
	current.data = current.data[len(part):]
	if len(part) == 0 {
		b.transfers = slices.Delete(b.transfers, index, index+1)
	}
	b.state.Unlock()
	b.changeProperty(requestor, property, current.kind, 8, part)
}

func (b *Clipboard) changeProperty(window, property, kind uint32, format byte, data []byte) {
	b.changePropertyMode(window, property, kind, format, data, modeReplace)
}

func (b *Clipboard) changePropertyMode(window, property, kind uint32, format byte, data []byte, mode byte) {
	units := len(data) / int(format/8)
	b.c.send(newRequest(opChangeProperty, mode).u32(window).u32(property).u32(kind).u8(format).u8(0).u8(0).u8(0).u32(uint32(units)).raw(data).done())
}

func drain[T any](events chan T) {
	for {
		select {
		case <-events:
		default:
			return
		}
	}
}

func utf8ToLatin1(text []byte) ([]byte, bool) {
	converted := make([]byte, 0, len(text))
	for _, r := range string(text) {
		if r > 0xff || r == utf8.RuneError {
			return nil, false
		}
		converted = append(converted, byte(r))
	}
	return converted, true
}

func latin1ToUTF8(data []byte) []byte {
	converted := make([]byte, 0, len(data))
	for _, b := range data {
		converted = utf8.AppendRune(converted, rune(b))
	}
	return converted
}
