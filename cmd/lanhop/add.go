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
	"strconv"
	"strings"
	"syscall"

	"github.com/rodrigogaraujo/lanhop/internal/hotkey"
	"github.com/rodrigogaraujo/lanhop/internal/identity"
	"github.com/rodrigogaraujo/lanhop/internal/pairing"
	"github.com/rodrigogaraujo/lanhop/internal/sharer"
	"github.com/rodrigogaraujo/lanhop/internal/trust"
)

var errAnotherPairingWaits = errors.New("another lanhop add or lanhop pair is already waiting on this machine. Let it finish, or stop it with Ctrl+C")

func add(args []string, stdout io.Writer) error {
	everyInterface := ":" + strconv.Itoa(pairing.DefaultPort)
	flags := flag.NewFlagSet("add", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	address := flags.String("address", "", "")
	listen := flags.String("listen", everyInterface, "")
	if err := flags.Parse(args); errors.Is(err, flag.ErrHelp) {
		fmt.Fprint(stdout, usage)
		return nil
	} else if err != nil {
		return usageError(err.Error())
	}
	if flags.NArg() != 1 {
		return usageError("add takes the name of the new machine, after its options")
	}
	name := flags.Arg(0)
	ownName := pairing.OwnName()
	switch {
	case !trust.ValidName(name):
		return usageError(fmt.Sprintf("%q must have 1 to %d letters, digits, hyphens or underscores", name, trust.MaxNameLength))
	case name == ownName:
		return usageError(fmt.Sprintf("%q is the name of this machine. Give the new machine another name", name))
	case *address != "" && !usableJoinAddress(*address):
		return usageError(fmt.Sprintf("--address %q must be an IPv4 address or a host name, with a port from 1 to 65535 or without", *address))
	}

	directory, err := configDirectory()
	if err != nil {
		return err
	}
	local, err := identity.LoadOrCreate(directory)
	if err != nil {
		return err
	}
	trusted, err := openTrusted()
	if err != nil {
		return err
	}
	for _, peer := range trusted.Peers() {
		if peer.Name == name {
			return fmt.Errorf("a machine named %q is already trusted here. Give the new machine another name, or first run: lanhop forget %s", name, name)
		}
	}

	listener, err := net.Listen("tcp", *listen)
	if errors.Is(err, syscall.EADDRINUSE) {
		return errAnotherPairingWaits
	}
	if err != nil {
		return err
	}
	_, port, _ := net.SplitHostPort(listener.Addr().String())

	hosts := []string{*address}
	if *address == "" {
		hosts = joinAddresses(interfacesOfThisMachine(), addressOfTheDefaultRoute())
	}
	if len(hosts) == 0 {
		listener.Close()
		return errors.New("this machine has no address on a private network. Give the one the new machine can reach with --address")
	}

	code, err := pairing.NewJoinCode(local.Fingerprint)
	if err != nil {
		listener.Close()
		return err
	}
	installer, short := joinLines(version, joinAddress(hosts[0], port), code)
	fmt.Fprintf(stdout, "Waiting for %s to join, for 10 minutes. Press Ctrl+C to stop.\n\n", name)
	fmt.Fprintf(stdout, "On %s, if it has no lanhop yet, type:\n\n  %s\n\n", name, installer)
	fmt.Fprintf(stdout, "If it has lanhop already, type:\n\n  %s\n\n", short)
	if len(hosts) > 1 {
		fmt.Fprintf(stdout, "The line uses %s. This machine also has %s: if %s cannot reach the first one, run lanhop add again with --address.\n\n", hosts[0], strings.Join(hosts[1:], ", "), name)
	}

	interrupted, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	joined, err := pairing.WaitForJoin(interrupted, listener, pairing.WaitConfig{
		Identity: local,
		Store:    trusted,
		OwnName:  ownName,
		Name:     name,
		Code:     code,
		Rejected: func(from string, reason error) {
			fmt.Fprintf(stdout, "A connection from %s did not join: %v\n", from, reason)
		},
	})
	switch {
	case errors.Is(err, pairing.ErrWaitOver) && interrupted.Err() != nil:
		fmt.Fprintf(stdout, "Stopped. %s did not join.\n", name)
		return nil
	case errors.Is(err, pairing.ErrWaitOver), errors.Is(err, pairing.ErrTooManyAttempts):
		return fmt.Errorf("%w. Run lanhop add %s again for a new line", err, name)
	case err != nil:
		return err
	}
	fmt.Fprintf(stdout, "%s joined from %s. %s\n", joined.Peer.Name, joined.From, hotkeyOf(trusted, joined.Peer.Name))
	placeLater := fmt.Sprintf("Place %s on the desk later with: lanhop layout --edit\n", joined.Peer.Name)
	if interrupted.Err() != nil {
		fmt.Fprint(stdout, placeLater)
		return nil
	}
	fmt.Fprintf(stdout, "Opening the layout editor, where %s waits in the tray: drag it to its place on the desk, save, and press Done.\n\n", joined.Peer.Name)
	if err := editLayoutUntil(interrupted, stdout); err != nil {
		fmt.Fprintf(stdout, "The layout editor did not open: %v\n%s", err, placeLater)
	}
	return nil
}

func hotkeyOf(trusted *trust.Store, name string) string {
	for position, peer := range trusted.Peers() {
		if peer.Name != name {
			continue
		}
		number, reachable := sharer.HotkeyNumber(position)
		if !reachable {
			return "It has no hotkey: the hotkeys reach only the first machines that lanhop peers lists. It can still be reached through the edges of the layout."
		}
		return fmt.Sprintf("%s+%d moves to it.", hotkey.DefaultModifiers, number)
	}
	return ""
}
