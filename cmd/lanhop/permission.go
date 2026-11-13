package main

import (
	"context"
	"errors"
	"os"
	"time"
)

const waitingForAccessibility = "lanhop waits for the Accessibility permission: in System Settings > Privacy & Security > Accessibility, allow the program that starts lanhop, as docs/setup-macos.md explains. lanhop carries on by itself once it is allowed."

const (
	permissionCheckInterval = time.Second
	freshProcessAfter       = 10 * time.Second
	alreadyWaitingVariable  = "LANHOP_WAITING_FOR_PERMISSION"
)

var errAskAgainInAFreshProcess = errors.New("lanhop asks macOS again for the permission from a fresh process")

func waitForPermission(ctx context.Context, granted func() bool, waiting func(), interval, freshAfter time.Duration) error {
	if granted() {
		return nil
	}
	waiting()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	askAgain := time.NewTimer(freshAfter)
	defer askAgain.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-askAgain.C:
			return errAskAgainInAFreshProcess
		case <-ticker.C:
		}
		if granted() {
			return nil
		}
	}
}

func toldAboutThePermissionAlready() bool {
	return os.Getenv(alreadyWaitingVariable) != ""
}
