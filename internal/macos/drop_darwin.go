//go:build darwin && cgo

package macos

/*
#cgo LDFLAGS: -framework AppKit -framework ApplicationServices -framework UniformTypeIdentifiers
#include <stdlib.h>
#include "drop_darwin.h"
*/
import "C"

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"
	"unsafe"

	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
)

const (
	dropEndLimit     = 2 * time.Second
	dropReleaseLimit = time.Second
	dropReleasePoll  = 20 * time.Millisecond
)

var (
	_ [0]struct{} = [windowReady - C.LANHOP_DROP_WINDOW_READY]struct{}{}
	_ [0]struct{} = [windowNotShown - C.LANHOP_DROP_WINDOW_NOT_SHOWN]struct{}{}
	_ [0]struct{} = [windowCovered - C.LANHOP_DROP_WINDOW_COVERED]struct{}{}
	_ [0]struct{} = [windowMissesTheCursor - C.LANHOP_DROP_WINDOW_MISSES_THE_CURSOR]struct{}{}
)

var (
	ErrDropRunning    = errors.New("macos: a drag session of lanhop is still running")
	ErrCannotPost     = errors.New("macos: this program is not allowed to post the mouse events that start a drag")
	ErrNoDropWindow   = errors.New("macos: AppKit gave lanhop no window to start the drag from")
	ErrMainThreadLate = errors.New("macos: the main thread did not answer in time")
)

type Drop struct {
	id           uint64
	window       dropWindow
	releaseLimit time.Duration
	write        WriteFile
	writing      context.Context
	stopWrites   context.CancelFunc
	promises     *promises
	button       *heldDown
	downSeen     *latch
	began        *latch
	ended        *latch
	state        sync.Mutex
	operation    Operation
	downAtEnd    bool
	closing      sync.Once
}

var drops = struct {
	sync.Mutex
	last    uint64
	byID    map[uint64]*Drop
	session uint64
}{byID: map[uint64]*Drop{}}

type nativeWindow struct {
	pointer unsafe.Pointer
}

func (w nativeWindow) state() windowState {
	return windowState(C.lanhopDropWindowState(w.pointer))
}

func (w nativeWindow) refuseDrops() error {
	if !bool(C.lanhopDropRefuseDrops(w.pointer)) {
		return ErrMainThreadLate
	}
	return nil
}

func (w nativeWindow) takeTheMouseAt(at Point) (bool, error) {
	switch C.lanhopDropTakeTheMouseAt(w.pointer, C.double(at.X), C.double(at.Y)) {
	case C.LANHOP_DROP_TAKES_THE_MOUSE:
		return true, nil
	case C.LANHOP_DROP_MISSES_THE_MOUSE:
		return false, nil
	}
	return false, ErrMainThreadLate
}

func (w nativeWindow) close() error {
	if !bool(C.lanhopDropClose(w.pointer)) {
		return ErrMainThreadLate
	}
	return nil
}

func openDropWindow(id uint64, files []PromisedFile, home Point) (dropWindow, error) {
	names := joinTerminated(promisedNames(files))
	kinds := joinTerminated(promisedKinds(files))
	cNames := C.CString(names)
	defer C.free(unsafe.Pointer(cNames))
	cKinds := C.CString(kinds)
	defer C.free(unsafe.Pointer(cKinds))
	pointer := C.lanhopDropOpen(C.uint64_t(id), cNames, C.int64_t(len(names)), cKinds, C.int64_t(len(kinds)), C.int(len(files)),
		C.double(home.X), C.double(home.Y), C.double(dropWindowSide))
	if pointer == nil {
		return nil, ErrNoDropWindow
	}
	return nativeWindow{pointer: pointer}, nil
}

func StartDrop(ctx context.Context, files []PromisedFile, write WriteFile) (*Drop, error) {
	if err := checkPromisedFiles(files, clipboard.FilesAndFoldersLimit); err != nil {
		return nil, err
	}
	if write == nil {
		return nil, ErrNoWriter
	}
	if !mainThreadIsServed() {
		return nil, ErrNoMainThread
	}
	if !CanPost() {
		return nil, ErrCannotPost
	}
	if err := startAppKit(); err != nil {
		return nil, err
	}
	screen := Quartz{}
	plan := planDrag(screen.Cursor(), screen.Displays())
	drop, err := registerDrop(len(files), write, newHeldDown(screen, plan.start))
	if err != nil {
		return nil, err
	}
	if err := drop.start(ctx, files, plan); err != nil {
		drop.Close()
		return nil, err
	}
	return drop, nil
}

func registerDrop(count int, write WriteFile, button *heldDown) (*Drop, error) {
	drops.Lock()
	defer drops.Unlock()
	if drops.session != 0 {
		return nil, ErrDropRunning
	}
	drops.last++
	writing, stopWrites := context.WithCancel(context.Background())
	drop := &Drop{
		id:           drops.last,
		releaseLimit: dropReleaseLimit,
		write:        write,
		writing:      writing,
		stopWrites:   stopWrites,
		promises:     newPromises(count),
		button:       button,
		downSeen:     newLatch(),
		began:        newLatch(),
		ended:        newLatch(),
	}
	drops.byID[drop.id] = drop
	drops.session = drop.id
	return drop, nil
}

func forgetDrop(drop *Drop) {
	drops.Lock()
	defer drops.Unlock()
	delete(drops.byID, drop.id)
}

func sessionOver(id uint64) {
	drops.Lock()
	defer drops.Unlock()
	if drops.session == id {
		drops.session = 0
	}
}

func dropWithID(id uint64) *Drop {
	drops.Lock()
	defer drops.Unlock()
	return drops.byID[id]
}

func (d *Drop) start(ctx context.Context, files []PromisedFile, plan dragPlan) error {
	window, err := openDropWindow(d.id, files, plan.start)
	if err != nil {
		return err
	}
	d.window = window
	return dragStart{
		plan:     plan,
		button:   d.button,
		window:   window.state,
		downSeen: d.downSeen.done,
		began:    d.began.done,
		timing:   timingOfTheProbe,
	}.run(ctx)
}

func (d *Drop) Ended() <-chan struct{} {
	return d.ended.done
}

func (d *Drop) Operation() Operation {
	d.state.Lock()
	defer d.state.Unlock()
	return d.operation
}

func (d *Drop) Kept() <-chan struct{} {
	return d.promises.kept
}

func (d *Drop) Close() {
	d.closing.Do(func() {
		d.stopWrites()
		d.promises.close()
		d.endTheSession()
		forgetDrop(d)
		if d.window == nil {
			sessionOver(d.id)
		} else if err := d.window.close(); err != nil {
			log.Printf("macos: closing the window of the drag: %v; the next drop waits until AppKit lets the session go", err)
		}
	})
}

func (d *Drop) endTheSession() {
	select {
	case <-d.ended.done:
		if d.buttonDownAtEnd() {
			d.button.releaseHere()
		}
		return
	default:
	}
	if !d.button.pending() {
		return
	}
	if d.window == nil {
		d.button.releaseHere()
		log.Printf("macos: the drag had no window of lanhop, so its button was released where the cursor is")
	} else {
		d.releaseOverTheWindow()
	}
	timer := time.NewTimer(dropEndLimit)
	defer timer.Stop()
	select {
	case <-d.ended.done:
	case <-timer.C:
	}
}

func (d *Drop) releaseOverTheWindow() {
	if err := d.window.refuseDrops(); err != nil {
		log.Printf("macos: refusing drops before the release: %v", err)
	}
	deadline := time.Now().Add(d.releaseLimit)
	for {
		at := d.button.screen.Cursor()
		taken, err := d.window.takeTheMouseAt(at)
		if taken {
			d.button.releaseAt(at)
			return
		}
		if !time.Now().Before(deadline) {
			reason := "another window takes the mouse there"
			if err != nil {
				reason = err.Error()
			}
			log.Printf("macos: the window of lanhop did not take the mouse under the cursor (%s), so the button of the drag was released there with drops refused", reason)
			d.button.releaseAt(at)
			return
		}
		time.Sleep(dropReleasePoll)
	}
}

func (d *Drop) buttonDownAtEnd() bool {
	d.state.Lock()
	defer d.state.Unlock()
	return d.downAtEnd
}

func (d *Drop) sessionEnded(operation Operation, buttonDown bool) {
	d.state.Lock()
	d.operation = operation
	d.downAtEnd = buttonDown
	d.state.Unlock()
	if !buttonDown {
		d.button.handOver()
	}
	d.ended.fire()
}

func endedSession(id uint64, operation Operation, buttonDown bool) {
	sessionOver(id)
	if drop := dropWithID(id); drop != nil {
		drop.sessionEnded(operation, buttonDown)
	}
}

func writePromisedFile(id uint64, file int, destination string) error {
	drop := dropWithID(id)
	if drop == nil {
		return ErrDropClosed
	}
	return drop.promises.keep(drop.writing, file, destination, drop.write)
}

//export lanhopDropMouseDown
func lanhopDropMouseDown(id C.uint64_t) {
	if drop := dropWithID(uint64(id)); drop != nil {
		drop.downSeen.fire()
	}
}

//export lanhopDropBegan
func lanhopDropBegan(id C.uint64_t) {
	if drop := dropWithID(uint64(id)); drop != nil {
		drop.began.fire()
	}
}

//export lanhopDropEnded
func lanhopDropEnded(id C.uint64_t, operation C.uint64_t, buttonDown C.bool) {
	endedSession(uint64(id), Operation(operation), bool(buttonDown))
}

//export lanhopDropSessionOver
func lanhopDropSessionOver(id C.uint64_t) {
	sessionOver(uint64(id))
}

//export lanhopDropWrite
func lanhopDropWrite(id C.uint64_t, file C.int, destination *C.char) *C.char {
	if err := writePromisedFile(uint64(id), int(file), C.GoString(destination)); err != nil {
		return C.CString(err.Error())
	}
	return nil
}
