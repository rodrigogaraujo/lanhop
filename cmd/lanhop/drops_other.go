//go:build !linux && !(darwin && cgo)

package main

import (
	"context"
	"errors"
	"io"

	"github.com/rodrigogaraujo/lanhop/internal/clipshare"
)

var errSourceDragStays = errors.New("lanhop cannot end a file drag on this system")

func openDropsFor(bool, *dropReplay, io.Writer) (clipshare.Drops, func(), error) {
	return nil, func() {}, nil
}

func watchLocalDrags(context.Context) (<-chan localDrag, func(), error) {
	return nil, func() {}, nil
}

func resetLocalDrags() {}

func endSourceDragHere() error {
	return errSourceDragStays
}

func dragLeavesHandler(*dropReplay, io.Writer, func()) func(context.Context) {
	return nil
}
