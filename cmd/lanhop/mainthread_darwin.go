//go:build darwin && cgo

package main

import (
	"os"
	"runtime"

	"github.com/rodrigogaraujo/lanhop/internal/macos"
)

func init() {
	runtime.LockOSThread()
}

func runOffTheMainThread(program func() int) {
	macos.ServeMainThread(func() {
		os.Exit(program())
	})
}
