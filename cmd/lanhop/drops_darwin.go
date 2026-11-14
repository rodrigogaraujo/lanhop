//go:build darwin && cgo

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"syscall"

	"github.com/rodrigogaraujo/lanhop/internal/clipshare"
	"github.com/rodrigogaraujo/lanhop/internal/macos"
)

func openDropsFor(central bool, hand *dropReplay, log io.Writer) (clipshare.Drops, func(), error) {
	start := startAppKitDrop
	if central {
		start = startAppKitDropUnderTheCursor
	}
	drops := newPromiseDrops(start, hand, carryTheQuarantine, log)
	return drops, drops.close, nil
}

type appKitDrop struct {
	*macos.Drop
}

func (d appKitDrop) Accepted() bool {
	return d.Operation().Accepted()
}

func startAppKitDrop(ctx context.Context, files []clipshare.DroppedFile, write writePromise) (promisedDrop, error) {
	return startAppKitDropWith(ctx, macos.StartDrop, files, write)
}

func startAppKitDropUnderTheCursor(ctx context.Context, files []clipshare.DroppedFile, write writePromise) (promisedDrop, error) {
	return startAppKitDropWith(ctx, macos.StartDropUnderTheCursor, files, write)
}

func startAppKitDropWith(ctx context.Context, start func(context.Context, []macos.PromisedFile, macos.WriteFile) (*macos.Drop, error), files []clipshare.DroppedFile, write writePromise) (promisedDrop, error) {
	promised := make([]macos.PromisedFile, len(files))
	for i, file := range files {
		promised[i] = macos.PromisedFile{Name: file.Name, Folder: file.Folder}
	}
	drop, err := start(ctx, promised, macos.WriteFile(write))
	if err != nil {
		return nil, err
	}
	return appKitDrop{drop}, nil
}

func carryTheQuarantine(source, destination string) error {
	mark, err := macos.Quarantine(source)
	switch {
	case errors.Is(err, syscall.ENOATTR):
		return nil
	case err != nil:
		return err
	case mark == "":
		return nil
	}
	return macos.SetQuarantine(destination, mark)
}

var localDragWatches = struct {
	sync.Mutex
	running map[*macos.DragWatch]struct{}
}{running: make(map[*macos.DragWatch]struct{})}

func watchLocalDrags(ctx context.Context) (<-chan localDrag, func(), error) {
	watch, err := macos.WatchDrags(macos.DragPollInterval)
	if err != nil {
		return nil, func() {}, err
	}
	localDragWatches.Lock()
	localDragWatches.running[watch] = struct{}{}
	localDragWatches.Unlock()
	drags, stop := forwardDrags(ctx, watch.Events(), func(seen macos.DragEvent) localDrag {
		return localDrag{Started: seen.Kind == macos.DragStarted, Paths: seen.Paths}
	}, func() {
		localDragWatches.Lock()
		delete(localDragWatches.running, watch)
		localDragWatches.Unlock()
		watch.Close()
	})
	return drags, stop, nil
}

func resetLocalDrags() {
	localDragWatches.Lock()
	defer localDragWatches.Unlock()
	for watch := range localDragWatches.running {
		watch.Reset()
	}
}

func endSourceDragHere() error {
	return macos.EndSourceDrag()
}

func dragLeavesHandler(hand *dropReplay, log io.Writer, stayed func(), described func() bool) func(context.Context) {
	return func(ctx context.Context) {
		end := func(under io.Closer) error { return endTheReplayedSourceDrag(under, described(), log) }
		endTheSourceDragUnderAShield(ctx, shieldTheCursor, end, hand, log, stayed)
	}
}

func endTheReplayedSourceDrag(under io.Closer, described bool, log io.Writer) error {
	if shield, shielded := under.(*macos.Shield); shielded {
		return macos.EndReplayedSourceDragUnder(shield.At(), described)
	}
	fmt.Fprintln(log, "With no window of lanhop under the cursor, lanhop posts no release at the HID tap, so this Mac may read the left button as held until the next click.")
	return macos.EndReplayedSourceDragUnshielded(described)
}

func shieldTheCursor(ctx context.Context) (io.Closer, error) {
	shield, err := macos.ShieldCursor(ctx)
	if err != nil {
		return nil, err
	}
	return shield, nil
}
