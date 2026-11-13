package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rodrigogaraujo/lanhop/internal/identity"
	"github.com/rodrigogaraujo/lanhop/internal/pairing"
)

const (
	joinVariable  = "LANHOP_JOIN"
	unitName      = "lanhop.service"
	receivingRole = "receive"
)

func join(args []string, stdout io.Writer) error {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Fprint(stdout, usage)
		return nil
	}
	if len(args) != 2 {
		return usageError("join takes the address of the central and the join code that lanhop add printed there")
	}
	address := args[0]
	code, err := pairing.ParseJoinCode(args[1])
	if err != nil {
		return usageError(fmt.Sprintf("%q is not a join code: it has 24 letters and digits, in groups of four", args[1]))
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
	central, err := pairing.Join(address, code, pairing.JoinConfig{Identity: local, Store: trusted})
	switch {
	case errors.Is(err, pairing.ErrAlreadyJoined):
		fmt.Fprintf(stdout, "This machine already trusts %q, the central of this join code. If %q no longer lists this machine, run lanhop forget %s here and type the same line again.\n", central.Name, central.Name, central.Name)
		return nil
	case errors.Is(err, pairing.ErrSelf):
		return errors.New("this machine is the central of this join code. Type the line on the machine you are adding")
	case errors.Is(err, pairing.ErrNotWaiting):
		return fmt.Errorf("%s is not waiting for a machine to join. Run lanhop add there again for a new line", address)
	case errors.Is(err, pairing.ErrUnreachable):
		return fmt.Errorf("%w. Check the address, and that no VPN on this machine blocks the local network", err)
	case err != nil:
		return err
	}

	fmt.Fprintf(stdout, "Joined %q at %s.\n", central.Name, central.Address)
	if os.Getenv(joinVariable) != "" {
		return nil
	}
	if receivingServiceIsInstalled() {
		fmt.Fprintf(stdout, "The lanhop service of this machine should connect to %q within about 10 seconds. lanhop status says when.\n", central.Name)
		return nil
	}
	fmt.Fprintf(stdout, "To start lanhop on this machine with each login, type:\n\n  %s\n", installerLine(version, "LANHOP_ROLE="+receivingRole))
	return nil
}

func receivingServiceIsInstalled() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	units := os.Getenv("XDG_CONFIG_HOME")
	if units == "" {
		units = filepath.Join(home, ".config")
	}
	if agent, err := os.ReadFile(filepath.Join(home, "Library", "LaunchAgents", agentLabel+".plist")); err == nil {
		return strings.Contains(string(agent), "<string>"+receivingRole+"</string>")
	}
	unit, err := os.ReadFile(filepath.Join(units, "systemd", "user", unitName))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(unit), "\n") {
		if strings.HasPrefix(line, "ExecStart=") && strings.HasSuffix(strings.TrimSpace(line), " "+receivingRole) {
			return true
		}
	}
	return false
}
