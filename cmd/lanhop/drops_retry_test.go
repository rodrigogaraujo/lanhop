package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/clipshare"
)

type openings struct {
	mutex    sync.Mutex
	failures int
	tries    int
	released int
	drops    clipshare.Drops
}

func (o *openings) open() (clipshare.Drops, func(), error) {
	o.mutex.Lock()
	defer o.mutex.Unlock()
	o.tries++
	if o.tries <= o.failures {
		return nil, func() {}, errors.New("no X server yet")
	}
	return o.drops, func() {
		o.mutex.Lock()
		defer o.mutex.Unlock()
		o.released++
	}, nil
}

func (o *openings) counts() (tries, released int) {
	o.mutex.Lock()
	defer o.mutex.Unlock()
	return o.tries, o.released
}

type startedDrops struct {
	mutex   sync.Mutex
	started int
}

func (d *startedDrops) StartDrop(context.Context, []clipshare.DroppedFile) (clipshare.DropSession, error) {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	d.started++
	return nil, nil
}

func TestDropsThatCannotOpenAtStartAreTriedAgainUntilTheyOpen(t *testing.T) {
	underneath := &startedDrops{}
	tries := &openings{failures: 2, drops: underneath}
	said := &screen{}

	drops, stop := openDropsUntilTheyOpen(context.Background(), tries.open, 5*time.Millisecond, said)

	if dropsOpenNow(drops) != nil {
		t.Fatal("drops that failed to open were taken as open")
	}
	if _, err := drops.StartDrop(context.Background(), nil); !errors.Is(err, errDropsNotOpenYet) {
		t.Errorf("a drop started before the drops opened gave %v, want %v", err, errDropsNotOpenYet)
	}
	waitUntil(t, "the drops to open", func() bool { return dropsOpenNow(drops) != nil })
	if _, err := drops.StartDrop(context.Background(), nil); err != nil {
		t.Errorf("a drop started once the drops opened gave %v", err)
	}
	if underneath.started != 1 {
		t.Errorf("the open drops started %d drops, want 1", underneath.started)
	}
	if strings.Count(said.String(), "lanhop tries again") != 1 || strings.Count(said.String(), "can now drop") != 1 {
		t.Errorf("the log says %q, want the failure and the opening once each", said.String())
	}

	stop()

	if tries, released := tries.counts(); tries != 3 || released != 1 {
		t.Errorf("the drops were tried %d times and released %d times, want 3 and 1", tries, released)
	}
	if dropsOpenNow(drops) != nil {
		t.Error("the drops are still open after stopping")
	}
}

func TestDropsThatOpenAtOnceAreUsedAsTheyAre(t *testing.T) {
	underneath := &startedDrops{}
	tries := &openings{drops: underneath}

	drops, stop := openDropsUntilTheyOpen(context.Background(), tries.open, time.Hour, &screen{})
	stop()

	if drops != clipshare.Drops(underneath) {
		t.Errorf("the drops opened at once were wrapped: %T", drops)
	}
	if _, released := tries.counts(); released != 1 {
		t.Errorf("the drops were released %d times, want once", released)
	}
}

func TestASystemWithoutDropsIsNotTriedAgain(t *testing.T) {
	tries := &openings{}

	drops, stop := openDropsUntilTheyOpen(context.Background(), tries.open, time.Millisecond, &screen{})
	time.Sleep(20 * time.Millisecond)
	stop()

	if drops != nil {
		t.Errorf("a system without drops got %v", drops)
	}
	if tried, _ := tries.counts(); tried != 1 {
		t.Errorf("the drops were tried %d times, want once", tried)
	}
}
