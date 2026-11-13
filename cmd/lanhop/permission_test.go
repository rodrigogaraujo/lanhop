package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/trust"
)

const (
	testCheckInterval = time.Millisecond
	testFreshAfter    = time.Hour
)

func TestPermissionThatIsThereAlreadyIsNotWaitedFor(t *testing.T) {
	waited := false
	err := waitForPermission(context.Background(), func() bool { return true }, func() { waited = true }, testCheckInterval, testFreshAfter)
	if err != nil || waited {
		t.Errorf("waitForPermission = %v, waited = %v, want nil and no wait", err, waited)
	}
}

func TestPermissionGivenLaterEndsTheWaitAfterOneNotice(t *testing.T) {
	var checks atomic.Int32
	granted := func() bool { return checks.Add(1) > 5 }
	notices := 0
	err := waitForPermission(context.Background(), granted, func() { notices++ }, testCheckInterval, testFreshAfter)
	if err != nil || notices != 1 {
		t.Errorf("waitForPermission = %v, notices = %d, want nil and one notice", err, notices)
	}
}

func TestPermissionThatDoesNotComeIsAskedForAgainFromAFreshProcess(t *testing.T) {
	err := waitForPermission(context.Background(), func() bool { return false }, func() {}, testCheckInterval, 20*testCheckInterval)
	if !errors.Is(err, errAskAgainInAFreshProcess) {
		t.Errorf("waitForPermission = %v, want %v", err, errAskAgainInAFreshProcess)
	}
}

func TestStoppingWhileWaitingIsNotAnError(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- waitForPermission(ctx, func() bool { return false }, func() {}, testCheckInterval, testFreshAfter)
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

func TestTheNoticeAboutThePermissionIsPrintedOnceAcrossFreshProcesses(t *testing.T) {
	useConfigDirectory(t)
	var stdout bytes.Buffer
	report := connectionReport{stdout: &stdout, record: openStatus(), sharer: trust.Peer{Name: "iMac", Address: centralAddress}}

	report.waitingForPermission()
	t.Setenv(alreadyWaitingVariable, "1")
	report.waitingForPermission()

	if got := strings.Count(stdout.String(), waitingForAccessibility); got != 1 {
		t.Errorf("the notice was printed %d times, want once:\n%s", got, stdout.String())
	}
	expectState(t, lanhopStatus(t), "accessibility")
}
