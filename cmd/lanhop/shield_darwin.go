//go:build darwin && cgo

package main

import (
	"context"
	"errors"
	"io"
)

var errNoShieldYet = errors.New("this build of lanhop has no window to shield the cursor with")

func shieldTheCursor(context.Context) (io.Closer, error) {
	return nil, errNoShieldYet
}
