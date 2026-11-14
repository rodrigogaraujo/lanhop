package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/arrival"
	"github.com/rodrigogaraujo/lanhop/internal/clipshare"
	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/features"
	"github.com/rodrigogaraujo/lanhop/internal/identity"
	"github.com/rodrigogaraujo/lanhop/internal/receiver"
	"github.com/rodrigogaraujo/lanhop/internal/trust"
)

const textAndFiles = event.FeatureClipboard | 1<<features.Files

func filesCommand(t *testing.T, args ...string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if code := run(append([]string{"files"}, args...), nobodyTyping(), &stdout, &stderr); code != 0 {
		t.Fatalf("lanhop files %v: exit code = %d, stderr = %q", args, code, stderr.String())
	}
	return stdout.String()
}

func useArrivalFolders(t *testing.T, directories ...string) {
	t.Helper()
	probe, err := arrival.Open(arrival.Config{Directory: filepath.Join(t.TempDir(), "probe")})
	if errors.Is(err, arrival.ErrCannotQuarantine) {
		t.Skipf("files cannot arrive with this build: %v", err)
	}
	if err != nil {
		t.Fatalf("arrival.Open: %v", err)
	}
	probe.Close()
	var mutex sync.Mutex
	left := slices.Clone(directories)
	previous := openArrivals
	openArrivals = func() (*arrival.Folder, error) {
		mutex.Lock()
		defer mutex.Unlock()
		if len(left) == 0 {
			return nil, errors.New("every arrival folder of this test is open already")
		}
		next := left[0]
		left = left[1:]
		return arrival.Open(arrival.Config{Directory: next})
	}
	t.Cleanup(func() { openArrivals = previous })
}

func arrivalFolderFails(t *testing.T, failure error) {
	t.Helper()
	previous := openArrivals
	openArrivals = func() (*arrival.Folder, error) { return nil, failure }
	t.Cleanup(func() { openArrivals = previous })
}

type clipboardsInTurn struct {
	mutex  sync.Mutex
	boards []clipshare.Clipboard
}

func (c *clipboardsInTurn) open() (clipshare.Clipboard, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if len(c.boards) == 0 {
		return nil, errors.New("every clipboard of this test is open already")
	}
	next := c.boards[0]
	c.boards = c.boards[1:]
	return next, nil
}

func useClipboardsInTurn(t *testing.T, boards ...clipshare.Clipboard) {
	t.Helper()
	previous := openClipboard
	openClipboard = (&clipboardsInTurn{boards: boards}).open
	t.Cleanup(func() { openClipboard = previous })
}

func waitUntil(t *testing.T, what string, happened func() bool, logs ...*screen) {
	t.Helper()
	for deadline := time.Now().Add(testPatience); !happened(); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			printed := make([]string, len(logs))
			for i, log := range logs {
				printed[i] = log.String()
			}
			t.Fatalf("waited in vain for %s, and the logs said %q", what, printed)
		}
	}
}

func arrivedEarlier(t *testing.T, arrivals, name string, ago time.Duration) string {
	t.Helper()
	content := []byte("kept for a while")
	staged := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(staged, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staged, "notes.txt"), content, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.MkdirAll(arrivals, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	arrived := filepath.Join(arrivals, name)
	if err := os.Rename(staged, arrived); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	record := fmt.Sprintf("%d %d\n", time.Now().Add(-ago).UnixNano(), len(content))
	if err := os.WriteFile(arrived+".complete", []byte(record), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return arrived
}

func entriesIn(t *testing.T, directory string) []string {
	t.Helper()
	listed, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	names := make([]string, len(listed))
	for i, entry := range listed {
		names[i] = entry.Name()
	}
	return names
}

func gone(path string) bool {
	_, err := os.Stat(path)
	return errors.Is(err, fs.ErrNotExist)
}

type replayNowhere struct{}

func (replayNowhere) Emit(...event.Event) error { return nil }

func (replayNowhere) Close() error { return nil }

func receiveThroughTheWiringOfReceive(t *testing.T, directory string, local *identity.Identity, sharer trust.Peer) *screen {
	t.Helper()
	log := &screen{}
	receiving, stop := context.WithCancel(context.Background())
	config, stopTheClipboard := receivingFrom(receiving, sharer, local, directory, new(features.Set), log)
	if config.Clipboard == nil {
		stop()
		t.Fatal("receive has no clipboard to share")
	}
	config.Replay = replayNowhere{}
	finished := make(chan error, 1)
	go func() {
		finished <- receiver.Run(receiving, config)
	}()
	t.Cleanup(func() {
		stop()
		select {
		case <-finished:
		case <-time.After(testPatience):
			t.Error("receiving did not stop")
		}
		stopTheClipboard()
	})
	return log
}

func TestFilesAreOnAfterANewInstallAndTheCommandTouchesNothingToSaySo(t *testing.T) {
	directory := useConfigDirectory(t)

	if said := filesCommand(t); said != "on\n" {
		t.Errorf("lanhop files printed %q, want on", said)
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Errorf("the configuration directory was created: %v", err)
	}
}

func TestTheFilesCommandSwitchesFilesOffAndOnWithAFileNextToTheClipboardSwitch(t *testing.T) {
	directory := useConfigDirectory(t)
	switchFile := filepath.Join(directory, filesSwitchFileName)

	said := filesCommand(t, "off")
	if !strings.Contains(said, "Copies of files no longer move to or from this machine, and copies of text still do.") {
		t.Errorf("lanhop files off printed %q", said)
	}
	if contents, err := os.ReadFile(switchFile); err != nil || string(contents) != "off\n" {
		t.Errorf("the switch file holds %q, %v, want off", contents, err)
	}
	if info, err := os.Stat(switchFile); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("the switch file has mode %v, %v, want 0600", info.Mode().Perm(), err)
	}
	if entries := entriesIn(t, directory); !slices.Equal(entries, []string{filesSwitchFileName}) {
		t.Errorf("the configuration directory holds %q, want only the switch file", entries)
	}
	if said := filesCommand(t); said != "off\n" {
		t.Errorf("lanhop files printed %q after off, want off", said)
	}
	if said, _ := clipboardCommand(t); said != "on for text, off for files\n" {
		t.Errorf("lanhop clipboard printed %q while files are off, want on for text, off for files", said)
	}
	if said, _ := clipboardCommand(t, "on"); !strings.Contains(said, "Copies of files stay off. Let them move too with: lanhop files on") {
		t.Errorf("lanhop clipboard on printed %q while files are off, want it to point to lanhop files on", said)
	}

	said = filesCommand(t, "on")
	if !strings.Contains(said, "Copies of files move to and from this machine.") {
		t.Errorf("lanhop files on printed %q", said)
	}
	if _, err := os.Stat(switchFile); !os.IsNotExist(err) {
		t.Errorf("the switch file is still there after on: %v", err)
	}
	if said, _ := clipboardCommand(t); said != "on\n" {
		t.Errorf("lanhop clipboard printed %q after files on, want on", said)
	}
}

func TestFilesSwitchedOnWhileTheClipboardIsOffStayOffUntilTheClipboardIsOn(t *testing.T) {
	useConfigDirectory(t)
	clipboardCommand(t, "off")
	filesCommand(t, "off")

	said := filesCommand(t, "on")
	if !strings.Contains(said, "Copies of files stay off until the clipboard is on again. Switch it on with: lanhop clipboard on") {
		t.Errorf("lanhop files on printed %q while the clipboard is off", said)
	}
	if said := filesCommand(t); said != "off, because lanhop clipboard is off\n" {
		t.Errorf("lanhop files printed %q while the clipboard is off", said)
	}

	if said, _ := clipboardCommand(t, "on"); !strings.Contains(said, "Copies of text and files move to and from this machine.") {
		t.Errorf("lanhop clipboard on printed %q", said)
	}
	if said := filesCommand(t); said != "on\n" {
		t.Errorf("lanhop files printed %q after the clipboard is on again, want on", said)
	}
}

func TestTheFilesCommandUsedWronglyShowsUsageAndTouchesNothing(t *testing.T) {
	for name, args := range map[string][]string{
		"an unknown word": {"maybe"},
		"two words":       {"on", "off"},
	} {
		t.Run(name, func(t *testing.T) {
			directory := useConfigDirectory(t)
			var stdout, stderr bytes.Buffer

			if code := run(append([]string{"files"}, args...), nobodyTyping(), &stdout, &stderr); code != 2 {
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

func TestHelpNamesTheFilesCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"help"}, nobodyTyping(), &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	for _, mention := range []string{"lanhop files ", "lanhop files off", "lanhop files on", "files switch", "on for text, off for files"} {
		if !strings.Contains(stdout.String(), mention) {
			t.Errorf("help does not mention %q", mention)
		}
	}
}

func TestFilesTakePartOnlyWhileTheClipboardSwitchAndTheFilesSwitchAreBothOn(t *testing.T) {
	directory := useConfigDirectory(t)
	useTheClipboard(t, fileClipboardOfTheTest{&clipboardOfTheTest{}})
	config, release, supported := clipboardConfigIn(context.Background(), directory, new(features.Set), io.Discard)
	defer release()
	if !supported {
		t.Fatal("clipboardConfigIn found no clipboard to share")
	}
	expect := func(when string, wantText, wantFiles bool) {
		t.Helper()
		if text, files := config.SwitchedOn(), config.FilesOn(); text != wantText || files != wantFiles {
			t.Errorf("%s, text = %v and files = %v, want %v and %v", when, text, files, wantText, wantFiles)
		}
	}

	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, clipboardSwitchFileName), []byte("off\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	expect("on a machine whose copies were switched off before files existed", false, false)
	clipboardCommand(t, "on")
	expect("with both switches on", true, true)
	filesCommand(t, "off")
	expect("with files switched off", true, false)
	clipboardCommand(t, "off")
	expect("with both switches off", false, false)
	filesCommand(t, "on")
	expect("with the clipboard switched off", false, false)
}

func TestShareAnnouncesFilesOnlyWhileBothSwitchesAreOnAndItHasAFolderForThem(t *testing.T) {
	cases := []struct {
		name      string
		files     string
		folder    func(t *testing.T)
		announced int32
		logged    []string
	}{
		{
			name:      "files on",
			files:     "on",
			folder:    func(t *testing.T) { useArrivalFolders(t, filepath.Join(t.TempDir(), "arrived")) },
			announced: textAndFiles,
		},
		{
			name:      "files switched off",
			files:     "off",
			folder:    func(t *testing.T) { useArrivalFolders(t, filepath.Join(t.TempDir(), "arrived")) },
			announced: event.FeatureClipboard,
		},
		{
			name:      "an arrival folder that cannot open",
			files:     "on",
			folder:    func(t *testing.T) { arrivalFolderFails(t, arrival.ErrCannotQuarantine) },
			announced: event.FeatureClipboard,
			logged: []string{
				"lanhop cannot open the folder for the files that arrive: " + arrival.ErrCannotQuarantine.Error() + "\n",
				"File sharing is off on this machine, because lanhop has no folder to keep the files that arrive.\n",
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			directory := useConfigDirectory(t)
			local := identityIn(t, directory)
			desk := identityIn(t, t.TempDir())
			trustAlready(t, directory, trust.Peer{Name: "desk", Fingerprint: desk.Fingerprint})
			filesCommand(t, c.files)
			c.folder(t)
			useTheClipboard(t, fileClipboardOfTheTest{&clipboardOfTheTest{}})
			share := startShare(t)

			conn, err := dialShare(desk, local, share.address)
			if err != nil {
				t.Fatalf("Dial: %v", err)
			}
			defer conn.Close()

			if got := received(t, conn, 1); got[0] != event.Features(c.announced) {
				t.Errorf("the paired machine first received %+v, want %+v", got[0], event.Features(c.announced))
			}
			for _, line := range c.logged {
				share.output.waitUntilShown(t, line)
			}
		})
	}
}

func TestFilesSwitchedOffSayNothingOfAMissingArrivalFolderUntilTheyAreSwitchedOn(t *testing.T) {
	directory := useConfigDirectory(t)
	local := identityIn(t, directory)
	desk := identityIn(t, t.TempDir())
	trustAlready(t, directory, trust.Peer{Name: "desk", Fingerprint: desk.Fingerprint})
	filesCommand(t, "off")
	arrivalFolderFails(t, arrival.ErrCannotQuarantine)
	useTheClipboard(t, fileClipboardOfTheTest{&clipboardOfTheTest{}})
	share := startShare(t)
	conn, err := dialShare(desk, local, share.address)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	if got := received(t, conn, 1); got[0] != event.Features(event.FeatureClipboard) {
		t.Fatalf("the paired machine first received %+v, want text only", got[0])
	}
	expectNotShown(t, share.output, "lanhop cannot open the folder", "File sharing is off")

	filesCommand(t, "on")
	share.input.pressesHotkeyUntilFocusLeaves(t, 2)

	share.output.waitUntilShown(t, "lanhop cannot open the folder for the files that arrive: "+arrival.ErrCannotQuarantine.Error()+"\n")
	share.output.waitUntilShown(t, "File sharing is off on this machine, because lanhop has no folder to keep the files that arrive.\n")
}

func TestReceiveWhoseArrivalFolderCannotOpenSaysWhyOnceAndKeepsItsClipboard(t *testing.T) {
	cases := []struct {
		name    string
		failure error
		said    string
	}{
		{
			name:    "a build that cannot quarantine",
			failure: arrival.ErrCannotQuarantine,
			said:    "lanhop cannot open the folder for the files that arrive: " + arrival.ErrCannotQuarantine.Error() + "\n",
		},
		{
			name:    "a folder that another lanhop holds",
			failure: fmt.Errorf("%w: /home/ana/.cache/lanhop/arrived/.lock", arrival.ErrFolderInUse),
			said:    "Another lanhop on this machine is using the folder for files that arrive.\n",
		},
		{
			name:    "an error with control characters",
			failure: errors.New("arrival: a folder named \"x\x1b[2J\ny\""),
			said:    "lanhop cannot open the folder for the files that arrive: arrival: a folder named \"x\\x1b[2J\\ny\"\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			directory := useConfigDirectory(t)
			arrivalFolderFails(t, c.failure)
			useTheClipboard(t, fileClipboardOfTheTest{&clipboardOfTheTest{}})
			log := &screen{}

			config, stop := receivingFrom(context.Background(), trust.Peer{}, identityIn(t, t.TempDir()), directory, new(features.Set), log)
			defer stop()

			if config.Clipboard == nil {
				t.Fatal("receive gave up its clipboard because the arrival folder cannot open")
			}
			log.waitUntilShown(t, "File sharing is off on this machine, because lanhop has no folder to keep the files that arrive.\n")
			if said := strings.Count(log.String(), c.said); said != 1 {
				t.Errorf("the log said %q %d times, want once: %q", c.said, said, log.String())
			}
		})
	}
}

func TestShareAndReceiveRemoveTheFilesThatArrivedMoreThanADayAgoOnceTheyStarted(t *testing.T) {
	starts := map[string]func(t *testing.T){
		"share": func(t *testing.T) { startShare(t) },
		"receive": func(t *testing.T) {
			_, stop := receivingFrom(context.Background(), trust.Peer{}, identityIn(t, t.TempDir()), t.TempDir(), new(features.Set), &screen{})
			t.Cleanup(stop)
		},
	}
	for name, start := range starts {
		t.Run(name, func(t *testing.T) {
			useConfigDirectory(t)
			arrivals := filepath.Join(t.TempDir(), "arrived")
			old := arrivedEarlier(t, arrivals, "5-00112233445566aa", 48*time.Hour)
			recent := arrivedEarlier(t, arrivals, "6-00112233445566bb", time.Hour)
			useArrivalFolders(t, arrivals)
			useTheClipboard(t, fileClipboardOfTheTest{&clipboardOfTheTest{}})

			start(t)

			waitUntil(t, "the files that arrived two days ago to be removed", func() bool { return gone(old) })
			if gone(recent) {
				t.Errorf("the files that arrived an hour ago were removed from %s", recent)
			}
		})
	}
}

func removingOldArrivals(t *testing.T, arrivals string, every time.Duration) *screen {
	t.Helper()
	useArrivalFolders(t, arrivals)
	folder, err := openArrivals()
	if err != nil {
		t.Fatalf("openArrivals: %v", err)
	}
	log := &screen{}
	stop := runUntilStopped(context.Background(), func(ctx context.Context) {
		keepRemovingOldArrivals(ctx, folder, every, log)
	})
	t.Cleanup(func() {
		stopped := make(chan struct{})
		go func() {
			stop()
			close(stopped)
		}()
		select {
		case <-stopped:
		case <-time.After(testPatience):
			t.Error("the removal of old arrivals kept running after it was stopped")
		}
		folder.Close()
	})
	return log
}

func TestOldArrivalsAreRemovedAsSoonAsTheRemovalStarts(t *testing.T) {
	arrivals := filepath.Join(t.TempDir(), "arrived")
	old := arrivedEarlier(t, arrivals, "7-00112233445566cc", 48*time.Hour)

	log := removingOldArrivals(t, arrivals, time.Hour)

	waitUntil(t, "the files that arrived two days ago to be removed", func() bool { return gone(old) }, log)
}

func TestOldArrivalsAreRemovedAgainAtEachInterval(t *testing.T) {
	arrivals := filepath.Join(t.TempDir(), "arrived")
	log := removingOldArrivals(t, arrivals, 10*time.Millisecond)

	old := arrivedEarlier(t, arrivals, "8-00112233445566dd", 48*time.Hour)

	waitUntil(t, "the files that arrived two days ago to be removed", func() bool { return gone(old) }, log)
}

func TestEachDistinctFailureToRemoveOldArrivalsIsLoggedOnce(t *testing.T) {
	arrivals := filepath.Join(t.TempDir(), "arrived")
	log := removingOldArrivals(t, arrivals, 5*time.Millisecond)
	t.Cleanup(func() { os.Chmod(arrivals, 0o700) })
	failure := func(mode string) string {
		return "lanhop could not remove the files that arrived more than a day ago: " + arrival.ErrUnsafeDirectory.Error() + ": " + arrivals + " can be written by others, with mode " + mode + "\n"
	}

	if err := os.Chmod(arrivals, 0o777); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	log.waitUntilShown(t, failure("-rwxrwxrwx"))
	time.Sleep(50 * time.Millisecond)
	if err := os.Chmod(arrivals, 0o770); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	log.waitUntilShown(t, failure("-rwxrwx---"))
	time.Sleep(50 * time.Millisecond)

	for _, mode := range []string{"-rwxrwxrwx", "-rwxrwx---"} {
		if said := strings.Count(log.String(), failure(mode)); said != 1 {
			t.Errorf("the failure with mode %s was logged %d times, want once: %q", mode, said, log.String())
		}
	}
}

func TestAFileCopiedOnTheMachineThatSharesArrivesWholeOnTheReceivingMachineThatGetsFocus(t *testing.T) {
	directory := useConfigDirectory(t)
	local := identityIn(t, directory)
	deskDirectory := t.TempDir()
	desk := identityIn(t, deskDirectory)
	trustAlready(t, directory, trust.Peer{Name: "desk", Fingerprint: desk.Fingerprint})
	deskArrivals := filepath.Join(t.TempDir(), "arrived")
	useArrivalFolders(t, filepath.Join(t.TempDir(), "arrived"), deskArrivals)
	sharing, receiving := &clipboardOfTheTest{}, &clipboardOfTheTest{}
	useClipboardsInTurn(t, fileClipboardOfTheTest{sharing}, fileClipboardOfTheTest{receiving})
	share := startShare(t)
	waitUntil(t, "share to open its clipboard", func() bool { return sharing.timesMarked() >= 1 }, share.output)
	deskLog := receiveThroughTheWiringOfReceive(t, deskDirectory, desk, trust.Peer{Name: "sharer", Fingerprint: local.Fingerprint, Address: share.address})
	waitUntil(t, "the desk to hear that share takes copies", func() bool { return receiving.timesMarked() >= 2 }, share.output, deskLog)
	waitUntil(t, "share to take a fresh mark for the desk", func() bool { return sharing.timesMarked() >= 2 }, share.output, deskLog)
	report := filepath.Join(t.TempDir(), "report of march.txt")
	content := bytes.Repeat([]byte("numbers of the quarter\n"), 10000)
	if err := os.WriteFile(report, content, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	sharing.personCopiesFiles(report)
	share.input.pressesHotkeyUntilFocusLeaves(t, 2)

	waitUntil(t, "the file to reach the clipboard of the desk", func() bool { return len(receiving.filesWritten()) > 0 }, share.output, deskLog)
	arrived := receiving.filesWritten()[0]
	if len(arrived) != 1 || filepath.Base(arrived[0]) != filepath.Base(report) || !strings.HasPrefix(arrived[0], deskArrivals+string(filepath.Separator)) {
		t.Fatalf("the desk got %q on its clipboard, want one %q inside %s", arrived, filepath.Base(report), deskArrivals)
	}
	if got, err := os.ReadFile(arrived[0]); err != nil || !bytes.Equal(got, content) {
		t.Errorf("the file that arrived holds %d bytes, %v, want the %d bytes that were copied", len(got), err, len(content))
	}
}
