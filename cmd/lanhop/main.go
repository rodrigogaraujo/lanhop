package main

import (
	"fmt"
	"io"
	"os"
)

var version = "dev"

const usage = `lanhop shares one keyboard and mouse across computers on the same network.

Usage:
  lanhop version    print the version
  lanhop devices    list the input devices this machine can read
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "version":
		fmt.Fprintln(stdout, "lanhop", version)
		return 0
	case "devices":
		if err := listDevices(stdout); err != nil {
			fmt.Fprintln(stderr, "lanhop:", err)
			return 1
		}
		return 0
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	}
	fmt.Fprintf(stderr, "lanhop: unknown command %q\n\n%s", args[0], usage)
	return 2
}
