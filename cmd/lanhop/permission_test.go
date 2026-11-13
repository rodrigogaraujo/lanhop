package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

const testCheckInterval = time.Millisecond

var errStalePermission = errors.New("the permission was given but this process does not see it")

func TestPermissionThatIsThereAlreadyIsNotWaitedFor(t *testing.T) {
	waited := false
	err := waitForPermission(context.Background(), func() bool { return true }, func() bool { return true }, func() { waited = true }, testCheckInterval, errStalePermission)
	if err != nil || waited {
		t.Errorf("waitForPermission = %v, waited = %v, want nil and no wait", err, waited)
	}
}

func TestPermissionGivenLaterEndsTheWaitAfterOneNotice(t *testing.T) {
	var checks atomic.Int32
	granted := func() bool { return checks.Add(1) > 5 }
	notices := 0
	err := waitForPermission(context.Background(), granted, func() bool { return false }, func() { notices++ }, testCheckInterval, errStalePermission)
	if err != nil || notices != 1 {
		t.Errorf("waitForPermission = %v, notices = %d, want nil and one notice", err, notices)
	}
}

func TestPermissionThatThisProcessCannotSeeEndsItSoThatANewOneStarts(t *testing.T) {
	err := waitForPermission(context.Background(), func() bool { return false }, func() bool { return true }, func() {}, testCheckInterval, errStalePermission)
	if !errors.Is(err, errStalePermission) {
		t.Errorf("waitForPermission = %v, want %v", err, errStalePermission)
	}
}

func TestStoppingWhileWaitingIsNotAnError(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- waitForPermission(ctx, func() bool { return false }, func() bool { return false }, func() {}, testCheckInterval, errStalePermission)
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
