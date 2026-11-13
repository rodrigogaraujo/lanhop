package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/status"
	"github.com/rodrigogaraujo/lanhop/internal/trust"
)

const (
	testCheckInterval = time.Millisecond
	testWaitForever   = time.Duration(0)
)

func TestPermissionThatIsThereAlreadyIsNotWaitedFor(t *testing.T) {
	waited := false
	err := waitForPermission(context.Background(), func() bool { return true }, func() { waited = true }, testCheckInterval, testWaitForever)
	if err != nil || waited {
		t.Errorf("waitForPermission = %v, waited = %v, want nil and no wait", err, waited)
	}
}

func TestPermissionGivenLaterEndsTheWaitAfterOneNotice(t *testing.T) {
	var checks atomic.Int32
	granted := func() bool { return checks.Add(1) > 5 }
	notices := 0
	err := waitForPermission(context.Background(), granted, func() { notices++ }, testCheckInterval, testWaitForever)
	if err != nil || notices != 1 {
		t.Errorf("waitForPermission = %v, notices = %d, want nil and one notice", err, notices)
	}
}

func TestServiceWhosePermissionDoesNotComeEndsSoThatLaunchdStartsAFreshOne(t *testing.T) {
	err := waitForPermission(context.Background(), func() bool { return false }, func() {}, testCheckInterval, 20*testCheckInterval)
	if !errors.Is(err, errAskAgainAfterARestart) {
		t.Errorf("waitForPermission = %v, want %v", err, errAskAgainAfterARestart)
	}
}

func TestLanhopStartedByHandKeepsWaitingForThePermission(t *testing.T) {
	ctx, stop := context.WithTimeout(context.Background(), 30*testCheckInterval)
	defer stop()
	err := waitForPermission(ctx, func() bool { return false }, func() {}, testCheckInterval, testWaitForever)
	if err != nil {
		t.Errorf("waitForPermission = %v, want it to wait until stopped", err)
	}
}

func TestStoppingWhileWaitingIsNotAnError(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- waitForPermission(ctx, func() bool { return false }, func() {}, testCheckInterval, testWaitForever)
	}()
	stop()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("waitForPermission = %v after a stop, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the wait did not end when it was stopped")
	}
}

func TestOnlyALanhopThatEndedToAskAgainIsQuietAboutThePermission(t *testing.T) {
	for _, previous := range []struct {
		report status.Report
		quiet  bool
	}{
		{status.Report{State: status.Stopped, Error: errAskAgainAfterARestart.Error()}, true},
		{status.Report{State: status.Stopped, Error: "this machine is not paired"}, false},
		{status.Report{State: status.Accessibility}, false},
		{status.Report{}, false},
	} {
		if got := askedAgainAfterARestart(previous.report); got != previous.quiet {
			t.Errorf("askedAgainAfterARestart(%+v) = %v, want %v", previous.report, got, previous.quiet)
		}
	}
}

func TestTheNoticeAboutThePermissionIsNotRepeatedByTheLanhopThatLaunchdStartedAgain(t *testing.T) {
	useConfigDirectory(t)
	var stdout bytes.Buffer
	first := connectionReport{stdout: &stdout, record: openStatus(), sharer: trust.Peer{Name: "iMac", Address: centralAddress}}
	first.waitingForPermission()
	first.record.ended(errAskAgainAfterARestart)

	again := connectionReport{stdout: &stdout, record: openStatus(), sharer: trust.Peer{Name: "iMac", Address: centralAddress}}
	again.quiet = askedAgainAfterARestart(again.record.lastReport())
	again.waitingForPermission()

	if got := strings.Count(stdout.String(), waitingForAccessibility); got != 1 {
		t.Errorf("the notice was printed %d times, want once:\n%s", got, stdout.String())
	}
	expectState(t, lanhopStatus(t), "accessibility")
}
