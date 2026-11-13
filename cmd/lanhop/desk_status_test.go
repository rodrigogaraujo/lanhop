package main

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/follow"
	"github.com/rodrigogaraujo/lanhop/internal/status"
	"github.com/rodrigogaraujo/lanhop/internal/trust"
	"github.com/rodrigogaraujo/lanhop/internal/update"
)

func TestShareWritesWhenEachMachineLeftAndForgetsItWhenItComesBack(t *testing.T) {
	directory := useConfigDirectory(t)
	clock := time.Date(2026, 11, 13, 14, 0, 0, 0, time.UTC)
	report := reportReceivers(openStatus(), func() time.Time { return clock }, nil)

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

	lines := strings.Split(desk, "\n")
	for _, line := range []string{
		"  server      Ctrl+Alt+2   connected",
		"  office-mac  Ctrl+Alt+3   not connected since 14:02",
		"  radix       Ctrl+Alt+5   not connected since 12 Nov 09:30",
		"  ntt         Ctrl+Alt+6   not connected since lanhop started",
		"  spare-j     no hotkey    not connected since lanhop started",
	} {
		if !slices.Contains(lines, line) {
			t.Errorf("the desk reads\n%s\nwant the line %q", desk, line)
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
	lines := strings.Split(rest, "\n")
	for _, line := range []string{"  server      Ctrl+Alt+2   connected", "  office-mac  Ctrl+Alt+3   not connected since lanhop started"} {
		if !slices.Contains(lines, line) {
			t.Errorf("lanhop status printed %q after its first line, want the line %q", rest, line)
		}
	}
}

func TestStatusOnASideMachineListsNoDesk(t *testing.T) {
	directory := useConfigDirectory(t)
	trustAlready(t, directory, trust.Peer{Name: "imac", Fingerprint: someFingerprint(1), Address: centralAddress}, trust.Peer{Name: "laptop", Fingerprint: someFingerprint(2)})
	openStatus().set(status.Report{State: status.Connected, Machines: []string{"imac"}, Address: centralAddress})
	if shown := lanhopStatus(t); strings.Count(shown, "\n") != 1 {
		t.Errorf("lanhop status on a side machine printed %q, want its one line", shown)
	}
}

func TestShareForgetsWhenAMachineThatIsNoLongerTrustedLeft(t *testing.T) {
	directory := useConfigDirectory(t)
	trustAlready(t, directory, trust.Peer{Name: "desk", Fingerprint: someFingerprint(1)})
	report := reportReceivers(openStatus(), time.Now, peersOnDisk(filepath.Join(directory, peersFileName)))

	report([]string{"desk", "laptop"})
	report(nil)

	read, err := status.Read(directory)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(read.Left) != 1 || read.Left[0].Name != "desk" {
		t.Errorf("departures %+v, want only desk, because laptop is not trusted any more", read.Left)
	}
}

func statusShows(t *testing.T, line *regexp.Regexp) {
	t.Helper()
	shown := ""
	for deadline := time.Now().Add(testPatience); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if shown = lanhopStatus(t); line.MatchString(shown) {
			return
		}
	}
	t.Fatalf("lanhop status printed %q, want a line that matches %s", shown, line)
}

func TestStatusFollowsAMachineThatConnectsLeavesAndComesBack(t *testing.T) {
	directory := useConfigDirectory(t)
	local := identityIn(t, directory)
	desk := identityIn(t, t.TempDir())
	trustAlready(t, directory, trust.Peer{Name: "desk", Fingerprint: desk.Fingerprint}, trustedLaptop)
	share := startShare(t)

	first, err := dialShare(desk, local, share.address)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	statusShows(t, regexp.MustCompile(`(?m)^  desk    Ctrl\+Alt\+2   connected$`))
	statusShows(t, regexp.MustCompile(`(?m)^  laptop  Ctrl\+Alt\+3   not connected since lanhop started$`))

	first.Close()
	statusShows(t, regexp.MustCompile(`(?m)^  desk    Ctrl\+Alt\+2   not connected since [0-9]{2}:[0-9]{2}$`))

	second, err := dialShare(desk, local, share.address)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer second.Close()
	statusShows(t, regexp.MustCompile(`(?m)^  desk    Ctrl\+Alt\+2   connected$`))
}

func TestTheDeskShowsTheVersionAndMarkOfEachConnectedMachine(t *testing.T) {
	peers := []trust.Peer{{Name: "server", Fingerprint: someFingerprint(1)}, {Name: "old", Fingerprint: someFingerprint(2)}}
	report := status.Report{
		State:    status.Sharing,
		Machines: []string{"server", "old"},
		Seats:    []status.Seat{{Name: "server", Version: "v0.1.3", Mark: "follows"}, {Name: "old", Mark: "needs the installer"}},
	}
	lines := strings.Split(describeDesk(peers, report, time.Now()), "\n")
	for _, line := range []string{"  server  Ctrl+Alt+2   connected, v0.1.3, follows", "  old     Ctrl+Alt+3   connected, needs the installer"} {
		if !slices.Contains(lines, line) {
			t.Errorf("the desk reads %q, want the line %q", lines, line)
		}
	}
}

func TestShareWritesTheVersionAndMarkOfEachSideMachine(t *testing.T) {
	directory := useConfigDirectory(t)
	report := newDeskStatus(openStatus(), time.Now, nil)
	report.receivers([]string{"office-mac"})
	report.seatsChanged([]follow.Seat{{Name: "office-mac", Version: update.Version{Major: 0, Minor: 1, Patch: 3}, Mark: follow.Follows}})

	read, err := status.Read(directory)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	want := []status.Seat{{Name: "office-mac", Version: "v0.1.3", Mark: "follows"}}
	if read.State != status.Sharing || !slices.Equal(read.Seats, want) || !slices.Equal(read.Machines, []string{"office-mac"}) {
		t.Errorf("status %+v, want sharing with office-mac on v0.1.3, following", read)
	}
}
