//go:build containertest

package main

import (
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rodrigogaraujo/lanhop/internal/identity"
	"github.com/rodrigogaraujo/lanhop/internal/transport"
	"github.com/rodrigogaraujo/lanhop/internal/trust"
)

const (
	peersFileName  = "peers.json"
	layoutFileName = "layout"
)

func main() {
	desk := flag.String("desk", "/desk", "the folder that holds the config folder of each machine, named after it")
	central := flag.String("central", "central", "the name of the machine that runs lanhop share")
	sides := flag.String("sides", "l1,l2", "the names of the machines that run lanhop receive, from left to right")
	port := flag.Int("port", transport.DefaultPort, "the port lanhop share listens on")
	flag.Parse()
	if flag.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "usage: pair [-desk FOLDER] [-central NAME] [-sides NAME,NAME] [-port PORT]")
		os.Exit(64)
	}
	if err := pair(*desk, *central, strings.Split(*sides, ","), *port, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "pair:", err)
		os.Exit(1)
	}
}

func pair(desk, central string, sides []string, port int, stdout io.Writer) error {
	fingerprints := make(map[string]identity.Fingerprint)
	for _, name := range append([]string{central}, sides...) {
		if !trust.ValidName(name) {
			return fmt.Errorf("%q is not a valid machine name", name)
		}
		own, err := identity.LoadOrCreate(filepath.Join(desk, name))
		if err != nil {
			return err
		}
		fingerprints[name] = own.Fingerprint
		fmt.Fprintf(stdout, "%s\t%s\n", name, own.Fingerprint.Code())
	}
	trustedByTheCentral, err := trust.Open(filepath.Join(desk, central, peersFileName))
	if err != nil {
		return err
	}
	address := net.JoinHostPort(central, strconv.Itoa(port))
	for _, side := range sides {
		if err := trustedByTheCentral.Add(trust.Peer{Name: side, Fingerprint: fingerprints[side]}); err != nil {
			return err
		}
		trustedBySide, err := trust.Open(filepath.Join(desk, side, peersFileName))
		if err != nil {
			return err
		}
		if err := trustedBySide.Add(trust.Peer{Name: central, Fingerprint: fingerprints[central], Address: address}); err != nil {
			return err
		}
	}
	layout := strings.Join(sides, "  ") + "\n"
	return os.WriteFile(filepath.Join(desk, central, layoutFileName), []byte(layout), 0o600)
}
