package main

import (
	"strings"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/status"
	"github.com/rodrigogaraujo/lanhop/internal/trust"
)

func TestShareWritesWhenEachMachineLeftAndForgetsItWhenItComesBack(t *testing.T) {
	directory := useConfigDirectory(t)
	clock := time.Date(2026, 11, 13, 14, 0, 0, 0, time.UTC)
	report := reportReceivers(openStatus(), func() time.Time { return clock })

	report([]string{"desk", "laptop"})
	clock = clock.Add(2 * time.Minute)
	report([]string{"desk"})
	clock = clock.Add(3 * time.Minute)
	report(nil)

	read, err := status.Read(directory)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if read.State != status.Waiting || len(read.Left) != 2 {
		t.Fatalf("status %+v, want waiting with desk and laptop gone", read)
	}
	if read.Left[0].Name != "desk" || !read.Left[0].At.Equal(clock) || read.Left[1].Name != "laptop" || !read.Left[1].At.Equal(clock.Add(-3*time.Minute)) {
		t.Errorf("departures %+v, want desk at %v and laptop at %v", read.Left, clock, clock.Add(-3*time.Minute))
	}

	report([]string{"laptop"})
	read, err = status.Read(directory)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if read.State != status.Sharing || len(read.Left) != 1 || read.Left[0].Name != "desk" {
		t.Errorf("status %+v, want sharing with laptop and only desk gone", read)
	}
}

func TestTheDeskListsEachSideMachineWithItsHotkeyAndWhetherItIsConnected(t *testing.T) {
	now := time.Date(2026, 11, 13, 15, 0, 0, 0, time.Local)
	peers := []trust.Peer{
		{Name: "server", Fingerprint: someFingerprint(1)},
		{Name: "office-mac", Fingerprint: someFingerprint(2)},
		{Name: "imac", Fingerprint: someFingerprint(3), Address: "192.168.1.9:24745"},
		{Name: "radix", Fingerprint: someFingerprint(4)},
		{Name: "ntt", Fingerprint: someFingerprint(5)},
	}
	for seed := byte(6); seed <= 10; seed++ {
		peers = append(peers, trust.Peer{Name: "spare-" + string('a'+rune(seed)), Fingerprint: someFingerprint(seed)})
	}
	report := status.Report{
		State:    status.Sharing,
		Machines: []string{"server"},
		Left: []status.Departure{
			{Name: "office-mac", At: time.Date(2026, 11, 13, 14, 2, 0, 0, time.Local)},
			{Name: "radix", At: time.Date(2026, 11, 12, 9, 30, 0, 0, time.Local)},
		},
	}
	desk := describeDesk(peers, report, now)

	for _, line := range []string{"server      Ctrl+Alt+2   connected", "office-mac  Ctrl+Alt+3   not connected since 14:02", "radix       Ctrl+Alt+5   not connected since 12 Nov 09:30", "ntt         Ctrl+Alt+6   not connected since lanhop started", "no hotkey"} {
		if !strings.Contains(desk, line) {
			t.Errorf("the desk reads\n%s\nwant a line with %q", desk, line)
		}
	}
	if strings.Contains(desk, "imac") {
		t.Errorf("the desk lists imac, a machine this one connects to, in\n%s", desk)
	}
}

func TestStatusOnTheCentralListsTheDeskAfterItsFirstLine(t *testing.T) {
	directory := useConfigDirectory(t)
	trustAlready(t, directory, trust.Peer{Name: "server", Fingerprint: someFingerprint(1)}, trust.Peer{Name: "office-mac", Fingerprint: someFingerprint(2)})
	openStatus().set(status.Report{State: status.Sharing, Machines: []string{"server"}})

	shown := lanhopStatus(t)
	first, rest, _ := strings.Cut(shown, "\n")
	expectState(t, first, "sharing", `"server"`)
	for _, part := range []string{"server", "Ctrl+Alt+2", "connected", "office-mac", "Ctrl+Alt+3", "not connected since lanhop started"} {
		if !strings.Contains(rest, part) {
			t.Errorf("lanhop status printed %q after its first line, want %q", rest, part)
		}
	}
}

func TestStatusOnASideMachineListsNoDesk(t *testing.T) {
	directory := useConfigDirectory(t)
	trustAlready(t, directory, trust.Peer{Name: "imac", Fingerprint: someFingerprint(1), Address: centralAddress})
	openStatus().set(status.Report{State: status.Connected, Machines: []string{"imac"}, Address: centralAddress})
	if shown := lanhopStatus(t); strings.Count(shown, "\n") != 1 {
		t.Errorf("lanhop status on a side machine printed %q, want its one line", shown)
	}
}
