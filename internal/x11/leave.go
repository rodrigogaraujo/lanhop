//go:build linux

package x11

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const (
	edgeAcceptLimit  = time.Second
	edgeStripLength  = 256
	edgeStripOutward = 2
	edgeStripInward  = 1
)

const (
	edgeLeft = iota
	edgeRight
	edgeTop
	edgeBottom
)

var (
	ErrDragStayed   = errors.New("x11: the drag did not reach the window of lanhop at the edge, so it was not released there")
	ErrEdgeDropLate = errors.New("x11: the window of lanhop at the edge got no drop in time, and waits for it a little longer")
)

type DragHand interface {
	Nudge(dx, dy int) error
	Release() error
	Escape() error
}

type edgeCatcher interface {
	Accepted() <-chan struct{}
	Over() bool
	Dropped() <-chan EdgeDrop
	Close() error
}

type stripAtTheEdge struct {
	strip  Rect
	dx, dy int
}

func (d *Drags) EndDragAtEdge(ctx context.Context, hand DragHand) ([]string, error) {
	at, err := d.queryPointer()
	var screen windowArea
	if err == nil {
		screen, err = d.areaOf(d.c.root)
	}
	if err != nil {
		return nil, stayWith(hand, fmt.Errorf("%w: %w", ErrDragStayed, err))
	}
	edge := edgeStripAt(at.x, at.y, screen.width, screen.height)
	return endAtEdge(ctx, func() (edgeCatcher, error) { return d.CatchAtEdge(edge.strip) }, hand, edge, edgeAcceptLimit)
}

func edgeStripAt(x, y, width, height int) stripAtTheEdge {
	distances := [...]int{edgeLeft: x, edgeRight: width - 1 - x, edgeTop: y, edgeBottom: height - 1 - y}
	nearest := edgeLeft
	for side, distance := range distances {
		if distance < distances[nearest] {
			nearest = side
		}
	}
	alongX := spanWithin(x-edgeStripLength/2, x+edgeStripLength/2, width)
	alongY := spanWithin(y-edgeStripLength/2, y+edgeStripLength/2, height)
	switch nearest {
	case edgeLeft:
		return stripAtTheEdge{strip: rectOf(spanWithin(x-edgeStripOutward, x+edgeStripInward+1, width), alongY), dx: 1}
	case edgeRight:
		return stripAtTheEdge{strip: rectOf(spanWithin(x-edgeStripInward, x+edgeStripOutward+1, width), alongY), dx: -1}
	case edgeTop:
		return stripAtTheEdge{strip: rectOf(alongX, spanWithin(y-edgeStripOutward, y+edgeStripInward+1, height)), dy: 1}
	}
	return stripAtTheEdge{strip: rectOf(alongX, spanWithin(y-edgeStripInward, y+edgeStripOutward+1, height)), dy: -1}
}

type span struct {
	from, to int
}

func spanWithin(from, to, size int) span {
	return span{from: max(from, 0), to: min(to, size)}
}

func rectOf(horizontal, vertical span) Rect {
	return Rect{X: horizontal.from, Y: vertical.from, Width: horizontal.to - horizontal.from, Height: vertical.to - vertical.from}
}

func endAtEdge(ctx context.Context, catchAt func() (edgeCatcher, error), hand DragHand, edge stripAtTheEdge, acceptLimit time.Duration) ([]string, error) {
	catch, err := catchAt()
	if err != nil {
		return nil, stayWith(hand, fmt.Errorf("%w: %w", ErrDragStayed, err))
	}
	err = hand.Nudge(edge.dx, edge.dy)
	if err == nil {
		err = hand.Nudge(-edge.dx, -edge.dy)
	}
	if err == nil && !acceptedInTime(ctx, catch, acceptLimit) {
		err = errors.New("the source never moved over it")
	}
	if err == nil && !catch.Over() {
		err = errors.New("the source left it")
	}
	if err != nil {
		stayed := stayWith(hand, fmt.Errorf("%w: %w", ErrDragStayed, err))
		catch.Close()
		return nil, stayed
	}
	if err := hand.Release(); err != nil {
		catch.Close()
		return nil, err
	}
	select {
	case drop, delivered := <-catch.Dropped():
		catch.Close()
		if !delivered {
			return nil, ErrClosed
		}
		return drop.Paths, drop.Err
	case <-ctx.Done():
		go closeOnceDropped(catch)
		return nil, ErrEdgeDropLate
	}
}

func acceptedInTime(ctx context.Context, catch edgeCatcher, limit time.Duration) bool {
	timer := time.NewTimer(limit)
	defer timer.Stop()
	select {
	case <-catch.Accepted():
		return true
	case <-timer.C:
		return false
	case <-ctx.Done():
		return false
	}
}

func stayWith(hand DragHand, why error) error {
	return errors.Join(why, hand.Escape(), hand.Release())
}

func closeOnceDropped(catch edgeCatcher) {
	for range catch.Dropped() {
	}
	catch.Close()
}
