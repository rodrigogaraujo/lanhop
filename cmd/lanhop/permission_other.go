//go:build !(darwin && cgo)

package main

import "context"

func waitUntilAllowedToControl(context.Context, func()) error {
	return nil
}

func waitUntilAllowedToShare(context.Context, func()) error {
	return nil
}
