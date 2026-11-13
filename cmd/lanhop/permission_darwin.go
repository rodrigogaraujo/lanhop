//go:build darwin && cgo

package main

import (
	"context"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/macos"
)

func waitUntilAllowedToControl(ctx context.Context, waiting func()) error {
	return waitForPermission(ctx, macos.CanPost, waiting, permissionCheckInterval, giveUpForAFreshAnswerAfter())
}

func waitUntilAllowedToShare(ctx context.Context, waiting func()) error {
	canShare := func() bool { return macos.CanListen() && macos.CanPost() }
	return waitForPermission(ctx, canShare, waiting, permissionCheckInterval, giveUpForAFreshAnswerAfter())
}

func giveUpForAFreshAnswerAfter() time.Duration {
	if startedByTheLaunchAgent() {
		return freshAnswerAfter
	}
	return 0
}
