package main

import (
	"bytes"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/sharer"
	"github.com/rodrigogaraujo/lanhop/internal/status"
	"github.com/rodrigogaraujo/lanhop/internal/trust"
)

const (
	dragCannotReachLaptop = `Dragging a file cannot reach "laptop". Copying the file and pasting it there works instead.`
	filesOffOnTablet      = `Dragging a file cannot reach "tablet": file sharing is off on that machine.`
	dragCannotLeaveHere   = "A file dragged on this machine cannot leave it. Copying the file and pasting it on the other machine works instead."
)

func TestShareSaysInItsLogAndItsStatusWhatADraggedFileCannotDo(t *testing.T) {
	directory := useConfigDirectory(t)
	report := newDeskStatus(openStatus(), time.Now, nil)
	report.receivers([]string{"laptop"})
	var log bytes.Buffer
	tell := tellBlockedDrags(&log, report)

	tell(sharer.BlockedDrag{Machine: "laptop", Block: sharer.DragCannotReach})
	tell(sharer.BlockedDrag{Machine: "tablet", Block: sharer.FilesOffThere})
	tell(sharer.BlockedDrag{Machine: "me", Block: sharer.DragCannotLeave})
	report.receivers(nil)

	read, err := status.Read(directory)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if read.State != status.Waiting || !slices.Equal(read.DragCannotReach, []string{"laptop"}) || !slices.Equal(read.DragFilesOff, []string{"tablet"}) || !read.DragCannotLeave {
		t.Errorf("status %+v, want waiting, with laptop out of reach of a dragged file, file sharing off on tablet and no drag leaving this machine", read)
	}
	if want := dragCannotReachLaptop + "\n" + filesOffOnTablet + "\n" + dragCannotLeaveHere + "\n"; log.String() != want {
		t.Errorf("the log reads %q, want %q", log.String(), want)
	}
}

func TestTheDeskStatusKeepsEachMachineADraggedFileCannotReachOnce(t *testing.T) {
	directory := useConfigDirectory(t)
	report := newDeskStatus(openStatus(), time.Now, nil)

	report.blockedDrag(sharer.BlockedDrag{Machine: "laptop", Block: sharer.DragCannotReach})
	report.blockedDrag(sharer.BlockedDrag{Machine: "laptop", Block: sharer.DragCannotReach})
	report.blockedDrag(sharer.BlockedDrag{Machine: "tablet", Block: sharer.DragCannotReach})
	report.blockedDrag(sharer.BlockedDrag{Machine: "laptop", Block: sharer.FilesOffThere})
	report.blockedDrag(sharer.BlockedDrag{Machine: "laptop", Block: sharer.FilesOffThere})

	read, err := status.Read(directory)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !slices.Equal(read.DragCannotReach, []string{"laptop", "tablet"}) || !slices.Equal(read.DragFilesOff, []string{"laptop"}) || read.DragCannotLeave {
		t.Errorf("status %+v, want laptop and tablet out of reach once each, file sharing off on laptop once, and nothing about leaving", read)
	}
}

func TestStatusOnTheCentralSaysWhatADraggedFileCannotDoAfterTheDesk(t *testing.T) {
	directory := useConfigDirectory(t)
	trustAlready(t, directory, trust.Peer{Name: "laptop", Fingerprint: someFingerprint(1)}, trust.Peer{Name: "tablet", Fingerprint: someFingerprint(2)})
	openStatus().set(status.Report{
		State:           status.Sharing,
		Machines:        []string{"laptop", "tablet"},
		DragCannotReach: []string{"laptop"},
		DragFilesOff:    []string{"tablet"},
		DragCannotLeave: true,
	})

	lines := strings.Split(lanhopStatus(t), "\n")
	seat := slices.Index(lines, "  tablet  Ctrl+Alt+3   connected")
	leave := slices.Index(lines, dragCannotLeaveHere)
	reach := slices.Index(lines, dragCannotReachLaptop)
	off := slices.Index(lines, filesOffOnTablet)
	if seat < 0 || leave <= seat || reach <= leave || off <= reach {
		t.Errorf("lanhop status printed\n%s\nwant the desk, then that a drag cannot leave, that it cannot reach laptop, and that file sharing is off on tablet", strings.Join(lines, "\n"))
	}
}

func TestStatusSaysNothingAboutDraggedFilesWhenNoneWasStopped(t *testing.T) {
	if got := describeDrags(status.Report{State: status.Sharing, Machines: []string{"laptop"}}); got != "" {
		t.Errorf("describeDrags = %q, want nothing", got)
	}
}

func TestStatusOnASideMachineSaysNothingAboutDraggedFiles(t *testing.T) {
	directory := useConfigDirectory(t)
	trustAlready(t, directory, trust.Peer{Name: "imac", Fingerprint: someFingerprint(1), Address: centralAddress})
	openStatus().set(status.Report{State: status.Connected, Machines: []string{"imac"}, Address: centralAddress, DragCannotReach: []string{"laptop"}})
	if shown := lanhopStatus(t); strings.Contains(shown, "Dragging") {
		t.Errorf("lanhop status on a side machine printed %q, want nothing about dragged files", shown)
	}
}
