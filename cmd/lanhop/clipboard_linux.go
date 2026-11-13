//go:build linux

package main

import (
	"github.com/rodrigogaraujo/lanhop/internal/clipshare"
	"github.com/rodrigogaraujo/lanhop/internal/x11"
)

var openClipboard = openTheX11Clipboard

func openTheX11Clipboard() (clipshare.Clipboard, error) {
	board, err := x11.Open()
	if err != nil {
		return nil, err
	}
	return board, nil
}
