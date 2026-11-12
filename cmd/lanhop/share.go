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
	"strconv"
	"syscall"

	"github.com/rodrigogaraujo/lanhop/internal/hotkey"
	"github.com/rodrigogaraujo/lanhop/internal/identity"
	"github.com/rodrigogaraujo/lanhop/internal/sharer"
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

func share(args []string, stdout io.Writer) error {
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

	capture, err := startCapture()
	if err != nil {
		return err
	}
	defer capture.Close()
	interrupted, stopWaiting := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stopWaiting()
	return shareUntil(interrupted, capture, *listen, stdout)
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

	machine := sharer.New(capture, trusted, detector)
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
	return machine.Run(done)
}
