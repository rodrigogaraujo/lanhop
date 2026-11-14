//go:build !linux && !(darwin && cgo)

package main

import "github.com/rodrigogaraujo/lanhop/internal/edge"

func readDock() edge.Side {
	return 0
}
