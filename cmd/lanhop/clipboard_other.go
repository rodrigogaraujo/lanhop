//go:build !linux && !(darwin && cgo)

package main

import "github.com/rodrigogaraujo/lanhop/internal/clipshare"

var openClipboard func() (clipshare.Clipboard, error)
