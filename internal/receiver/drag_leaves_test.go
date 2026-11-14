package receiver

import (
	"context"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/transport"
)

func deskThatEndsDragsWith(t *testing.T, limit time.Duration, hookOf func(d *desk) func(ctx context.Context)) (*desk, *recordedClipboard, *transport.Conn) {
	t.Helper()
	d := newDesk(t)
	board := &recordedClipboard{}
	d.clipboard = board
	listener := d.sharerListens(t, loopback)
	config := d.config(listener.Addr().String())
	if hookOf != nil {
		config.DragLeaves = hookOf(d)
	}
	d.start(t, func(ctx context.Context) error { return runWith(ctx, config, quickRetries, limit) })
	return d, board, accept(t, listener)
}

func TestTheDragThatLeftIsEndedBeforeTheLeaveThatFollowsIt(t *testing.T) {
	letGo := make(chan struct{})
	started := make(chan struct{})
	d, board, sharerSide := deskThatEndsDragsWith(t, testPatience, func(d *desk) func(context.Context) {
		return func(context.Context) {
			close(started)
			<-letGo
			d.replay.Emit(event.Key(event.ButtonLeft, released), event.Sync())
		}
	})
	send(t, sharerSide, event.Enter(), event.Key(keyB, pressed), event.Key(event.ButtonLeft, pressed), event.Sync())
	d.replay.expect(t, event.Key(keyB, pressed), event.Key(event.ButtonLeft, pressed), event.Sync())

	send(t, sharerSide, event.DragLeaves(), event.Leave())
	select {
	case <-started:
	case <-time.After(testPatience):
		t.Fatal("the hook did not run on frame 23")
	}
	time.Sleep(50 * time.Millisecond)
	if news := board.news(); len(news) != 2 {
		t.Fatalf("the clipboard heard %q while the hook ran, want only the connection and the focus", news)
	}
	d.replay.expect(t, event.Key(keyB, pressed), event.Key(event.ButtonLeft, pressed), event.Sync())
	close(letGo)

	d.replay.expect(t,
		event.Key(keyB, pressed), event.Key(event.ButtonLeft, pressed), event.Sync(),
		event.Key(event.ButtonLeft, released), event.Sync(),
		event.Key(keyB, released), event.Sync(),
	)
	board.expect(t, "connected", "focus arrives", "focus leaves")
}

func TestTheLeaveAfterFrame23ReplaysNoReleaseOfTheLeftButton(t *testing.T) {
	d, board, sharerSide := deskThatEndsDragsWith(t, testPatience, func(*desk) func(context.Context) {
		return func(context.Context) {}
	})

	send(t, sharerSide, event.Enter(), event.Key(event.ButtonLeft, pressed), event.Key(leftShift, pressed), event.Sync(), event.DragLeaves(), event.Leave())

	board.expect(t, "connected", "focus arrives", "focus leaves")
	d.replay.expect(t,
		event.Key(event.ButtonLeft, pressed), event.Key(leftShift, pressed), event.Sync(),
		event.Key(leftShift, released), event.Sync(),
	)
	time.Sleep(50 * time.Millisecond)
	d.replay.expect(t,
		event.Key(event.ButtonLeft, pressed), event.Key(leftShift, pressed), event.Sync(),
		event.Key(leftShift, released), event.Sync(),
	)
}

func TestAHookThatOverrunsItsLimitHoldsTheLeaveNoLonger(t *testing.T) {
	blocked := make(chan struct{})
	t.Cleanup(func() { close(blocked) })
	var bounded context.Context
	reached := make(chan struct{})
	d, board, sharerSide := deskThatEndsDragsWith(t, 50*time.Millisecond, func(*desk) func(context.Context) {
		return func(ctx context.Context) {
			bounded = ctx
			close(reached)
			<-blocked
		}
	})
	send(t, sharerSide, event.Enter(), event.Key(event.ButtonLeft, pressed), event.Sync())
	d.replay.expect(t, event.Key(event.ButtonLeft, pressed), event.Sync())

	asked := time.Now()
	send(t, sharerSide, event.DragLeaves(), event.Leave())

	board.expect(t, "connected", "focus arrives", "focus leaves")
	if waited := time.Since(asked); waited > time.Second {
		t.Errorf("the Leave waited %v for a hook limited to 50 ms", waited)
	}
	<-reached
	if _, hasDeadline := bounded.Deadline(); !hasDeadline {
		t.Error("the hook got a context without a deadline")
	}
	d.replay.expect(t, event.Key(event.ButtonLeft, pressed), event.Sync())
}

func TestWithoutAHookFrame23GoesToTheClipboardAndTheLeaveReleasesTheButton(t *testing.T) {
	d, board, sharerSide := deskThatEndsDragsWith(t, testPatience, nil)

	send(t, sharerSide, event.Enter(), event.Key(event.ButtonLeft, pressed), event.Sync(), event.DragLeaves(), event.Leave())

	board.expect(t, "connected", "focus arrives", heard(event.DragLeaves()), "focus leaves")
	d.replay.expect(t,
		event.Key(event.ButtonLeft, pressed), event.Sync(),
		event.Key(event.ButtonLeft, released), event.Sync(),
	)
}
