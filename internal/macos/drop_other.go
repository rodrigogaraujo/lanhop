//go:build !(darwin && cgo)

package macos

import (
	"context"
	"errors"
)

var ErrDropNotBuilt = errors.New("macos: dropping a dragged file needs lanhop built with cgo on macOS")

type Drop struct{}

func StartDrop(context.Context, []PromisedFile, WriteFile) (*Drop, error) {
	return nil, ErrDropNotBuilt
}

func (*Drop) Ended() <-chan struct{} {
	return nil
}

func (*Drop) Operation() Operation {
	return OperationNone
}

func (*Drop) Kept() <-chan struct{} {
	return nil
}

func (*Drop) Close() {}
