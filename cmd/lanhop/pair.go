package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"

	"github.com/rodrigogaraujo/lanhop/internal/identity"
	"github.com/rodrigogaraujo/lanhop/internal/pairing"
	"github.com/rodrigogaraujo/lanhop/internal/trust"
)

const question = `
The other machine will be trusted here as %q.

  This machine:   %s
  Other machine:  %s

Look at the screen of the other machine. It must show the same two codes, swapped:
there, "This machine" must be %s and "Other machine" must be %s.

If it does, type yes and press Enter. Anything else refuses.
`

type terminal struct {
	stdin  io.Reader
	stdout io.Writer
}

func (t terminal) Confirm(asked pairing.Question) bool {
	fmt.Fprintf(t.stdout, question, asked.Name, asked.OwnCode, asked.OtherCode, asked.OtherCode, asked.OwnCode)
	answer, _ := bufio.NewReader(t.stdin).ReadString('\n')
	return strings.EqualFold(strings.TrimSpace(answer), "yes")
}

func pair(args []string, stdin io.Reader, stdout io.Writer) error {
	everyInterface := ":" + strconv.Itoa(pairing.DefaultPort)
	flags := flag.NewFlagSet("pair", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	as := flags.String("as", "", "")
	listen := flags.String("listen", everyInterface, "")
	if err := flags.Parse(args); errors.Is(err, flag.ErrHelp) {
		fmt.Fprint(stdout, usage)
		return nil
	} else if err != nil {
		return usageError(err.Error())
	}
	dialing := flags.NArg() == 1
	switch {
	case flags.NArg() > 1:
		return usageError("pair takes one address at most, after its options")
	case *as != "" && !trust.ValidName(*as):
		return usageError(fmt.Sprintf("--as %q must have 1 to %d letters, digits, hyphens or underscores", *as, trust.MaxNameLength))
	case dialing && *listen != everyInterface:
		return usageError("--listen is for the machine that waits, so it cannot come with an address")
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
	config := pairing.Config{
		Identity: local,
		Store:    trusted,
		Name:     pairing.OwnName(),
		As:       *as,
		Asker:    terminal{stdin: stdin, stdout: stdout},
	}

	if dialing {
		peer, err := pairing.Dial(flags.Arg(0), config)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Paired. %q is now trusted, at %s.\n", peer.Name, peer.Address)
		return nil
	}

	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	fmt.Fprintf(stdout, "Waiting for the other machine on port %d. On it, run:\n\n  lanhop pair ADDRESS-OF-THIS-MACHINE:%d\n", port, port)
	peer, err := pairing.Accept(listener, config)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Paired. %q is now trusted.\n", peer.Name)
	return nil
}
