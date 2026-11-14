//go:build linux

package x11

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
)

func startXvfb(t *testing.T) string {
	t.Helper()
	display, _ := startXvfbThatCanStop(t)
	return display
}

func startXvfbThatCanStop(t *testing.T) (string, func()) {
	t.Helper()
	server, err := exec.LookPath("Xvfb")
	if err != nil {
		t.Skip("Xvfb is not installed")
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	command := exec.Command(server, "-displayfd", "3", "-nolisten", "tcp")
	command.ExtraFiles = []*os.File{writer}
	if err := command.Start(); err != nil {
		t.Fatalf("start Xvfb: %v", err)
	}
	writer.Close()
	var stopping sync.Once
	stop := func() {
		stopping.Do(func() {
			command.Process.Kill()
			command.Wait()
		})
	}
	t.Cleanup(stop)
	number := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(reader).ReadString('\n')
		number <- strings.TrimSpace(line)
	}()
	select {
	case n := <-number:
		return ":" + n, stop
	case <-time.After(10 * time.Second):
		t.Fatal("Xvfb did not say which display it took")
		return "", stop
	}
}

func openOn(t *testing.T, display string) *Clipboard {
	t.Helper()
	board, err := openNamed(display, "")
	if err != nil {
		t.Fatalf("open %s: %v", display, err)
	}
	t.Cleanup(func() { board.Close() })
	return board
}

func twoPrograms(t *testing.T) (*Clipboard, *Clipboard) {
	t.Helper()
	display := startXvfb(t)
	return openOn(t, display), openOn(t, display)
}

func TestATextWrittenByOneProgramIsReadByAnother(t *testing.T) {
	writer, reader := twoPrograms(t)
	texts := map[string]string{
		"a word":                 "hello",
		"accents, emoji, kanji":  "olá, ação 🙂👍🏽 日本語",
		"tabs and several lines": "line one\r\nline two\n\tindented\n",
		"64 KiB, one piece":      strings.Repeat("x", 64<<10),
		"the limit, in parts":    strings.Repeat("y", clipboard.Limit),
	}
	for name, text := range texts {
		t.Run(name, func(t *testing.T) {
			if _, err := writer.Write(text); err != nil {
				t.Fatalf("Write: %v", err)
			}

			got, err := reader.Read()

			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			if got != text {
				t.Errorf("read %d bytes, want the same %d bytes", len(got), len(text))
			}
		})
	}
}

func TestAProgramReadsBackWhatItWrote(t *testing.T) {
	writer, _ := twoPrograms(t)
	if _, err := writer.Write("mine"); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if got, err := writer.Read(); err != nil || got != "mine" {
		t.Errorf("Read = %q, %v, want %q", got, err, "mine")
	}
}

func TestTwoCopiesFromTheSameProgramMoveTheMarkTwice(t *testing.T) {
	writer, watcher := twoPrograms(t)
	before := watcher.Mark()

	writer.Write("one")
	writer.Write("two")

	if got := watcher.Mark() - before; got != 2 {
		t.Errorf("the mark moved by %d, want 2", got)
	}
}

func TestAMarkTakenRightAfterACopyCountsIt(t *testing.T) {
	writer, watcher := twoPrograms(t)
	before := watcher.Mark()

	writer.Write("copied just before the hotkey")

	if watcher.Mark() == before {
		t.Error("a mark taken right after a copy did not count it")
	}
}

func TestAWriteReturnsTheMarkAfterItsOwnCopy(t *testing.T) {
	writer, _ := twoPrograms(t)

	mark, err := writer.Write("x")
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	if got := writer.Mark(); got != mark {
		t.Errorf("Mark after the write = %d, want the %d that Write returned", got, mark)
	}
}

func TestAClipboardThatNobodyOwnsIsEmpty(t *testing.T) {
	_, reader := twoPrograms(t)

	if _, err := reader.Read(); !errors.Is(err, clipboard.ErrEmpty) {
		t.Errorf("Read: error = %v, want %v", err, clipboard.ErrEmpty)
	}
}

func TestAnOwnerThatGoesAwayEmptiesTheClipboardAndMovesTheMark(t *testing.T) {
	writer, watcher := twoPrograms(t)
	writer.Write("x")
	before := watcher.Mark()

	writer.Close()

	deadline := time.Now().Add(2 * time.Second)
	for watcher.Mark() == before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if watcher.Mark() == before {
		t.Error("the mark did not move when the owner went away")
	}
	if _, err := watcher.Read(); !errors.Is(err, clipboard.ErrEmpty) {
		t.Errorf("Read: error = %v, want %v", err, clipboard.ErrEmpty)
	}
}

func TestACopyMarkedPrivateIsNotRead(t *testing.T) {
	writer, reader := twoPrograms(t)
	if _, err := writer.write("a password", secretHint); err != nil {
		t.Fatalf("write: %v", err)
	}

	if got, err := reader.Read(); !errors.Is(err, clipboard.ErrPrivate) || got != "" {
		t.Errorf("Read = %q, %v, want nothing and %v", got, err, clipboard.ErrPrivate)
	}
}

func TestACopyWhosePrivacyHintCannotBeReadIsTreatedAsPrivate(t *testing.T) {
	writer, reader := twoPrograms(t)
	if _, err := writer.write("hunter2", refusedHint); err != nil {
		t.Fatalf("write: %v", err)
	}

	if got, err := reader.Read(); !errors.Is(err, clipboard.ErrPrivate) || got != "" {
		t.Errorf("Read = %q, %v, want nothing and %v", got, err, clipboard.ErrPrivate)
	}
}

func TestLosingTheXServerClosesDone(t *testing.T) {
	display, stop := startXvfbThatCanStop(t)
	board := openOn(t, display)

	stop()

	select {
	case <-board.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("Done did not close when the X server went away")
	}
	if _, err := board.Read(); !errors.Is(err, ErrClosed) {
		t.Errorf("Read after the X server went away: error = %v, want %v", err, ErrClosed)
	}
}

func offering(writer *Clipboard, format func(b *Clipboard) uint32, data string) {
	writer.state.Lock()
	writer.extraOffers = map[uint32][]byte{format(writer): []byte(data)}
	writer.state.Unlock()
}

func TestACopyOfFilesIsNotTextEvenWithTheirPathsAsText(t *testing.T) {
	formats := map[string]struct {
		format func(b *Clipboard) uint32
		data   string
	}{
		"x-special/gnome-copied-files": {func(b *Clipboard) uint32 { return b.atoms.gnomeFiles }, "copy\nfile:///home/someone/report.pdf"},
		"text/uri-list of files":       {func(b *Clipboard) uint32 { return b.atoms.uriList }, "# copied\r\nfile:///home/someone/report.pdf\r\nFILE:///home/someone/notes.txt\r\n"},
		"application/vnd.portal.files": {func(b *Clipboard) uint32 { return b.atoms.portalFiles }, "token"},
	}
	for name, offer := range formats {
		t.Run(name, func(t *testing.T) {
			writer, reader := twoPrograms(t)
			offering(writer, offer.format, offer.data)
			writer.Write("/home/someone/report.pdf")

			if got, err := reader.Read(); !errors.Is(err, clipboard.ErrNotText) || got != "" {
				t.Errorf("Read = %q, %v, want nothing and %v", got, err, clipboard.ErrNotText)
			}
		})
	}
}

func TestALinkOfferedAsAnAddressListIsStillText(t *testing.T) {
	writer, reader := twoPrograms(t)
	offering(writer, func(b *Clipboard) uint32 { return b.atoms.uriList }, "https://example.org/page\r\n")
	writer.Write("https://example.org/page")

	if got, err := reader.Read(); err != nil || got != "https://example.org/page" {
		t.Errorf("Read = %q, %v, want the link", got, err)
	}
}

func TestAListWithAFileAndALinkIsStillText(t *testing.T) {
	if everyAddressIsAFile("file:///a\nhttps://example.org\n") {
		t.Error("a list with a web address counted as files")
	}
	if everyAddressIsAFile("# only a comment\n") {
		t.Error("a list with no address counted as files")
	}
}

func TestATextOverTheLimitIsNotRead(t *testing.T) {
	writer, reader := twoPrograms(t)
	writer.Write(strings.Repeat("z", clipboard.Limit+1))

	if _, err := reader.Read(); !errors.Is(err, clipboard.ErrTooLarge) {
		t.Errorf("Read: error = %v, want %v", err, clipboard.ErrTooLarge)
	}
}

func ownedByAnotherProgram(t *testing.T, display string, reader *Clipboard, answer func(owner *conn, event []byte)) {
	t.Helper()
	d, err := parseDisplay(display)
	if err != nil {
		t.Fatalf("parseDisplay: %v", err)
	}
	ready := make(chan struct{})
	var owner *conn
	owner, err = dial(d, nil, func(event []byte) {
		<-ready
		answer(owner, event)
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	close(ready)
	t.Cleanup(func() { owner.close() })
	window := owner.newID()
	owner.send(newRequest(opCreateWindow, 0).u32(window).u32(owner.root).u16(0).u16(0).u16(1).u16(1).u16(0).u16(classInputOnly).u32(0).u32(0).done())
	owner.send(newRequest(opSetSelectionOwner, 0).u32(window).u32(reader.atoms.clipboard).u32(currentTime).done())
	reply, err := owner.call(newRequest(opGetSelectionOwner, 0).u32(reader.atoms.clipboard).done())
	if err != nil || binary.LittleEndian.Uint32(reply[8:12]) != window {
		t.Fatalf("the other program does not own the clipboard: %v", err)
	}
}

func neverAnswering(*conn, []byte) {}

func tricklingATextForever(names atoms, every time.Duration) func(owner *conn, event []byte) {
	return func(owner *conn, event []byte) {
		switch event[0] & 0x7f {
		case eventSelectionRequest:
			requestor := binary.LittleEndian.Uint32(event[12:16])
			target := binary.LittleEndian.Uint32(event[20:24])
			property := binary.LittleEndian.Uint32(event[24:28])
			if target == names.targets {
				offered := binary.LittleEndian.AppendUint32(binary.LittleEndian.AppendUint32(nil, names.targets), names.utf8String)
				owner.send(newRequest(opChangeProperty, modeReplace).u32(requestor).u32(property).u32(atomAtom).u8(32).u8(0).u8(0).u8(0).u32(2).raw(offered).done())
			} else {
				owner.send(newRequest(opChangeWindowAttributes, 0).u32(requestor).u32(attributeEventMask).u32(propertyChangeMask).done())
				owner.send(newRequest(opChangeProperty, modeReplace).u32(requestor).u32(property).u32(names.incr).u8(32).u8(0).u8(0).u8(0).u32(1).raw(binary.LittleEndian.AppendUint32(nil, 1<<20)).done())
			}
			notify := make([]byte, 32)
			notify[0] = eventSelectionNotify
			copy(notify[4:8], event[4:8])
			copy(notify[8:12], event[12:16])
			copy(notify[12:16], event[16:20])
			copy(notify[16:20], event[20:24])
			copy(notify[20:24], event[24:28])
			owner.send(newRequest(opSendEvent, 0).u32(requestor).u32(0).raw(notify).done())
		case eventPropertyNotify:
			window := binary.LittleEndian.Uint32(event[4:8])
			property := binary.LittleEndian.Uint32(event[8:12])
			if event[16] == propertyDeleted {
				time.AfterFunc(every, func() {
					owner.send(newRequest(opChangeProperty, modeReplace).u32(window).u32(property).u32(names.utf8String).u8(8).u8(0).u8(0).u8(0).u32(1).raw([]byte("x")).done())
				})
			}
		}
	}
}

func TestAnOwnerThatNeverAnswersIsReportedInsteadOfAwaited(t *testing.T) {
	display := startXvfb(t)
	reader := openOn(t, display)
	ownedByAnotherProgram(t, display, reader, neverAnswering)
	reader.readTimeout = 200 * time.Millisecond

	start := time.Now()
	_, err := reader.Read()

	if !errors.Is(err, ErrNoAnswer) {
		t.Errorf("Read: error = %v, want %v", err, ErrNoAnswer)
	}
	if waited := time.Since(start); waited > time.Second {
		t.Errorf("Read waited %v", waited)
	}
}

func TestAnOwnerThatTricklesItsCopyForeverIsGivenUpWhenTheWholeReadRunsOut(t *testing.T) {
	display := startXvfb(t)
	reader := openOn(t, display)
	ownedByAnotherProgram(t, display, reader, tricklingATextForever(reader.atoms, 50*time.Millisecond))
	reader.readTimeout = 400 * time.Millisecond
	reader.wholeRead = 800 * time.Millisecond

	start := time.Now()
	failed := make(chan error, 1)
	go func() {
		_, err := reader.Read()
		failed <- err
	}()

	select {
	case err := <-failed:
		if !errors.Is(err, ErrNoAnswer) {
			t.Errorf("Read: error = %v, want %v", err, ErrNoAnswer)
		}
		if waited := time.Since(start); waited < reader.wholeRead || waited > reader.wholeRead+reader.readTimeout {
			t.Errorf("Read gave up after %v, want between %v and %v", waited, reader.wholeRead, reader.wholeRead+reader.readTimeout)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Read was still taking parts of a copy that never ends after 5 s")
	}
}

func TestWritingAndReading64KiBTakesLittleOfTheTimeACopyMayTake(t *testing.T) {
	const budget = 300 * time.Millisecond
	writer, reader := twoPrograms(t)
	text := strings.Repeat("w", 64<<10)

	start := time.Now()
	writer.Write(text)
	wrote := time.Since(start)
	if _, err := reader.Read(); err != nil {
		t.Fatalf("Read: %v", err)
	}
	both := time.Since(start)

	t.Logf("64 KiB: write %v, read %v", wrote, both-wrote)
	if both > budget/3 {
		t.Errorf("writing and reading 64 KiB took %v, more than a third of the %v that SC-001 allows a whole copy", both, budget)
	}
}

func TestAnotherX11ProgramReadsWhatLanhopWroteAndTheReverse(t *testing.T) {
	tool, err := exec.LookPath("xclip")
	if err != nil {
		t.Skip("xclip is not installed")
	}
	display := startXvfb(t)
	board := openOn(t, display)
	environment := append(os.Environ(), "DISPLAY="+display)

	for _, text := range []string{"from lanhop, olá 🙂", strings.Repeat("l", clipboard.Limit)} {
		board.Write(text)
		read := exec.Command(tool, "-selection", "clipboard", "-o")
		read.Env = environment
		got, err := read.Output()
		if err != nil || string(got) != text {
			t.Errorf("xclip read %d bytes, %v, want the %d bytes lanhop wrote", len(got), err, len(text))
		}
	}

	for _, text := range []string{"from xclip", strings.Repeat("c", clipboard.Limit)} {
		write := exec.Command(tool, "-selection", "clipboard", "-i")
		write.Env = environment
		write.Stdin = strings.NewReader(text)
		if err := write.Run(); err != nil {
			t.Fatalf("xclip -i: %v", err)
		}
		if got, err := board.Read(); err != nil || got != text {
			t.Errorf("lanhop read %d bytes, %v, want the %d bytes xclip wrote", len(got), err, len(text))
		}
	}
}

func TestOnlyDisplaysWhoseSocketsBelongToTrustedUsersAreTried(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{"X3", "X1", "Xnot", "other"} {
		listener, err := net.Listen("unix", filepath.Join(directory, name))
		if err != nil {
			t.Fatalf("Listen: %v", err)
		}
		t.Cleanup(func() { listener.Close() })
	}
	os.WriteFile(filepath.Join(directory, "X2"), nil, 0o600)
	me := uint32(os.Getuid())

	mine := displaysOnThisMachine(directory, me)
	if len(mine) != 2 || mine[0].number != "1" || mine[1].number != "3" {
		t.Errorf("displays owned by me = %+v, want 1 then 3", mine)
	}
	if others := displaysOnThisMachine(directory, me+1); len(others) != 0 {
		t.Errorf("displays owned by another user = %+v, want none", others)
	}
}

func TestAnXServerRunByThisUserIsTrusted(t *testing.T) {
	display := startXvfb(t)
	d, _ := parseDisplay(display)

	socket, err := net.Dial("unix", d.socket)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer socket.Close()

	if err := runByRootOrMe(socket); err != nil {
		t.Errorf("runByRootOrMe: %v", err)
	}
}

func TestDisplaysAreReadFromTheirName(t *testing.T) {
	tests := []struct {
		value   string
		number  string
		problem error
	}{
		{":0", "0", nil},
		{":1.0", "1", nil},
		{"unix:12", "12", nil},
		{"localhost:10.0", "", ErrRemoteDisplay},
		{"0", "", nil},
		{":x", "", nil},
	}
	for _, test := range tests {
		d, err := parseDisplay(test.value)
		switch {
		case test.number != "":
			if err != nil || d.number != test.number || d.socket != filepath.Join(socketDirectory, "X"+test.number) {
				t.Errorf("parseDisplay(%q) = %+v, %v", test.value, d, err)
			}
		case test.problem != nil:
			if !errors.Is(err, test.problem) {
				t.Errorf("parseDisplay(%q) error = %v, want %v", test.value, err, test.problem)
			}
		default:
			if err == nil {
				t.Errorf("parseDisplay(%q) gave %+v, want an error", test.value, d)
			}
		}
	}
}

func authorityEntry(family uint16, address, number, name string, secret []byte) []byte {
	field := func(b []byte) []byte { return append([]byte{byte(len(b) >> 8), byte(len(b))}, b...) }
	entry := []byte{byte(family >> 8), byte(family)}
	entry = append(entry, field([]byte(address))...)
	entry = append(entry, field([]byte(number))...)
	entry = append(entry, field([]byte(name))...)
	return append(entry, field(secret)...)
}

func TestTheCookieOfADisplayIsTheOneForItsNumber(t *testing.T) {
	file := append(authorityEntry(familyLocal, "desk", "1", cookieName, []byte("one")),
		authorityEntry(familyLocal, "desk", "0", "XDM-AUTHORIZATION-1", []byte("other kind"))...)
	file = append(file, authorityEntry(familyWild, "", "0", cookieName, []byte("zero"))...)

	file = append(file, authorityEntry(familyLocal, "another-machine", "3", cookieName, []byte("theirs"))...)

	if got, err := cookieFor("0", "desk", file); err != nil || string(got) != "zero" {
		t.Errorf("cookie of display 0 = %q, %v", got, err)
	}
	if got, err := cookieFor("1", "desk", file); err != nil || string(got) != "one" {
		t.Errorf("cookie of display 1 = %q, %v", got, err)
	}
	if _, err := cookieFor("2", "desk", file); err == nil {
		t.Error("display 2 got a cookie that belongs to another display")
	}
	if _, err := cookieFor("3", "desk", file); err == nil {
		t.Error("display 3 got the cookie of another machine with a shared home directory")
	}
	if _, err := cookieFor("0", "desk", file[:5]); err == nil {
		t.Error("a cut file gave a cookie")
	}
}

func TestTheSessionIsFoundInTheEnvironmentOfTheServiceManager(t *testing.T) {
	output := []byte("HOME=/home/someone\nDISPLAY=:0\nXAUTHORITY=/home/someone/.Xauthority\nLANG=C\n")

	display, authority := sessionEnvironment(output)

	if display != ":0" || authority != "/home/someone/.Xauthority" {
		t.Errorf("sessionEnvironment = %q, %q", display, authority)
	}
}

func TestOnTheDesk(t *testing.T) {
	if os.Getenv("LANHOP_TEST_REAL_X11") != "1" {
		t.Skip("set LANHOP_TEST_REAL_X11=1 to write test texts to the clipboard of the session in DISPLAY")
	}
	first, err := Open()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer first.Close()
	second, err := Open()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer second.Close()

	for i, pair := range []struct{ writer, reader *Clipboard }{{first, second}, {second, first}} {
		text := fmt.Sprintf("lanhop test text %d, %s", i+1, time.Now().Format(time.RFC3339Nano))
		before := pair.reader.Mark()
		if _, err := pair.writer.Write(text); err != nil {
			t.Fatalf("Write: %v", err)
		}
		if pair.reader.Mark() == before {
			t.Error("the mark of the other program did not move")
		}
		if got, err := pair.reader.Read(); err != nil || got != text {
			t.Errorf("Read = %q, %v, want %q", got, err, text)
		}
	}
}
