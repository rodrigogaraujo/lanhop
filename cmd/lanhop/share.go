package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/edge"
	"github.com/rodrigogaraujo/lanhop/internal/features"
	"github.com/rodrigogaraujo/lanhop/internal/hotkey"
	"github.com/rodrigogaraujo/lanhop/internal/identity"
	"github.com/rodrigogaraujo/lanhop/internal/pairing"
	"github.com/rodrigogaraujo/lanhop/internal/sharer"
	"github.com/rodrigogaraujo/lanhop/internal/status"
	"github.com/rodrigogaraujo/lanhop/internal/transport"
	"github.com/rodrigogaraujo/lanhop/internal/trust"
)

const sharing = `Sharing the keyboard and mouse of this machine on port %d.

  %[2]s+1  brings them back to this machine
  %[2]s+2  gives them to the first machine that "lanhop peers" lists, %[2]s+3 to the second, and so on

Press Ctrl+C here to stop.
`

type inputCapture interface {
	sharer.Capture
	Close() error
}

type peersOnDisk string

func (p peersOnDisk) Peers() []trust.Peer {
	return p.read().Peers()
}

func (p peersOnDisk) Find(fingerprint identity.Fingerprint) (trust.Peer, bool) {
	return p.read().Find(fingerprint)
}

func (p peersOnDisk) read() *trust.Store {
	stored, err := trust.Open(string(p))
	if err != nil {
		return &trust.Store{}
	}
	return stored
}

func share(args []string, stdout io.Writer) (err error) {
	flags := flag.NewFlagSet("share", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	listen := flags.String("listen", ":"+strconv.Itoa(transport.DefaultPort), "")
	if err := flags.Parse(args); errors.Is(err, flag.ErrHelp) {
		fmt.Fprint(stdout, usage)
		return nil
	} else if err != nil {
		return usageError(err.Error())
	}
	if flags.NArg() > 0 {
		return usageError("share takes no argument besides its options")
	}

	record := openStatus()
	quiet := quietAboutThePermission(record)
	defer func() { record.ended(err) }()
	interrupted, stopWaiting := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stopWaiting()
	waiting := func() {
		if !quiet {
			fmt.Fprintln(stdout, waitingForAccessibility)
		}
		record.set(status.Report{State: status.Accessibility})
	}
	if err := waitUntilAllowedToShare(interrupted, waiting); err != nil {
		return err
	}
	if interrupted.Err() != nil {
		return nil
	}
	capture, err := startCapture()
	if err != nil {
		return err
	}
	defer capture.Close()
	return shareUntil(interrupted, capture, *listen, stdout)
}

func reportReceivers(record statusFile, now func() time.Time) func(names []string) {
	var connected []string
	departed := map[string]time.Time{}
	return func(names []string) {
		for _, name := range connected {
			if !slices.Contains(names, name) {
				departed[name] = now()
			}
		}
		for _, name := range names {
			delete(departed, name)
		}
		connected = slices.Clone(names)
		left := make([]status.Departure, 0, len(departed))
		for name, at := range departed {
			left = append(left, status.Departure{Name: name, At: at})
		}
		slices.SortFunc(left, func(a, b status.Departure) int { return strings.Compare(a.Name, b.Name) })
		if len(names) == 0 {
			record.set(status.Report{State: status.Waiting, Left: left})
			return
		}
		record.set(status.Report{State: status.Sharing, Machines: names, Left: left})
	}
}

func shareUntil(done context.Context, capture sharer.Capture, address string, stdout io.Writer) error {
	directory, err := configDirectory()
	if err != nil {
		return err
	}
	local, err := identity.LoadOrCreate(directory)
	if err != nil {
		return err
	}
	peersPath := filepath.Join(directory, peersFileName)
	if _, err := trust.Open(peersPath); err != nil {
		return err
	}
	trusted := peersOnDisk(peersPath)
	detector, err := hotkey.New(hotkey.DefaultModifiers)
	if err != nil {
		return err
	}
	listener, err := transport.Listen(address, transport.Config{Identity: local}, trusted)
	if err != nil {
		return err
	}
	defer listener.Close()

	own := pairing.OwnName()
	desk := &deskOnDisk{
		path:      filepath.Join(directory, layoutFileName),
		peersPath: peersPath,
		own:       own,
		known:     knownOnThisDesk(own, trusted),
		stdout:    stdout,
	}
	machine := sharer.New(capture, trusted, detector, sharer.EdgeSwitching{
		Name:   own,
		Layout: desk.Layout,
		Speed:  sharer.DefaultSpeed(),
		Watch:  edge.DefaultSettings(),
	})
	desk.ownEdges = machine.OwnScreenHasEdges()
	machine.OnReceivers(reportReceivers(openStatus(), time.Now))
	defer shareTheClipboard(done, machine, directory, new(features.Set), stdout)()
	go func() {
		for {
			receiver, err := listener.Accept()
			if err != nil {
				return
			}
			machine.Connect(receiver)
		}
	}()
	fmt.Fprintf(stdout, sharing, listener.Addr().(*net.TCPAddr).Port, hotkey.DefaultModifiers)
	desk.start()
	following, stopFollowing := context.WithCancel(done)
	stoppedFollowing := make(chan struct{})
	go func() {
		defer close(stoppedFollowing)
		desk.follow(following, layoutCheckInterval)
	}()
	defer func() {
		stopFollowing()
		<-stoppedFollowing
	}()
	return machine.Run(done)
}
