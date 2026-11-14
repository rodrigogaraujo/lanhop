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
	"sync"
	"syscall"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/edge"
	"github.com/rodrigogaraujo/lanhop/internal/features"
	"github.com/rodrigogaraujo/lanhop/internal/follow"
	"github.com/rodrigogaraujo/lanhop/internal/hotkey"
	"github.com/rodrigogaraujo/lanhop/internal/identity"
	"github.com/rodrigogaraujo/lanhop/internal/pairing"
	"github.com/rodrigogaraujo/lanhop/internal/sharer"
	"github.com/rodrigogaraujo/lanhop/internal/status"
	"github.com/rodrigogaraujo/lanhop/internal/transport"
	"github.com/rodrigogaraujo/lanhop/internal/trust"
	"github.com/rodrigogaraujo/lanhop/internal/update"
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

type peerList interface {
	Peers() []trust.Peer
}

type deskStatus struct {
	mutex     sync.Mutex
	record    statusFile
	now       func() time.Time
	trusted   peerList
	connected []string
	departed  map[string]time.Time
	seats     []status.Seat
	stopped   bool
}

func newDeskStatus(record statusFile, now func() time.Time, trusted peerList) *deskStatus {
	return &deskStatus{record: record, now: now, trusted: trusted, departed: map[string]time.Time{}}
}

func (d *deskStatus) receivers(names []string) {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	for _, name := range d.connected {
		if !slices.Contains(names, name) {
			d.departed[name] = d.now()
		}
	}
	for _, name := range names {
		delete(d.departed, name)
	}
	if d.trusted != nil {
		stillTrusted := map[string]bool{}
		for _, peer := range d.trusted.Peers() {
			stillTrusted[peer.Name] = true
		}
		for name := range d.departed {
			if !stillTrusted[name] {
				delete(d.departed, name)
			}
		}
	}
	d.connected = slices.Clone(names)
	d.write()
}

func (d *deskStatus) stop() {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	d.stopped = true
}

func (d *deskStatus) seatsChanged(seats []follow.Seat) {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	d.seats = d.seats[:0]
	for _, seat := range seats {
		written := status.Seat{Name: seat.Name, Mark: string(seat.Mark)}
		if seat.Version != (update.Version{}) {
			written.Version = seat.Version.String()
		}
		d.seats = append(d.seats, written)
	}
	d.write()
}

func (d *deskStatus) write() {
	if d.stopped {
		return
	}
	left := make([]status.Departure, 0, len(d.departed))
	for name, at := range d.departed {
		left = append(left, status.Departure{Name: name, At: at})
	}
	slices.SortFunc(left, func(a, b status.Departure) int { return strings.Compare(a.Name, b.Name) })
	report := status.Report{State: status.Sharing, Machines: d.connected, Left: left, Seats: slices.Clone(d.seats)}
	if len(d.connected) == 0 {
		report.State = status.Waiting
		report.Machines = nil
	}
	d.record.set(report)
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
	report := newDeskStatus(openStatus(), time.Now, trusted)
	defer report.stop()
	machine.OnReceivers(report.receivers)
	defer shareTheClipboard(done, machine, directory, listener.AwaitData, new(features.Set), followerOfTheCentral(report.seatsChanged), stdout)()
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
