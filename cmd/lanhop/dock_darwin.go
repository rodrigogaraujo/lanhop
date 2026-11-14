//go:build darwin && cgo

package main

import (
	"github.com/rodrigogaraujo/lanhop/internal/edge"
	"github.com/rodrigogaraujo/lanhop/internal/macos"
)

func readDock() edge.Side {
	return dockOfTheMacDock(macos.DockOrientation())
}
