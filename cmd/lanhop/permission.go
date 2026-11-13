package main

import (
	"context"
	"time"
)

const waitingForAccessibility = "lanhop waits for the Accessibility permission: in System Settings > Privacy & Security > Accessibility, allow the program that starts lanhop, as docs/setup-macos.md explains. lanhop carries on by itself once it is allowed."

const (
	permissionCheckInterval  = time.Second
	staleChecksBeforeRestart = 3
)

func waitForPermission(ctx context.Context, granted, trusted func() bool, waiting func(), interval time.Duration, stale error) error {
	if granted() {
		return nil
	}
	waiting()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	staleChecks := 0
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		if granted() {
			return nil
		}
		if !trusted() {
			staleChecks = 0
			continue
		}
		staleChecks++
		if staleChecks >= staleChecksBeforeRestart {
			return stale
		}
	}
}
