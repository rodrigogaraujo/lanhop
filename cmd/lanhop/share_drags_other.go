//go:build !(darwin && cgo)

package main

import (
	"context"
	"io"

	"github.com/rodrigogaraujo/lanhop/internal/sharer"
)

func endTheDragHere(context.Context, sharer.Capture, io.Writer) func() error {
	return func() error { return errNoDragEndsHere }
}
