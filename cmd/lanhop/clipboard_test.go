package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
	"github.com/rodrigogaraujo/lanhop/internal/clipshare"
	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/trust"
)

func TestMain(m *testing.M) {
	openClipboard = nil
	os.Exit(m.Run())
}

type clipboardOfTheTest struct {
	mutex      sync.Mutex
	text       string
	mark       int64
	marksTaken int
}

func (c *clipboardOfTheTest) Mark() int64 {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.marksTaken++
	return c.mark
}

func (c *clipboardOfTheTest) Read() (string, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.text, nil
}

func (c *clipboardOfTheTest) Write(text string) (int64, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.text = text
	c.mark++
	return c.mark, nil
}

func (c *clipboardOfTheTest) personCopies(text string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.text = text
	c.mark++
}

func (c *clipboardOfTheTest) timesMarked() int {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.marksTaken
}

func useTheClipboard(t *testing.T, board clipshare.Clipboard) {
	t.Helper()
	previous := openClipboard
	openClipboard = func() (clipshare.Clipboard, error) { return board, nil }
	t.Cleanup(func() { openClipboard = previous })
}

func clipboardCommand(t *testing.T, args ...string) (stdout string, exitCode int) {
	t.Helper()
	var out, stderr bytes.Buffer
	exitCode = run(append([]string{"clipboard"}, args...), nobodyTyping(), &out, &stderr)
	if exitCode == 1 {
		t.Fatalf("lanhop clipboard %v failed: %s", args, stderr.String())
	}
	return out.String(), exitCode
}

func TestClipboardSharingIsOnAfterANewInstallAndTheCommandTouchesNothingToSaySo(t *testing.T) {
	directory := useConfigDirectory(t)

	if said, _ := clipboardCommand(t); said != "on\n" {
		t.Errorf("lanhop clipboard printed %q, want on", said)
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Errorf("the configuration directory was created: %v", err)
	}
}

func TestTheClipboardCommandSwitchesSharingOffAndOnWithAFileInTheConfigurationDirectory(t *testing.T) {
	directory := useConfigDirectory(t)
	switchFile := filepath.Join(directory, clipboardSwitchFileName)

	said, _ := clipboardCommand(t, "off")
	if !strings.Contains(said, "Clipboard sharing is off on this machine") {
		t.Errorf("lanhop clipboard off printed %q", said)
	}
	if contents, err := os.ReadFile(switchFile); err != nil || string(contents) != "off\n" {
		t.Errorf("the switch file holds %q, %v, want off", contents, err)
	}
	if said, _ := clipboardCommand(t); said != "off\n" {
		t.Errorf("lanhop clipboard printed %q after off, want off", said)
	}

	said, _ = clipboardCommand(t, "on")
	if !strings.Contains(said, "Clipboard sharing is on on this machine") {
		t.Errorf("lanhop clipboard on printed %q", said)
	}
	if _, err := os.Stat(switchFile); !os.IsNotExist(err) {
		t.Errorf("the switch file is still there after on: %v", err)
	}
	if said, _ := clipboardCommand(t, "on"); !strings.Contains(said, "is on") {
		t.Errorf("lanhop clipboard on, when already on, printed %q", said)
	}
}

func TestTheClipboardSwitchFileIsReadWithoutCareForCaseOrSpaces(t *testing.T) {
	path := filepath.Join(t.TempDir(), clipboardSwitchFileName)
	cases := map[string]bool{"off\n": false, " Off ": false, "OFF": false, "on\n": true, "": true}
	for contents, wantOn := range cases {
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		if on := clipboardSwitchedOn(path); on != wantOn {
			t.Errorf("a switch file holding %q reads as on = %v, want %v", contents, on, wantOn)
		}
	}
}

func TestTheClipboardCommandUsedWronglyShowsUsageAndTouchesNothing(t *testing.T) {
	for name, args := range map[string][]string{
		"an unknown word": {"maybe"},
		"two words":       {"on", "off"},
	} {
		t.Run(name, func(t *testing.T) {
			directory := useConfigDirectory(t)
			var stdout, stderr bytes.Buffer

			if code := run(append([]string{"clipboard"}, args...), nobodyTyping(), &stdout, &stderr); code != 2 {
				t.Errorf("exit code = %d, want 2", code)
			}
			if !strings.Contains(stderr.String(), "Usage:") {
				t.Errorf("stderr = %q, want the usage", stderr.String())
			}
			if _, err := os.Stat(directory); !os.IsNotExist(err) {
				t.Errorf("the configuration directory was created: %v", err)
			}
		})
	}
}

func TestHelpNamesTheClipboardCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"help"}, nobodyTyping(), &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	for _, mention := range []string{"lanhop clipboard ", "lanhop clipboard off", "lanhop clipboard on", "clipboard switch"} {
		if !strings.Contains(stdout.String(), mention) {
			t.Errorf("help does not mention %q", mention)
		}
	}
}

func TestShareSendsACopyMadeOnThisMachineToThePairedMachineThatGetsFocus(t *testing.T) {
	directory := useConfigDirectory(t)
	local := identityIn(t, directory)
	desk := identityIn(t, t.TempDir())
	trustAlready(t, directory, trust.Peer{Name: "desk", Fingerprint: desk.Fingerprint})
	board := &clipboardOfTheTest{}
	useTheClipboard(t, board)
	share := startShare(t)
	conn, err := dialShare(desk, local, share.address)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	if got := received(t, conn, 1); got[0] != event.Features(event.FeatureClipboard) {
		t.Fatalf("the paired machine first received %+v, want Features with bit 0", got[0])
	}
	if err := conn.Send(event.Features(event.FeatureClipboard)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	for deadline := time.Now().Add(testPatience); board.timesMarked() < 2; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("share never took a fresh mark when the paired machine said it takes copies")
		}
	}
	board.personCopies("copied on the machine that shares")
	share.input.pressesHotkeyUntilFocusLeaves(t, 2)

	var copies clipboard.Assembler
	for {
		frame := received(t, conn, 1)[0]
		text, progress, err := copies.Take(frame)
		if err != nil {
			t.Fatalf("the copy did not assemble: %v", err)
		}
		if progress == clipboard.Arrived {
			if text != "copied on the machine that shares" {
				t.Errorf("the paired machine got %q", text)
			}
			return
		}
	}
}

func TestShareSwitchedOffSaysThatItTakesNoCopies(t *testing.T) {
	directory := useConfigDirectory(t)
	local := identityIn(t, directory)
	desk := identityIn(t, t.TempDir())
	trustAlready(t, directory, trust.Peer{Name: "desk", Fingerprint: desk.Fingerprint})
	clipboardCommand(t, "off")
	useTheClipboard(t, &clipboardOfTheTest{})
	share := startShare(t)

	conn, err := dialShare(desk, local, share.address)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	if got := received(t, conn, 1); got[0] != event.Features(0) {
		t.Errorf("the paired machine first received %+v, want Features with bit 0 clear", got[0])
	}
}
