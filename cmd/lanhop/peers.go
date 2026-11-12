package main

import (
	"fmt"
	"io"
	"strings"
)

func listPeers(stdout io.Writer) error {
	trusted, err := openTrusted()
	if err != nil {
		return err
	}
	peers := trusted.Peers()
	if len(peers) == 0 {
		fmt.Fprintln(stdout, "No machine is trusted yet. Pair one with: lanhop pair")
		return nil
	}
	for _, peer := range peers {
		columns := []string{peer.Name, peer.Fingerprint.Code()}
		if peer.Address != "" {
			columns = append(columns, peer.Address)
		}
		fmt.Fprintln(stdout, strings.Join(columns, "\t"))
	}
	return nil
}

func forget(args []string, stdout io.Writer) error {
	if len(args) != 1 {
		return usageError("forget takes the name of one machine, as lanhop peers shows it")
	}
	trusted, err := openTrusted()
	if err != nil {
		return err
	}
	if err := trusted.Remove(args[0]); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Forgot %q.\n", args[0])
	return nil
}
