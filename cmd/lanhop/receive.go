package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"

	"github.com/rodrigogaraujo/lanhop/internal/features"
	"github.com/rodrigogaraujo/lanhop/internal/identity"
	"github.com/rodrigogaraujo/lanhop/internal/receiver"
	"github.com/rodrigogaraujo/lanhop/internal/status"
	"github.com/rodrigogaraujo/lanhop/internal/transport"
	"github.com/rodrigogaraujo/lanhop/internal/trust"
)

type connectionReport struct {
	stdout      io.Writer
	quiet       bool
	record      statusFile
	sharer      trust.Peer
	lastFailure string
}

func (r *connectionReport) waitingForPermission() {
	if !r.quiet {
		fmt.Fprintln(r.stdout, waitingForAccessibility)
	}
	r.record.set(status.Report{State: status.Accessibility})
}

func (r *connectionReport) connecting() {
	r.record.set(r.about(status.Connecting, nil))
}

func (r *connectionReport) connected() {
	r.lastFailure = ""
	fmt.Fprintf(r.stdout, "Connected to %q.\n", r.sharer.Name)
	r.record.set(r.about(status.Connected, nil))
}

func (r *connectionReport) failed(reason error) {
	r.record.set(r.about(stateOfFailure(reason), reason))
	failure := reason.Error()
	if failure == r.lastFailure {
		return
	}
	r.lastFailure = failure
	fmt.Fprintf(r.stdout, "Not connected: %s. lanhop keeps trying.\n", failure)
}

func (r *connectionReport) about(state status.State, reason error) status.Report {
	report := status.Report{State: state, Machines: []string{r.sharer.Name}, Address: r.sharer.Address}
	if reason != nil {
		report.Error = reason.Error()
	}
	return report
}

func sharerAmong(trusted []trust.Peer, from string) (trust.Peer, error) {
	if from != "" {
		return sharerNamed(trusted, from)
	}
	withAddress := slices.DeleteFunc(slices.Clone(trusted), func(peer trust.Peer) bool { return peer.Address == "" })
	switch len(withAddress) {
	case 0:
		return trust.Peer{}, errors.New("this machine is not paired with a machine to receive from. First run: lanhop pair ADDRESS")
	case 1:
		return withAddress[0], nil
	}
	names := make([]string, len(withAddress))
	for i, peer := range withAddress {
		names[i] = peer.Name
	}
	return trust.Peer{}, fmt.Errorf("more than one trusted machine has an address (%s). Choose one with: lanhop receive --from NAME", strings.Join(names, ", "))
}

func sharerNamed(trusted []trust.Peer, name string) (trust.Peer, error) {
	for _, peer := range trusted {
		if peer.Name != name {
			continue
		}
		if peer.Address == "" {
			return trust.Peer{}, fmt.Errorf("%q has no address here, because it was paired by waiting for it. To receive from it, forget it and run: lanhop pair ADDRESS", name)
		}
		return peer, nil
	}
	return trust.Peer{}, fmt.Errorf("no trusted machine is named %q. See the names with: lanhop peers", name)
}

func receive(args []string, stdout io.Writer) (err error) {
	flags := flag.NewFlagSet("receive", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	from := flags.String("from", "", "")
	if err := flags.Parse(args); errors.Is(err, flag.ErrHelp) {
		fmt.Fprint(stdout, usage)
		return nil
	} else if err != nil {
		return usageError(err.Error())
	}
	if flags.NArg() > 0 {
		return usageError("receive takes no argument, only its option")
	}
	report := connectionReport{stdout: stdout, record: openStatus()}
	report.quiet = quietAboutThePermission(report.record)
	defer func() { report.record.ended(err) }()

	trusted, err := openTrusted()
	if err != nil {
		return err
	}
	sharer, err := sharerAmong(trusted.Peers(), *from)
	if err != nil {
		return err
	}
	report.sharer = sharer
	directory, err := configDirectory()
	if err != nil {
		return err
	}
	local, err := identity.LoadOrCreate(directory)
	if err != nil {
		return err
	}
	interrupted, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	if err := waitUntilAllowedToControl(interrupted, report.waitingForPermission); err != nil {
		return err
	}
	if interrupted.Err() != nil {
		return nil
	}
	replay, err := openReplay()
	if err != nil {
		return err
	}

	fmt.Fprintf(stdout, "Receiving from %q at %s. Press Ctrl+C to stop.\n", sharer.Name, sharer.Address)
	report.connecting()
	clipboard, stopSharingTheClipboard := clipboardOfThisSide(interrupted, directory, new(features.Set), stdout)
	defer stopSharingTheClipboard()
	return receiver.Run(interrupted, receiver.Config{
		Transport: transport.Config{Identity: local},
		Sharer:    sharer,
		Replay:    replay,
		Clipboard: clipboard,
		OnConnect: report.connected,
		OnFailure: report.failed,
	})
}
