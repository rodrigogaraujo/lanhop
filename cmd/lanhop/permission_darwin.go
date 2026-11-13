//go:build darwin && cgo

package main

import (
	"context"
	"errors"
	"os"
	"syscall"

	"github.com/rodrigogaraujo/lanhop/internal/macos"
)

func waitUntilAllowedToControl(ctx context.Context, waiting func()) error {
	return askUntilAllowed(ctx, macos.CanPost, waiting)
}

func waitUntilAllowedToShare(ctx context.Context, waiting func()) error {
	return askUntilAllowed(ctx, func() bool { return macos.CanListen() && macos.CanPost() }, waiting)
}

func askUntilAllowed(ctx context.Context, granted func() bool, waiting func()) error {
	err := waitForPermission(ctx, granted, waiting, permissionCheckInterval, freshProcessAfter)
	if errors.Is(err, errAskAgainInAFreshProcess) {
		return startAgainInPlace()
	}
	os.Unsetenv(alreadyWaitingVariable)
	return err
}

func startAgainInPlace() error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	return syscall.Exec(executable, os.Args, append(os.Environ(), alreadyWaitingVariable+"=1"))
}
