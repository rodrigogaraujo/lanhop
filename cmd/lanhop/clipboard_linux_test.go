//go:build linux

package main

import "testing"

func TestAnX11ClipboardThatCannotBeReachedIsNoClipboardAtAll(t *testing.T) {
	t.Setenv("DISPLAY", ":4242")

	board, err := openTheX11Clipboard()

	if board != nil || err == nil {
		t.Errorf("openTheX11Clipboard = %#v, %v, want no clipboard and the reason", board, err)
	}
}
