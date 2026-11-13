//go:build darwin && cgo

package main

import (
	"context"

	"github.com/rodrigogaraujo/lanhop/internal/macos"
)

func waitUntilAllowedToControl(ctx context.Context, waiting func()) error {
	return waitForPermission(ctx, macos.CanPost, macos.IsTrusted, waiting, permissionCheckInterval, errNotAllowedToControl)
}

func waitUntilAllowedToShare(ctx context.Context, waiting func()) error {
	canShare := func() bool { return macos.CanListen() && macos.CanPost() }
	return waitForPermission(ctx, canShare, macos.IsTrusted, waiting, permissionCheckInterval, errNotAllowedToShare)
}
