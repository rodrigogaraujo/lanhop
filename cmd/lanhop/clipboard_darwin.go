//go:build darwin && cgo

package main

import (
	"github.com/rodrigogaraujo/lanhop/internal/clipshare"
	"github.com/rodrigogaraujo/lanhop/internal/macos"
)

var openClipboard = func() (clipshare.Clipboard, error) {
	return macos.GeneralPasteboard(), nil
}
