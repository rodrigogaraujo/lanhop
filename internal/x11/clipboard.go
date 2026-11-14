//go:build linux

package x11

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"sync"
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
	retiredAtMost    = 8
	largestChunk     = 64 << 10
	privateHintValue = "secret"
	uriListLimit     = 64 << 10
	fileListLimit    = 16 << 20
)

var (
	ErrNoAnswer    = errors.New("x11: the program that owns the clipboard or the drag did not answer")
	ErrRefused     = errors.New("x11: the X server did not let lanhop own the clipboard")
	ErrNoXFixes    = errors.New("x11: the X server has no XFIXES extension")
	ErrNotAbsolute = errors.New("x11: a file is not given by an absolute path")
	ErrNULInPath   = errors.New("x11: a file path holds a NUL byte")
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

type Clipboard struct {
	*selectionReader
	c            *conn
	window       uint32
	atoms        atoms
	xfixesEvent  byte
	writer       *propertyWriter
	times        chan uint32
	state        sync.Mutex
	mark         int64
	owned        bool
	ownedSince   uint32
	held         heldCopy
	extraOffers  map[uint32][]byte
	wholeRead    time.Duration
	handleEvents func(event []byte)
}

func Open() (*Clipboard, error) {
	return openSession(open)
}

func openNamed(value, authority string) (*Clipboard, error) {
	return openNamedWith(open, value, authority)
}

func open(d display, cookie []byte) (*Clipboard, error) {
	board := &Clipboard{
		selectionReader: newSelectionReader(),
		times:           make(chan uint32, 8),
		wholeRead:       wholeReadTimeout,
	}
	c, err := dial(d, cookie, board.handle)
	if err != nil {
		return nil, err
	}
	board.c = c
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
	b.writer = newPropertyWriter(b.c, b.atoms.incr)

	xfixes, firstEvent, err := queryXFixes(b.c)
	if err != nil {
		return err
	}
	b.xfixesEvent = firstEvent
	if err := agreeXFixesVersion(b.c, xfixes); err != nil {
		return err
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
	if err := b.attach(b.c, b.atoms.clipboard, b.atoms.ownProperty, b.atoms.incr); err != nil {
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
	addresses, err := fileAddresses(paths)
	if err != nil {
		return heldCopy{}, err
	}
	gnomeList := []byte("copy\n" + strings.Join(addresses, "\n"))
	offers := []offer{
		{target: b.atoms.gnomeFiles, kind: b.atoms.gnomeFiles, data: gnomeList},
		{target: b.atoms.uriList, kind: b.atoms.uriList, data: addressList(addresses)},
	}
	return heldCopy{paths: slices.Clone(paths), offers: offers, hint: offered}, nil
}

func fileAddresses(paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, clipboard.ErrEmpty
	}
	addresses := make([]string, len(paths))
	for i, path := range paths {
		if !filepath.IsAbs(path) {
			return nil, fmt.Errorf("%w: %q", ErrNotAbsolute, path)
		}
		if strings.ContainsRune(path, 0) {
			return nil, fmt.Errorf("%w: %q", ErrNULInPath, path)
		}
		addresses[i] = fileAddress(path)
	}
	return addresses, nil
}

func addressList(addresses []string) []byte {
	return []byte(strings.Join(addresses, "\r\n") + "\r\n")
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
	return b.readPaths(target, currentTime, target == b.atoms.gnomeFiles, until)
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
	offered, err := b.offeredTargets(b.atoms.targets, currentTime, until)
	if errors.Is(err, errTargetRefused) {
		return []uint32{b.atoms.utf8String}, nil
	}
	return offered, err
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

func (b *Clipboard) serverTime() (uint32, error) {
	drain(b.times)
	b.writer.changeMode(b.window, b.atoms.timeProperty, atomString, 8, nil, modeAppend)
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
	return ownerOf(b.c, b.atoms.clipboard)
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
		b.notified(event)
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
		if window == b.window {
			if state == propertyNewValue && property == b.atoms.timeProperty {
				push(b.times, binary.LittleEndian.Uint32(event[12:16]))
			}
			return
		}
		if b.propertyChanged(window, property, state) {
			return
		}
		if state == propertyDeleted {
			b.writer.propertyDeleted(window, property)
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
	notifySelection(b.c, when, requestor, selection, target, answered)
}

func (b *Clipboard) provide(requestor, property, target, since uint32, held heldCopy) bool {
	b.state.Lock()
	extra, isExtra := b.extraOffers[target]
	b.state.Unlock()
	if isExtra {
		b.writer.change(requestor, property, target, 8, extra)
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
		b.writer.change(requestor, property, atomAtom, 32, data)
		return true
	case b.atoms.timestamp:
		b.writer.change(requestor, property, atomInteger, 32, binary.LittleEndian.AppendUint32(nil, since))
		return true
	case b.atoms.passwordHint:
		if held.hint != secretHint {
			return false
		}
		b.writer.change(requestor, property, b.atoms.utf8String, 8, []byte(privateHintValue))
		return true
	}
	for _, offered := range held.offers {
		if offered.target == target {
			b.writer.give(requestor, property, offered.kind, offered.data)
			return true
		}
	}
	return false
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
