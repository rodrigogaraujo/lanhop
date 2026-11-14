//go:build darwin && cgo

package main

import (
	"runtime"

	"github.com/rodrigogaraujo/lanhop/internal/macos"
)

func init() {
	runtime.LockOSThread()
}

func runOffTheMainThread(program func() int) {
	macos.ServeMainThread(program)
}
