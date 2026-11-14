//go:build linux

package x11

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
)

const (
	opCreateWindow           = 1
	opChangeWindowAttributes = 2
	opDestroyWindow          = 4
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
	wholeReadTimeout = 3 * time.Second
	transferLifetime = 10 * time.Second
	largestChunk     = 64 << 10
	privateHintValue = "secret"
	uriListLimit     = 64 << 10
	fileListLimit    = 16 << 20
)

var (
	ErrNoAnswer    = errors.New("x11: the program that owns the clipboard did not answer")
	ErrRefused     = errors.New("x11: the X server did not let lanhop own the clipboard")
	ErrNoXFixes    = errors.New("x11: the X server has no XFIXES extension")
	ErrNotAbsolute = errors.New("x11: a file to put on the clipboard is not given by an absolute path")
	ErrNULInPath   = errors.New("x11: a path to put on the clipboard holds a NUL byte")
)

type atoms struct {
	clipboard, targets, timestamp, utf8String, text, plain, plainUTF8, incr, passwordHint uint32
	gnomeFiles, uriList, portalFiles, portalTransfer                                      uint32
	ownProperty, timeProperty                                                             uint32
}

type hint int

const (
	noHint hint = iota
	secretHint
	refusedHint
)

type selectionNotify struct {
	target   uint32
	property uint32
}

type offer struct {
	target uint32
	kind   uint32
	data   []byte
}

type heldCopy struct {
	text   []byte
	paths  []string
	offers []offer
	hint   hint
}

func (h heldCopy) holdsFiles() bool {
	return len(h.paths) > 0
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
	held         heldCopy
	extraOffers  map[uint32][]byte
	transfers    []*transfer
	readTimeout  time.Duration
	reader       atomic.Uint32
	wholeRead    time.Duration
	handleEvents func(event []byte)
}

func Open() (*Clipboard, error) {
	if value := os.Getenv("DISPLAY"); value != "" {
		return openNamed(value, authorityFile())
	}
	asking, cancel := context.WithTimeout(context.Background(), dialTimeout)
	defer cancel()
	if output, err := exec.CommandContext(asking, "systemctl", "--user", "show-environment").Output(); err == nil {
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
		wholeRead:   wholeReadTimeout,
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
		&b.atoms.clipboard:      "CLIPBOARD",
		&b.atoms.targets:        "TARGETS",
		&b.atoms.timestamp:      "TIMESTAMP",
		&b.atoms.timeProperty:   "LANHOP_TIME",
		&b.atoms.utf8String:     "UTF8_STRING",
		&b.atoms.text:           "TEXT",
		&b.atoms.plain:          "text/plain",
		&b.atoms.plainUTF8:      "text/plain;charset=utf-8",
		&b.atoms.incr:           "INCR",
		&b.atoms.passwordHint:   "x-kde-passwordManagerHint",
		&b.atoms.gnomeFiles:     "x-special/gnome-copied-files",
		&b.atoms.uriList:        "text/uri-list",
		&b.atoms.portalFiles:    "application/vnd.portal.files",
		&b.atoms.portalTransfer: "application/vnd.portal.filetransfer",
		&b.atoms.ownProperty:    "LANHOP_CLIPBOARD",
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
	if err := b.openReader(); err != nil {
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

func (b *Clipboard) Done() <-chan struct{} {
	return b.c.closed
}

func (b *Clipboard) Mark() int64 {
	b.owner()
	b.state.Lock()
	defer b.state.Unlock()
	return b.mark
}

func (b *Clipboard) Write(text string) (mark int64, err error) {
	return b.write(text, noHint)
}

func (b *Clipboard) write(text string, offered hint) (int64, error) {
	return b.own(b.textCopy([]byte(text), offered))
}

func (b *Clipboard) WriteFiles(paths []string) (mark int64, err error) {
	return b.writeFiles(paths, noHint)
}

func (b *Clipboard) writeFiles(paths []string, offered hint) (int64, error) {
	held, err := b.filesCopy(paths, offered)
	if err != nil {
		return 0, err
	}
	return b.own(held)
}

func (b *Clipboard) own(held heldCopy) (int64, error) {
	now, err := b.serverTime()
	if err != nil {
		return 0, err
	}
	b.state.Lock()
	b.owned, b.ownedSince, b.held = true, now, held
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
		b.owned, b.held = false, heldCopy{}
		b.state.Unlock()
		return 0, ErrRefused
	}
	return b.Mark(), nil
}

func (b *Clipboard) textCopy(text []byte, offered hint) heldCopy {
	offers := []offer{
		{target: b.atoms.utf8String, kind: b.atoms.utf8String, data: text},
		{target: b.atoms.text, kind: b.atoms.utf8String, data: text},
		{target: b.atoms.plainUTF8, kind: b.atoms.plainUTF8, data: text},
		{target: b.atoms.plain, kind: b.atoms.plain, data: text},
	}
	if latin1, fitsLatin1 := utf8ToLatin1(text); fitsLatin1 {
		offers = append(offers, offer{target: atomString, kind: atomString, data: latin1})
	}
	return heldCopy{text: text, offers: offers, hint: offered}
}

func (b *Clipboard) filesCopy(paths []string, offered hint) (heldCopy, error) {
	if len(paths) == 0 {
		return heldCopy{}, clipboard.ErrEmpty
	}
	addresses := make([]string, len(paths))
	for i, path := range paths {
		if !filepath.IsAbs(path) {
			return heldCopy{}, fmt.Errorf("%w: %q", ErrNotAbsolute, path)
		}
		if strings.ContainsRune(path, 0) {
			return heldCopy{}, fmt.Errorf("%w: %q", ErrNULInPath, path)
		}
		addresses[i] = fileAddress(path)
	}
	gnomeList := []byte("copy\n" + strings.Join(addresses, "\n"))
	uriList := []byte(strings.Join(addresses, "\r\n") + "\r\n")
	offers := []offer{
		{target: b.atoms.gnomeFiles, kind: b.atoms.gnomeFiles, data: gnomeList},
		{target: b.atoms.uriList, kind: b.atoms.uriList, data: uriList},
	}
	return heldCopy{paths: slices.Clone(paths), offers: offers, hint: offered}, nil
}

func fileAddress(path string) string {
	return (&url.URL{Scheme: "file", Path: path}).String()
}

func (b *Clipboard) heldByLanhop(owner uint32) (heldCopy, bool) {
	b.state.Lock()
	defer b.state.Unlock()
	return b.held, owner == b.window && b.owned
}

func (b *Clipboard) Read() (string, error) {
	until := time.Now().Add(b.wholeRead)
	owner, err := b.owner()
	if err != nil {
		return "", err
	}
	if owner == atomNone {
		return "", clipboard.ErrEmpty
	}
	if held, mine := b.heldByLanhop(owner); mine {
		switch {
		case held.hint != noHint:
			return "", clipboard.ErrPrivate
		case held.holdsFiles():
			return "", clipboard.ErrNotText
		}
		return string(held.text), nil
	}
	offered, err := b.targetsOffered(until)
	if err != nil {
		return "", err
	}
	if b.markedPrivate(offered, until) {
		return "", clipboard.ErrPrivate
	}
	if b.offersFiles(offered, until) {
		return "", clipboard.ErrNotText
	}
	target, found := b.textTarget(offered)
	if !found {
		return "", clipboard.ErrNotText
	}
	data, err := b.convert(target, clipboard.Limit, until)
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

func (b *Clipboard) ReadFiles() ([]string, error) {
	until := time.Now().Add(b.wholeRead)
	owner, err := b.owner()
	if err != nil {
		return nil, err
	}
	if owner == atomNone {
		return nil, clipboard.ErrNotFiles
	}
	if held, mine := b.heldByLanhop(owner); mine {
		switch {
		case held.hint != noHint:
			return nil, clipboard.ErrPrivate
		case !held.holdsFiles():
			return nil, clipboard.ErrNotFiles
		}
		return slices.Clone(held.paths), nil
	}
	offered, err := b.targetsOffered(until)
	if err != nil {
		return nil, err
	}
	if b.markedPrivate(offered, until) {
		return nil, clipboard.ErrPrivate
	}
	target, found := b.fileListTarget(offered)
	if !found {
		return nil, clipboard.ErrNotFiles
	}
	list, err := b.convert(target, fileListLimit, until)
	switch {
	case errors.Is(err, errTargetRefused):
		return nil, clipboard.ErrNotFiles
	case errors.Is(err, clipboard.ErrTooLarge):
		return nil, fmt.Errorf("%w: the list is longer than %d bytes", clipboard.ErrTooManyFiles, fileListLimit)
	case err != nil:
		return nil, err
	}
	return pathsInList(string(list), target == b.atoms.gnomeFiles)
}

func (b *Clipboard) fileListTarget(offered []uint32) (uint32, bool) {
	for _, wanted := range []uint32{b.atoms.gnomeFiles, b.atoms.uriList} {
		if slices.Contains(offered, wanted) {
			return wanted, true
		}
	}
	return 0, false
}

func pathsInList(list string, startsWithCopyOrCut bool) ([]string, error) {
	rest := list
	if startsWithCopyOrCut {
		var action string
		action, rest, _ = strings.Cut(rest, "\n")
		if action = strings.Trim(action, " \t\r"); action != "copy" && action != "cut" {
			return nil, clipboard.ErrNotFiles
		}
	}
	var paths []string
	for rest != "" {
		var line string
		line, rest, _ = strings.Cut(rest, "\n")
		line = strings.Trim(line, " \t\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if len(paths) == clipboard.FilesAndFoldersLimit {
			return nil, clipboard.ErrTooManyFiles
		}
		path, onThisMachine := localPath(line)
		if !onThisMachine {
			return nil, clipboard.ErrNotFiles
		}
		paths = append(paths, path)
	}
	if len(paths) == 0 {
		return nil, clipboard.ErrNotFiles
	}
	return paths, nil
}

func localPath(address string) (string, bool) {
	if strings.ContainsAny(address, "?#") || strings.Contains(address, "%2f") || strings.Contains(address, "%2F") {
		return "", false
	}
	parsed, err := url.Parse(address)
	if err != nil || parsed.Scheme != "file" || parsed.User != nil {
		return "", false
	}
	if parsed.Host != "" && !strings.EqualFold(parsed.Host, "localhost") {
		return "", false
	}
	if !filepath.IsAbs(parsed.Path) || strings.ContainsRune(parsed.Path, 0) {
		return "", false
	}
	return parsed.Path, true
}

func (b *Clipboard) markedPrivate(offered []uint32, until time.Time) bool {
	if !slices.Contains(offered, b.atoms.passwordHint) {
		return false
	}
	marker, err := b.convert(b.atoms.passwordHint, len(privateHintValue)+16, until)
	return err != nil || string(marker) == privateHintValue
}

func (b *Clipboard) targetsOffered(until time.Time) ([]uint32, error) {
	data, err := b.convert(b.atoms.targets, 4096, until)
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

func (b *Clipboard) offersFiles(offered []uint32, until time.Time) bool {
	for _, files := range []uint32{b.atoms.gnomeFiles, b.atoms.portalFiles, b.atoms.portalTransfer} {
		if slices.Contains(offered, files) {
			return true
		}
	}
	if !slices.Contains(offered, b.atoms.uriList) {
		return false
	}
	list, err := b.convert(b.atoms.uriList, uriListLimit, until)
	if err != nil {
		return true
	}
	return everyAddressIsAFile(string(list))
}

func everyAddressIsAFile(list string) bool {
	addresses := 0
	for _, line := range strings.Split(list, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.HasPrefix(strings.ToLower(line), "file:") {
			return false
		}
		addresses++
	}
	return addresses > 0
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

func (b *Clipboard) openReader() error {
	window := b.c.newID()
	create := newRequest(opCreateWindow, 0).u32(window).u32(b.c.root).
		u16(0).u16(0).u16(1).u16(1).u16(0).u16(classInputOnly).u32(0).
		u32(attributeEventMask).u32(propertyChangeMask).done()
	if err := b.c.send(create); err != nil {
		return err
	}
	b.reader.Store(window)
	return nil
}

func (b *Clipboard) leaveTheOwnerNothingToWriteInto() {
	abandoned := b.reader.Load()
	if err := b.openReader(); err != nil {
		return
	}
	b.c.send(newRequest(opDestroyWindow, 0).u32(abandoned).done())
}

func (b *Clipboard) convert(target uint32, limit int, until time.Time) ([]byte, error) {
	if !time.Now().Before(until) {
		return nil, ErrNoAnswer
	}
	data, err := b.askTheOwner(target, limit, until)
	if errors.Is(err, ErrNoAnswer) || errors.Is(err, clipboard.ErrTooLarge) {
		b.leaveTheOwnerNothingToWriteInto()
	}
	return data, err
}

func (b *Clipboard) askTheOwner(target uint32, limit int, until time.Time) ([]byte, error) {
	reader := b.reader.Load()
	drain(b.notifies)
	if err := b.c.send(newRequest(opDeleteProperty, 0).u32(reader).u32(b.atoms.ownProperty).done()); err != nil {
		return nil, err
	}
	request := newRequest(opConvertSelection, 0).u32(reader).u32(b.atoms.clipboard).u32(target).u32(b.atoms.ownProperty).u32(currentTime).done()
	if err := b.c.send(request); err != nil {
		return nil, err
	}
	deadline := b.nextStepDeadline(until)
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
			return b.receive(limit, until)
		case <-deadline:
			return nil, ErrNoAnswer
		case <-b.c.closed:
			return nil, b.c.err()
		}
	}
}

func (b *Clipboard) receive(limit int, until time.Time) ([]byte, error) {
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
		if err := b.waitForNewValue(b.nextStepDeadline(until)); err != nil {
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

func (b *Clipboard) nextStepDeadline(until time.Time) <-chan time.Time {
	return time.After(min(b.readTimeout, time.Until(until)))
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
	reply, err := b.c.call(newRequest(opGetProperty, 1).u32(b.reader.Load()).u32(b.atoms.ownProperty).u32(0).u32(0).u32(units).done())
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
			b.owned, b.held = false, heldCopy{}
			b.state.Unlock()
		}
	case code == eventPropertyNotify:
		window := binary.LittleEndian.Uint32(event[4:8])
		property := binary.LittleEndian.Uint32(event[8:12])
		state := event[16]
		if window == b.window || window == b.reader.Load() {
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
	owned, since, held := b.owned, b.ownedSince, b.held
	b.state.Unlock()

	answered := uint32(atomNone)
	if owned && selection == b.atoms.clipboard {
		if b.provide(requestor, property, target, since, held) {
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

func (b *Clipboard) provide(requestor, property, target, since uint32, held heldCopy) bool {
	b.state.Lock()
	extra, isExtra := b.extraOffers[target]
	b.state.Unlock()
	if isExtra {
		b.changeProperty(requestor, property, target, 8, extra)
		return true
	}
	switch target {
	case b.atoms.targets:
		targets := []uint32{b.atoms.targets, b.atoms.timestamp}
		for _, offered := range held.offers {
			targets = append(targets, offered.target)
		}
		b.state.Lock()
		for target := range b.extraOffers {
			targets = append(targets, target)
		}
		b.state.Unlock()
		if held.hint != noHint {
			targets = append(targets, b.atoms.passwordHint)
		}
		data := make([]byte, 0, len(targets)*4)
		for _, atom := range targets {
			data = binary.LittleEndian.AppendUint32(data, atom)
		}
		b.changeProperty(requestor, property, atomAtom, 32, data)
		return true
	case b.atoms.timestamp:
		b.changeProperty(requestor, property, atomInteger, 32, binary.LittleEndian.AppendUint32(nil, since))
		return true
	case b.atoms.passwordHint:
		if held.hint != secretHint {
			return false
		}
		b.changeProperty(requestor, property, b.atoms.utf8String, 8, []byte(privateHintValue))
		return true
	}
	for _, offered := range held.offers {
		if offered.target == target {
			return b.give(requestor, property, offered.kind, offered.data)
		}
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

func (b *Clipboard) startTransfer(requestor, property, kind uint32, data []byte) {
	b.c.send(newRequest(opChangeWindowAttributes, 0).u32(requestor).u32(attributeEventMask).u32(propertyChangeMask).done())
	now := time.Now()
	b.state.Lock()
	b.transfers = slices.DeleteFunc(b.transfers, func(t *transfer) bool {
		return now.Sub(t.started) > transferLifetime || (t.requestor == requestor && t.property == property)
	})
	b.transfers = append(b.transfers, &transfer{requestor: requestor, property: property, kind: kind, data: data, started: now})
	b.state.Unlock()
	b.changeProperty(requestor, property, b.atoms.incr, 32, binary.LittleEndian.AppendUint32(nil, uint32(len(data))))
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
	lastToThisWindow := false
	if len(part) == 0 {
		b.transfers = slices.Delete(b.transfers, index, index+1)
		lastToThisWindow = !slices.ContainsFunc(b.transfers, func(t *transfer) bool { return t.requestor == requestor })
	}
	b.state.Unlock()
	b.changeProperty(requestor, property, current.kind, 8, part)
	if lastToThisWindow {
		b.c.send(newRequest(opChangeWindowAttributes, 0).u32(requestor).u32(attributeEventMask).u32(0).done())
	}
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
