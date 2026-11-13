//go:build darwin && cgo

package main

import (
	"context"
	"os"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/macos"
)

const agentLabel = "com.github.rodrigogaraujo.lanhop"

func waitUntilAllowedToControl(ctx context.Context, waiting func()) error {
	return waitForPermission(ctx, macos.CanPost, waiting, permissionCheckInterval, giveUpForAFreshAnswerAfter())
}

func waitUntilAllowedToShare(ctx context.Context, waiting func()) error {
	canShare := func() bool { return macos.CanListen() && macos.CanPost() }
	return waitForPermission(ctx, canShare, waiting, permissionCheckInterval, giveUpForAFreshAnswerAfter())
}

func giveUpForAFreshAnswerAfter() time.Duration {
	if os.Getenv("XPC_SERVICE_NAME") == agentLabel {
		return freshAnswerAfter
	}
	return 0
}
