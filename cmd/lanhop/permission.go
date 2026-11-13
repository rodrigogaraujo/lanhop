package main

import (
	"context"
	"errors"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/status"
)

const waitingForAccessibility = "lanhop waits for the Accessibility permission: in System Settings > Privacy & Security > Accessibility, allow the program that starts lanhop, as docs/setup-macos.md explains. lanhop carries on by itself once it is allowed."

const (
	permissionCheckInterval = time.Second
	freshAnswerAfter        = 10 * time.Second
)

var errAskAgainAfterARestart = errors.New("lanhop ended to ask macOS again for the Accessibility permission, and launchd starts it again in a few seconds")

func waitForPermission(ctx context.Context, granted func() bool, waiting func(), interval, giveUpAfter time.Duration) error {
	if granted() {
		return nil
	}
	waiting()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var giveUp <-chan time.Time
	if giveUpAfter > 0 {
		timer := time.NewTimer(giveUpAfter)
		defer timer.Stop()
		giveUp = timer.C
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-giveUp:
			return errAskAgainAfterARestart
		case <-ticker.C:
		}
		if granted() {
			return nil
		}
	}
}

func askedAgainAfterARestart(previous status.Report) bool {
	return previous.State == status.Stopped && previous.Error == errAskAgainAfterARestart.Error()
}
