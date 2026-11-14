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
	"sync"
	"time"
	"unsafe"

	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
)

const dropEndLimit = 2 * time.Second

var (
	_ [0]struct{} = [windowReady - C.LANHOP_DROP_WINDOW_READY]struct{}{}
	_ [0]struct{} = [windowNotShown - C.LANHOP_DROP_WINDOW_NOT_SHOWN]struct{}{}
	_ [0]struct{} = [windowCovered - C.LANHOP_DROP_WINDOW_COVERED]struct{}{}
)

var (
	ErrDropRunning  = errors.New("macos: a drag session of lanhop is already running")
	ErrCannotPost   = errors.New("macos: this program is not allowed to post the mouse events that start a drag")
	ErrNoDropWindow = errors.New("macos: AppKit gave lanhop no window to start the drag from")
)

type Drop struct {
	id         uint64
	native     unsafe.Pointer
	write      WriteFile
	writing    context.Context
	stopWrites context.CancelFunc
	promises   *promises
	button     *heldDown
	downSeen   *latch
	began      *latch
	ended      *latch
	state      sync.Mutex
	operation  Operation
	downAtEnd  bool
	closing    sync.Once
}

var drops = struct {
	sync.Mutex
	last    uint64
	byID    map[uint64]*Drop
	session *Drop
}{byID: map[uint64]*Drop{}}

func StartDrop(ctx context.Context, files []PromisedFile, write WriteFile) (*Drop, error) {
	if err := checkPromisedFiles(files, clipboard.FilesAndFoldersLimit); err != nil {
		return nil, err
	}
	if write == nil {
		return nil, ErrNoWriter
	}
	if !mainThreadServed.Load() {
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
	if drops.session != nil {
		return nil, ErrDropRunning
	}
	drops.last++
	writing, stopWrites := context.WithCancel(context.Background())
	drop := &Drop{
		id:         drops.last,
		write:      write,
		writing:    writing,
		stopWrites: stopWrites,
		promises:   newPromises(count),
		button:     button,
		downSeen:   newLatch(),
		began:      newLatch(),
		ended:      newLatch(),
	}
	drops.byID[drop.id] = drop
	drops.session = drop
	return drop, nil
}

func forgetDrop(drop *Drop) {
	drops.Lock()
	defer drops.Unlock()
	delete(drops.byID, drop.id)
	if drops.session == drop {
		drops.session = nil
	}
}

func dropWithID(id uint64) *Drop {
	drops.Lock()
	defer drops.Unlock()
	return drops.byID[id]
}

func (d *Drop) start(ctx context.Context, files []PromisedFile, plan dragPlan) error {
	names := joinTerminated(promisedNames(files))
	kinds := joinTerminated(promisedKinds(files))
	cNames := C.CString(names)
	defer C.free(unsafe.Pointer(cNames))
	cKinds := C.CString(kinds)
	defer C.free(unsafe.Pointer(cKinds))
	d.native = C.lanhopDropOpen(C.uint64_t(d.id), cNames, C.int64_t(len(names)), cKinds, C.int64_t(len(kinds)), C.int(len(files)),
		C.double(plan.start.X), C.double(plan.start.Y), C.double(dropWindowSide))
	if d.native == nil {
		return ErrNoDropWindow
	}
	return dragStart{
		plan:     plan,
		button:   d.button,
		window:   d.windowState,
		downSeen: d.downSeen.done,
		began:    d.began.done,
		timing:   timingOfTheProbe,
	}.run(ctx)
}

func (d *Drop) windowState() windowState {
	return windowState(C.lanhopDropWindowState(d.native))
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
		if d.native != nil {
			C.lanhopDropClose(d.native)
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
	if !d.button.releaseAtHome() {
		return
	}
	timer := time.NewTimer(dropEndLimit)
	defer timer.Stop()
	select {
	case <-d.ended.done:
	case <-timer.C:
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
	drops.Lock()
	if drops.session == d {
		drops.session = nil
	}
	drops.Unlock()
	d.ended.fire()
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
	if drop := dropWithID(uint64(id)); drop != nil {
		drop.sessionEnded(Operation(operation), bool(buttonDown))
	}
}

//export lanhopDropWrite
func lanhopDropWrite(id C.uint64_t, file C.int, destination *C.char) *C.char {
	if err := writePromisedFile(uint64(id), int(file), C.GoString(destination)); err != nil {
		return C.CString(err.Error())
	}
	return nil
}
