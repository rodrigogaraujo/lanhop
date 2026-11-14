package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/arrival"
	"github.com/rodrigogaraujo/lanhop/internal/clipshare"
	"github.com/rodrigogaraujo/lanhop/internal/transport"
	"github.com/rodrigogaraujo/lanhop/internal/trust"
)

const (
	filesSwitchFileName     = "files"
	oldArrivalsRemovedEvery = time.Hour
)

var openArrivals = func() (*arrival.Folder, error) {
	return arrival.Open(arrival.Config{})
}

func (s copySwitches) filesState() string {
	switch {
	case !s.files:
		return switchedOff
	case !s.clipboard:
		return "off, because lanhop clipboard is off"
	}
	return switchedOn
}

func (s copySwitches) afterTheFilesSwitch() string {
	switch {
	case !s.files && s.clipboard:
		return "Copies of files no longer move to or from this machine, and copies of text still do. " + followedAtTheNextFocus
	case !s.files:
		return "Copies of files no longer move to or from this machine. " + followedAtTheNextFocus
	case !s.clipboard:
		return "Copies of files stay off until the clipboard is on again. Switch it on with: lanhop clipboard on"
	}
	return "Copies of files move to and from this machine. " + followedAtTheNextFocus
}

func switchFiles(args []string, stdout io.Writer) error {
	return showOrFlipSwitch("files", filesSwitchFileName, args, stdout, copySwitches.filesState, copySwitches.afterTheFilesSwitch)
}

func keepArrivals(done context.Context, log io.Writer) (*arrival.Folder, func()) {
	folder, err := openArrivals()
	if err != nil {
		fmt.Fprintf(log, "lanhop cannot open the folder for the files that arrive: %v\n", err)
		return nil, func() {}
	}
	removeOldArrivals(folder, log)
	stopRemoving := runUntilStopped(done, func(ctx context.Context) {
		keepRemovingOldArrivals(ctx, folder, oldArrivalsRemovedEvery, log)
	})
	return folder, func() {
		stopRemoving()
		folder.Close()
	}
}

func keepRemovingOldArrivals(ctx context.Context, folder *arrival.Folder, every time.Duration, log io.Writer) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			removeOldArrivals(folder, log)
		}
	}
}

func removeOldArrivals(folder *arrival.Folder, log io.Writer) {
	if err := folder.RemoveOld(); err != nil {
		fmt.Fprintf(log, "lanhop could not remove the files that arrived more than a day ago: %v\n", err)
	}
}

func dataConnectionsTo(central trust.Peer, config transport.Config) clipshare.DialData {
	return func(ctx context.Context, transfer int32) (*transport.DataConn, error) {
		return transport.DialData(ctx, config, central, transfer)
	}
}
