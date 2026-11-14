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
	ErrDropRunning  = errors.New("macos: a drag session of lanhop is still running")
	ErrCannotPost   = errors.New("macos: this program is not allowed to post the mouse events that start a drag")
	ErrNoDropWindow = errors.New("macos: AppKit gave lanhop no window to start the drag from")
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

func openShieldWindow(at Point) (dropWindow, error) {
	pointer := C.lanhopShieldOpen(C.double(at.X), C.double(at.Y), C.double(dropWindowSide))
	if pointer == nil {
		return nil, ErrNoDropWindow
	}
	return nativeWindow{pointer: pointer}, nil
}

func ShieldCursor(ctx context.Context) (*Shield, error) {
	return shieldUnder(ctx, Quartz{}.Cursor)
}

func shieldUnder(ctx context.Context, cursor func() Point) (*Shield, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !mainThreadIsServed() {
		return nil, ErrNoMainThread
	}
	if err := startAppKit(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	window, err := openShieldWindow(cursor())
	if err != nil {
		return nil, err
	}
	shield := newShield(window, shieldLifetime)
	if err := shield.raise(ctx, cursor, shieldLimit, shieldPoll); err != nil {
		return nil, err
	}
	return shield, nil
}

func appIsActive() bool {
	return bool(C.lanhopAppIsActive())
}

func StartDrop(ctx context.Context, files []PromisedFile, write WriteFile) (*Drop, error) {
	if err := readyToDrop(files, write); err != nil {
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

func StartDropUnderTheCursor(ctx context.Context, files []PromisedFile, write WriteFile) (*Drop, error) {
	if err := readyToDrop(files, write); err != nil {
		return nil, err
	}
	screen := Quartz{}
	drop, err := registerDrop(len(files), write, newHeldDownByThePerson(screen))
	if err != nil {
		return nil, err
	}
	if err := drop.startUnderTheCursor(ctx, files, screen); err != nil {
		drop.Close()
		return nil, err
	}
	return drop, nil
}

func readyToDrop(files []PromisedFile, write WriteFile) error {
	if err := checkPromisedFiles(files, clipboard.FilesAndFoldersLimit); err != nil {
		return err
	}
	if write == nil {
		return ErrNoWriter
	}
	if !mainThreadIsServed() {
		return ErrNoMainThread
	}
	if !CanPost() {
		return ErrCannotPost
	}
	return startAppKit()
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

type openedWindow struct {
	window dropWindow
	err    error
}

func (d *Drop) startUnderTheCursor(ctx context.Context, files []PromisedFile, screen Quartz) error {
	bounded, stop := context.WithTimeout(ctx, startUnderTheCursorLimit)
	defer stop()
	opening := make(chan openedWindow, 1)
	go func() {
		window, err := openDropWindow(d.id, files, screen.Cursor())
		opening <- openedWindow{window, err}
	}()
	var window dropWindow
	select {
	case opened := <-opening:
		if opened.err != nil {
			return opened.err
		}
		window = opened.window
	case <-bounded.Done():
		go closeWhenOpened(opening)
		return ErrDropWindowNotShown
	}
	d.window = window
	return startUnderTheCursor{
		button:   d.button,
		window:   window,
		cursor:   screen.Cursor,
		plan:     func(at Point) dragPlan { return planDrag(at, screen.Displays()) },
		downSeen: d.downSeen.done,
		began:    d.began.done,
		timing:   timingOfTheProbe,
		limit:    startUnderTheCursorLimit,
	}.run(bounded)
}

func closeWhenOpened(opening <-chan openedWindow) {
	if opened := <-opening; opened.err == nil {
		opened.window.close()
	}
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
	sessionEnd{
		button:       d.button,
		window:       d.window,
		ended:        d.ended.done,
		downAtEnd:    d.buttonDownAtEnd,
		releaseLimit: d.releaseLimit,
		releasePoll:  dropReleasePoll,
		endLimit:     dropEndLimit,
		logf:         log.Printf,
	}.run()
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
