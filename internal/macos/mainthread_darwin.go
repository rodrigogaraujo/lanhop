//go:build darwin && cgo

package macos

/*
#cgo LDFLAGS: -framework AppKit
#include "drop_darwin.h"
*/
import "C"

import (
	"errors"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

const appKitStartLimit = 5 * time.Second

var (
	ErrNoMainThread      = errors.New("macos: no AppKit run loop can run on the main thread of this program")
	ErrAppKitDidNotStart = errors.New("macos: AppKit did not start on the main thread")
)

var (
	mainThreadServed atomic.Bool
	appKitWanted     = make(chan struct{})
	askForAppKit     sync.Once
	appKitReady      = newLatch()
)

func ServeMainThread(program func()) {
	runtime.LockOSThread()
	if bool(C.lanhopOnMainThread()) {
		mainThreadServed.Store(true)
	}
	go func() {
		program()
		os.Exit(0)
	}()
	<-appKitWanted
	C.lanhopAppRun()
	select {}
}

func startAppKit() error {
	if !mainThreadServed.Load() {
		return ErrNoMainThread
	}
	askForAppKit.Do(func() { close(appKitWanted) })
	timer := time.NewTimer(appKitStartLimit)
	defer timer.Stop()
	select {
	case <-appKitReady.done:
		return nil
	case <-timer.C:
		return ErrAppKitDidNotStart
	}
}

//export lanhopAppKitReady
func lanhopAppKitReady() {
	appKitReady.fire()
}
