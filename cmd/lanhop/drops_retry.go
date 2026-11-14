package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/clipshare"
)

const dropsRetryInterval = 30 * time.Second

var errDropsNotOpenYet = errors.New("lanhop has not opened the drop on this machine yet")

type openDropsOnce func() (clipshare.Drops, func(), error)

type dropsWhenOpen struct {
	mutex   sync.Mutex
	drops   clipshare.Drops
	release func()
}

func (d *dropsWhenOpen) StartDrop(ctx context.Context, files []clipshare.DroppedFile) (clipshare.DropSession, error) {
	drops := d.current()
	if drops == nil {
		return nil, errDropsNotOpenYet
	}
	return drops.StartDrop(ctx, files)
}

func (d *dropsWhenOpen) current() clipshare.Drops {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	return d.drops
}

func (d *dropsWhenOpen) opened(drops clipshare.Drops, release func()) {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	d.drops, d.release = drops, release
}

func (d *dropsWhenOpen) shut() {
	d.mutex.Lock()
	release := d.release
	d.drops, d.release = nil, nil
	d.mutex.Unlock()
	if release != nil {
		release()
	}
}

func dropsOpenNow(drops clipshare.Drops) clipshare.Drops {
	if later, waiting := drops.(*dropsWhenOpen); waiting {
		return later.current()
	}
	return drops
}

func openDropsUntilTheyOpen(done context.Context, open openDropsOnce, every time.Duration, log io.Writer) (clipshare.Drops, func()) {
	drops, release, err := open()
	if err == nil {
		if release == nil {
			release = func() {}
		}
		return drops, release
	}
	if release != nil {
		release()
	}
	fmt.Fprintf(log, "A file dragged from another machine cannot be dropped on this one, and one dragged here cannot leave, for now: %s. lanhop tries again every %v.\n", forTheLog(err), every)
	later := &dropsWhenOpen{}
	trying, stopTrying := context.WithCancel(done)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		keepTryingToOpen(trying, open, every, later, log)
	}()
	return later, func() {
		stopTrying()
		<-stopped
		later.shut()
	}
}

func keepTryingToOpen(done context.Context, open openDropsOnce, every time.Duration, later *dropsWhenOpen, log io.Writer) {
	retry := time.NewTicker(every)
	defer retry.Stop()
	for {
		select {
		case <-done.Done():
			return
		case <-retry.C:
		}
		drops, release, err := open()
		if err != nil {
			if release != nil {
				release()
			}
			continue
		}
		if release == nil {
			release = func() {}
		}
		later.opened(drops, release)
		fmt.Fprintln(log, "lanhop can now drop a file dragged from another machine on this one, and let one dragged here leave.")
		return
	}
}
