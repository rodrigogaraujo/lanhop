//go:build linux

package x11

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
)

func startXvfb(t *testing.T) string {
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
	t.Cleanup(func() {
		command.Process.Kill()
		command.Wait()
	})
	number := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(reader).ReadString('\n')
		number <- strings.TrimSpace(line)
	}()
	select {
	case n := <-number:
		return ":" + n
	case <-time.After(10 * time.Second):
		t.Fatal("Xvfb did not say which display it took")
		return ""
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
	if _, err := writer.write("a password", true); err != nil {
		t.Fatalf("write: %v", err)
	}

	if got, err := reader.Read(); !errors.Is(err, clipboard.ErrPrivate) || got != "" {
		t.Errorf("Read = %q, %v, want nothing and %v", got, err, clipboard.ErrPrivate)
	}
}

func TestATextOverTheLimitIsNotRead(t *testing.T) {
	writer, reader := twoPrograms(t)
	writer.Write(strings.Repeat("z", clipboard.Limit+1))

	if _, err := reader.Read(); !errors.Is(err, clipboard.ErrTooLarge) {
		t.Errorf("Read: error = %v, want %v", err, clipboard.ErrTooLarge)
	}
}

func TestAnOwnerThatNeverAnswersIsReportedInsteadOfAwaited(t *testing.T) {
	display := startXvfb(t)
	d, err := parseDisplay(display)
	if err != nil {
		t.Fatalf("parseDisplay: %v", err)
	}
	silent, err := dial(d, nil, func([]byte) {})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { silent.close() })
	reader := openOn(t, display)
	window := silent.newID()
	silent.send(newRequest(opCreateWindow, 0).u32(window).u32(silent.root).u16(0).u16(0).u16(1).u16(1).u16(0).u16(classInputOnly).u32(0).u32(0).done())
	silent.send(newRequest(opSetSelectionOwner, 0).u32(window).u32(reader.atoms.clipboard).u32(currentTime).done())
	reader.readTimeout = 200 * time.Millisecond

	start := time.Now()
	_, err = reader.Read()

	if !errors.Is(err, ErrNoAnswer) {
		t.Errorf("Read: error = %v, want %v", err, ErrNoAnswer)
	}
	if waited := time.Since(start); waited > time.Second {
		t.Errorf("Read waited %v", waited)
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

	board.Write("from lanhop, olá 🙂")
	read := exec.Command(tool, "-selection", "clipboard", "-o")
	read.Env = environment
	got, err := read.Output()
	if err != nil || string(got) != "from lanhop, olá 🙂" {
		t.Errorf("xclip read %q, %v", got, err)
	}

	write := exec.Command(tool, "-selection", "clipboard", "-i")
	write.Env = environment
	write.Stdin = strings.NewReader("from xclip")
	if err := write.Run(); err != nil {
		t.Fatalf("xclip -i: %v", err)
	}
	if text, err := board.Read(); err != nil || text != "from xclip" {
		t.Errorf("lanhop read %q, %v, want %q", text, err, "from xclip")
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

	if got, err := cookieFor("0", file); err != nil || string(got) != "zero" {
		t.Errorf("cookie of display 0 = %q, %v", got, err)
	}
	if got, err := cookieFor("1", file); err != nil || string(got) != "one" {
		t.Errorf("cookie of display 1 = %q, %v", got, err)
	}
	if _, err := cookieFor("2", file); err == nil {
		t.Error("display 2 got a cookie that belongs to another display")
	}
	if _, err := cookieFor("0", file[:5]); err == nil {
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
