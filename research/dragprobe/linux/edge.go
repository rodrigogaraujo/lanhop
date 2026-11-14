//go:build linux

package main

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/x11"
	"github.com/rodrigogaraujo/lanhop/research/dragprobe/cli"
)

type edgeResult struct {
	drop x11.EdgeDrop
	err  error
}

type edgeStrip struct {
	drags *x11.Drags
	edge  *x11.Edge
	stop  context.CancelFunc
	done  chan struct{}
	drops chan edgeResult
}

func openEdge(ctx context.Context, c command, p *cli.Printer) (*edgeStrip, error) {
	drags, err := x11.OpenDrags(c.display)
	if err != nil {
		return nil, err
	}
	edge, err := drags.OpenEdge(x11.EdgeOptions{Side: c.edge, Width: c.width, Say: func(line string) { p.Say("edge: %s", line) }})
	if err != nil {
		drags.Close()
		return nil, err
	}
	r := edge.Rect()
	p.Say("edge window 0x%x mapped and raised over the %s edge: %dx%d at %d,%d, InputOnly, override-redirect, XdndAware version 5", edge.Window(), c.edge, r.Width, r.Height, r.X, r.Y)
	serving, stop := context.WithCancel(ctx)
	strip := &edgeStrip{drags: drags, edge: edge, stop: stop, done: make(chan struct{}), drops: make(chan edgeResult, 1)}
	go func() {
		defer close(strip.done)
		drop, err := edge.NextDrop(serving)
		strip.drops <- edgeResult{drop, err}
	}()
	return strip, nil
}

func (s *edgeStrip) close(p *cli.Printer) {
	s.stop()
	<-s.done
	window := s.edge.Window()
	err := s.edge.Close()
	s.drags.Close()
	if err != nil {
		p.Say("edge window 0x%x: %v while unmapping and destroying it", window, err)
		return
	}
	p.Say("edge window 0x%x unmapped and destroyed", window)
}

func describeEdgeDrop(drop x11.EdgeDrop, p *cli.Printer) {
	p.Say("the edge took a drop from 0x%x: XDND version %d, %d XdndPosition, %.1f ms from XdndEnter to XdndDrop; text/uri-list read: %v; XdndFinished accepted %v with %s, %.1f ms after XdndDrop",
		drop.Source, drop.Version, drop.Positions, millis(drop.EnterToDrop), drop.URIListErr == nil, drop.Accepted, drop.FinishAction, millis(drop.DropToFinish))
}

func localPaths(list string) []string {
	var paths []string
	for _, line := range strings.Split(list, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		address, err := url.Parse(line)
		if err != nil || address.Scheme != "file" || address.Path == "" {
			continue
		}
		paths = append(paths, address.Path)
	}
	return paths
}

func reportFiles(list string, p *cli.Printer) {
	for _, path := range localPaths(list) {
		_, err := os.Stat(path)
		p.Say("dragged file still in its place: %v (%s)", err == nil, path)
	}
}

func stoppedBecause(ctx context.Context, err error, limit time.Duration, awaited string, p *cli.Printer) error {
	switch {
	case ctx.Err() != nil:
		p.Say("stopped")
		return nil
	case errors.Is(err, context.DeadlineExceeded):
		p.Say("no %s within %v, so the probe stops", awaited, limit)
		return nil
	}
	return err
}

func dropOnEdge(ctx context.Context, c command, drags *x11.Drags, p *cli.Printer) error {
	if err := drags.Watch(); err != nil {
		return err
	}
	waiting, stop := context.WithTimeout(ctx, c.timeout)
	defer stop()
	strip, err := openEdge(waiting, c, p)
	if err != nil {
		return err
	}
	defer strip.close(p)
	describeOwner(drags, p)
	p.Say("drag the throwaway file in Nautilus, push the pointer against the %s edge of the screen, and release the button there; the probe stops after the drop, after %v without one, or on Ctrl+C", c.edge, c.timeout)
	for {
		select {
		case change := <-drags.OwnerChanges():
			p.Say("XdndSelection: %s, owner 0x%x", change.Reason, change.Owner)
		case got := <-strip.drops:
			if got.err != nil {
				return stoppedBecause(ctx, got.err, c.timeout, "drop on the edge", p)
			}
			describeEdgeDrop(got.drop, p)
			reportFiles(got.drop.URIList, p)
			if _, err := observe(ctx, drags, c.after, nil, p); err != nil {
				return err
			}
			reportFiles(got.drop.URIList, p)
			return nil
		}
	}
}
