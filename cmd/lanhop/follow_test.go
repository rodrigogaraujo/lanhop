package main

import (
	"crypto/ed25519"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/trust"
	"github.com/rodrigogaraujo/lanhop/internal/update"
)

func TestUpdatesSwitchesOffAndOnAgain(t *testing.T) {
	directory := useConfigDirectory(t)
	for _, step := range []struct {
		args  []string
		shows string
	}{
		{nil, "on\n"},
		{[]string{"off"}, "stays on its version"},
		{nil, "off\n"},
		{[]string{"on"}, "follows the version"},
		{nil, "on\n"},
	} {
		output := &screen{}
		if err := switchUpdates(step.args, output); err != nil {
			t.Fatalf("lanhop updates %q: %v", step.args, err)
		}
		if !strings.Contains(output.String(), step.shows) {
			t.Errorf("lanhop updates %q printed %q, want %q", step.args, output.String(), step.shows)
		}
	}
	if _, err := os.Stat(filepath.Join(directory, updatesFileName)); !os.IsNotExist(err) {
		t.Errorf("the switch file is still there after lanhop updates on: %v", err)
	}
	var misuse usageError
	if err := switchUpdates([]string{"maybe"}, io.Discard); !errors.As(err, &misuse) {
		t.Errorf("lanhop updates maybe = %v, want a usage error", err)
	}
}

func TestOnlyALanhopStartedByItsServiceWithAKeyAndUpdatesOnFollows(t *testing.T) {
	directory := useConfigDirectory(t)
	keys := projectReleaseKeys
	t.Cleanup(func() { projectReleaseKeys = keys })
	follows := followsTheCentral(directory)

	projectReleaseKeys = []ed25519.PublicKey{make(ed25519.PublicKey, ed25519.PublicKeySize)}
	t.Setenv(serviceVariable, "1")
	if !follows() {
		t.Errorf("a lanhop started by its service, with a key and updates on, does not follow")
	}
	t.Setenv(serviceVariable, "")
	if follows() {
		t.Errorf("a lanhop started by hand follows")
	}
	t.Setenv(serviceVariable, "1")
	if err := writeUpdatesSwitch(directory, false); err != nil {
		t.Fatal(err)
	}
	if follows() {
		t.Errorf("a lanhop with updates off follows")
	}
	if err := writeUpdatesSwitch(directory, true); err != nil {
		t.Fatal(err)
	}
	projectReleaseKeys = nil
	if follows() {
		t.Errorf("a lanhop without a release key follows")
	}
}

func TestARestartForAnUpdateEndsWithStatus3(t *testing.T) {
	if code := exitCodeFor(errRestartForTheUpdate, io.Discard); code != 3 {
		t.Errorf("exit status %d after the update, want 3, which the service takes as an error and restarts", code)
	}
}

func TestShareSendsUpdateToAnOlderSideMachineThatFollows(t *testing.T) {
	running := version
	version = "v0.1.3"
	t.Cleanup(func() { version = running })
	directory := useConfigDirectory(t)
	local := identityIn(t, directory)
	desk := identityIn(t, t.TempDir())
	trustAlready(t, directory, trust.Peer{Name: "desk", Fingerprint: desk.Fingerprint})
	share := startShare(t)

	conn, err := dialShare(desk, local, share.address)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	if err := conn.Send(event.Features(1<<8), event.Version(update.Version{Major: 0, Minor: 1, Patch: 2}.Value())); err != nil {
		t.Fatalf("Send: %v", err)
	}
	arrived := make(chan event.Event, 1)
	go func() {
		for {
			frame, err := conn.Receive()
			if err != nil {
				return
			}
			if frame.IsControl() && frame.Code == event.ControlUpdate {
				arrived <- frame
				return
			}
		}
	}()
	select {
	case frame := <-arrived:
		if frame.Value != (update.Version{Major: 0, Minor: 1, Patch: 3}).Value() {
			t.Errorf("Update asks for %d, want v0.1.3", frame.Value)
		}
	case <-time.After(testPatience):
		t.Fatal("lanhop share never asked the older side machine to update")
	}
}

func TestAnOldSideMachineThatSaysNothingKeepsItsInputAndGetsNoUpdate(t *testing.T) {
	running := version
	version = "v0.1.3"
	t.Cleanup(func() { version = running })
	directory := useConfigDirectory(t)
	local := identityIn(t, directory)
	old := identityIn(t, t.TempDir())
	trustAlready(t, directory, trust.Peer{Name: "old", Fingerprint: old.Fingerprint})
	share := startShare(t)

	conn, err := dialShare(old, local, share.address)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	frames := make(chan event.Event, 64)
	go func() {
		for {
			frame, err := conn.Receive()
			if err != nil {
				close(frames)
				return
			}
			frames <- frame
		}
	}()
	share.input.pressesHotkeyUntilFocusLeaves(t, 2)

	sawEnter := false
	for deadline := time.After(time.Second); ; {
		select {
		case frame, open := <-frames:
			if !open {
				t.Fatal("the connection ended")
			}
			if frame.IsControl() && frame.Code == event.ControlUpdate {
				t.Fatalf("an old side machine that sent no Version was asked to update")
			}
			if frame == event.Enter() {
				sawEnter = true
			}
			continue
		case <-deadline:
		}
		break
	}
	if !sawEnter {
		t.Errorf("the old side machine never got focus")
	}
}
