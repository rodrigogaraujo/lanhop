//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/x11"
)

type stayingSide struct {
	describedLocally
	mutex  sync.Mutex
	stays  int
	listen func(describing bool)
}

func (s *stayingSide) OnDescribingDrags(changed func(describing bool)) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.listen = changed
}

func (s *stayingSide) DragStayed() {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.stays++
}

func (s *stayingSide) timesStayed() int {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.stays
}

func TestADragThatStaysOnThisLinuxMachineAfterFrame23IsToldToTheCentral(t *testing.T) {
	shared := sharedDrags
	sharedDrags = &dragsHolder{open: func() (*x11.Drags, error) { return nil, errors.New("no X server") }}
	t.Cleanup(func() { sharedDrags = shared })
	side := &stayingSide{}
	log := &replayLog{}
	dragLeaves, stop := dragsOfThisSide(context.Background(), side, newDropReplay(keepingReplayLog{log}, nil), &screen{})
	defer stop()
	ending, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	dragLeaves(ending)

	if stays := side.timesStayed(); stays != 1 {
		t.Errorf("the central was told %d times that the drag stayed, want once", stays)
	}
	expectEntries(t, "the replay", onlyEvents(log.seen()), "escape down", "escape up", "left up")
}

func TestALinuxCentralOpensNoDrop(t *testing.T) {
	drops, release, err := openDropsFor(true, nil, &screen{})
	if release != nil {
		defer release()
	}

	if drops != nil || err != nil {
		t.Errorf("a Linux central opened %v, %v, want no drop and no error", drops, err)
	}
}

func TestTheHandOfTheEdgeMovesReleasesAndEscapesThroughTheReplay(t *testing.T) {
	log := &replayLog{}
	hand := replayHand{newDropReplay(keepingReplayLog{log}, nil)}

	for _, step := range []func() error{
		func() error { return hand.Nudge(1, 0) },
		func() error { return hand.Nudge(0, -1) },
		hand.Release,
		hand.Escape,
	} {
		if err := step(); err != nil {
			t.Fatalf("the hand failed: %v", err)
		}
	}

	expectEntries(t, "the replay", onlyEvents(log.seen()), "x +1", "y -1", "left up", "escape down", "escape up")
}

func TestAnXDNDDropCountsWhenTheTargetMayHaveTheFiles(t *testing.T) {
	cases := []struct {
		err                  error
		accepted, filesGiven bool
		dropped              bool
	}{
		{nil, false, false, true},
		{x11.ErrNotFinished, false, false, true},
		{x11.ErrDropFailed, true, true, true},
		{fmt.Errorf("after the drop: %w", x11.ErrClosed), true, true, true},
		{x11.ErrClosed, true, false, false},
		{x11.ErrClosed, false, true, false},
		{x11.ErrDragTooLong, true, true, false},
		{errors.New("something else"), true, true, false},
	}
	for _, c := range cases {
		if got := xdndDropHappened(c.err, c.accepted, c.filesGiven); got != c.dropped {
			t.Errorf("xdndDropHappened(%v, accepted %v, files given %v) = %v, want %v", c.err, c.accepted, c.filesGiven, got, c.dropped)
		}
	}
}

func TestAWaylandSessionIsToldFromItsVariablesAndElseFromTheUserManager(t *testing.T) {
	never := func() ([]byte, error) { return nil, errors.New("the user manager was asked") }
	cases := []struct {
		name        string
		environment map[string]string
		userManager func() ([]byte, error)
		wayland     bool
	}{
		{"WAYLAND_DISPLAY set", map[string]string{"WAYLAND_DISPLAY": "wayland-0", "DISPLAY": ":0"}, never, true},
		{"session type wayland", map[string]string{"XDG_SESSION_TYPE": "Wayland"}, never, true},
		{"session type x11", map[string]string{"XDG_SESSION_TYPE": "x11", "DISPLAY": ":0"}, never, false},
		{"only DISPLAY, the user manager says wayland", map[string]string{"DISPLAY": ":0"}, func() ([]byte, error) { return []byte("WAYLAND_DISPLAY=wayland-0\n"), nil }, true},
		{"only DISPLAY, the user manager says x11", map[string]string{"DISPLAY": ":0"}, func() ([]byte, error) { return []byte("DISPLAY=:0\nXDG_SESSION_TYPE=x11\n"), nil }, false},
		{"nothing, the user manager cannot answer", map[string]string{}, never, false},
	}
	for _, c := range cases {
		getenv := func(name string) string { return c.environment[name] }
		if got := waylandSessionIn(getenv, c.userManager); got != c.wayland {
			t.Errorf("%s: wayland = %v, want %v", c.name, got, c.wayland)
		}
	}
}

func TestTheEnvironmentOfTheUserManagerSaysWaylandOnlyWithAValue(t *testing.T) {
	cases := map[string]bool{
		"WAYLAND_DISPLAY=wayland-0\n":        true,
		"XDG_SESSION_TYPE=wayland\n":         true,
		"  XDG_SESSION_TYPE=WAYLAND  \n":     true,
		"XDG_SESSION_TYPE=x11\nDISPLAY=:0\n": false,
		"WAYLAND_DISPLAY=\n":                 false,
		"OTHER_WAYLAND_DISPLAY=wayland-0\n":  false,
		"":                                   false,
	}
	for environment, want := range cases {
		if got := saysWayland([]byte(environment)); got != want {
			t.Errorf("saysWayland(%q) = %v, want %v", environment, got, want)
		}
	}
}
