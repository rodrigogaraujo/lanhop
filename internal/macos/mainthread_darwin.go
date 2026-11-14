//go:build darwin && cgo

package macos

/*
#cgo LDFLAGS: -framework AppKit -framework ApplicationServices
#include "drop_darwin.h"
*/
import "C"

import (
	"errors"
	"log"
	"os"
	"runtime"
	"sync"
	"time"
)

const (
	appKitStartLimit    = 5 * time.Second
	mainThreadExitLimit = 5 * time.Second
)

var (
	ErrNoMainThread      = errors.New("macos: no AppKit run loop can run on the main thread of this program")
	ErrNoGUISession      = errors.New("macos: this program runs outside a session with a screen, so AppKit cannot start")
	ErrAppKitDidNotStart = errors.New("macos: AppKit did not start on the main thread")
	errExiting           = errors.New("macos: the program is exiting, so AppKit does not start")
)

var mainThread struct {
	sync.Mutex
	served  bool
	appKit  bool
	exiting bool
}

var (
	appKitWanted = make(chan struct{})
	exitCodes    = make(chan int, 1)
	appKitReady  = newLatch()
)

func ServeMainThread(program func() int) {
	runtime.LockOSThread()
	mainThread.Lock()
	mainThread.served = bool(C.lanhopOnMainThread())
	mainThread.Unlock()
	go func() {
		exitOnTheMainThread(program())
	}()
	select {
	case code := <-exitCodes:
		os.Exit(code)
	case <-appKitWanted:
	}
	C.lanhopAppRun()
	os.Exit(<-exitCodes)
}

func exitOnTheMainThread(code int) {
	mainThread.Lock()
	mainThread.exiting = true
	appKit := mainThread.appKit
	mainThread.Unlock()
	exitCodes <- code
	if appKit {
		C.lanhopExitOnMainThread(C.int(code))
		time.Sleep(mainThreadExitLimit)
		log.Printf("macos: the main thread did not exit within %v, so lanhop exits from another thread", mainThreadExitLimit)
		os.Exit(code)
	}
	select {}
}

func askAppKitToQuit() {
	C.lanhopAppQuit()
}

func mainThreadIsServed() bool {
	mainThread.Lock()
	defer mainThread.Unlock()
	return mainThread.served
}

func startAppKit() error {
	if err := askForAppKit(); err != nil {
		return err
	}
	timer := time.NewTimer(appKitStartLimit)
	defer timer.Stop()
	select {
	case <-appKitReady.done:
		return nil
	case <-timer.C:
		return ErrAppKitDidNotStart
	}
}

func askForAppKit() error {
	mainThread.Lock()
	defer mainThread.Unlock()
	switch {
	case !mainThread.served:
		return ErrNoMainThread
	case mainThread.exiting:
		return errExiting
	case mainThread.appKit:
		return nil
	case !bool(C.lanhopHasGUISession()):
		return ErrNoGUISession
	}
	mainThread.appKit = true
	close(appKitWanted)
	return nil
}

//export lanhopAppKitReady
func lanhopAppKitReady() {
	appKitReady.fire()
}

//export lanhopExitNow
func lanhopExitNow(code C.int) {
	os.Exit(int(code))
}
