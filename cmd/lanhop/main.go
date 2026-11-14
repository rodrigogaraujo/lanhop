package main

import (
	"errors"
	"fmt"
	"io"
	"os"
)

var version = "dev"

const usage = `lanhop shares one keyboard and mouse across computers on the same network.

Usage:
  lanhop add NAME        on the machine with the keyboard: print a line that adds a new machine as NAME, and wait for it
  lanhop join ADDRESS CODE
                         on the new machine: join the machine with the keyboard, with the line that lanhop add printed
  lanhop pair            on the machine with the keyboard: wait for another machine and pair with it
  lanhop pair ADDRESS    on the other machine: pair with the machine that waits at ADDRESS
  lanhop share           on the machine with the keyboard: share it and the mouse with the paired machines
  lanhop receive         on the other machine: let the machine with the keyboard control this one
  lanhop status          say what lanhop is doing on this machine: waiting for a permission, connecting or connected
  lanhop update          move this machine to the latest release of lanhop, and restart its service
  lanhop updates         say whether this machine follows the version of the machine with the keyboard: on or off
  lanhop updates off     keep this machine on its version, whatever the machine with the keyboard runs
  lanhop updates on      follow the version of the machine with the keyboard again, as after a new install
  lanhop peers           list the machines this one trusts
  lanhop forget NAME     stop trusting a machine
  lanhop layout          show where the layout file is and how lanhop understands it
  lanhop layout --edit   arrange the machines on the desk by dragging them, in a browser
  lanhop clipboard       say whether copies of text move between this machine and the others: on or off
  lanhop clipboard off   keep copies of text and files from leaving or reaching this machine
  lanhop clipboard on    let copies of text move again, as after a new install
  lanhop files           say whether copies of files move between this machine and the others: on or off
  lanhop files off       keep copies of files from leaving or reaching this machine, while text still moves
  lanhop files on        let copies of files move again, as after a new install
  lanhop devices         list the input devices this machine can read
  lanhop version         print the version

Options of add, before NAME:
  --address ADDRESS   put ADDRESS in the line, when the new machine cannot reach the one lanhop add picks
  --listen ADDRESS    wait on ADDRESS instead of port 24746 of every network interface

Options of pair, before ADDRESS:
  --as NAME           call the other machine NAME here, when the name it proposes is taken
  --listen ADDRESS    wait on ADDRESS instead of port 24746 of every network interface

Options of update:
  --version VERSION   move to VERSION, such as v0.1.3, instead of the latest release
  --older             with --version, accept a release older than this one, and stop following
                      the version of the machine with the keyboard

Option of share:
  --listen ADDRESS    wait on ADDRESS instead of port 24745 of every network interface

Option of receive:
  --from NAME         receive from the trusted machine NAME, when more than one has an address

The layout file draws the desk, so that pushing the cursor through an edge of a
screen reaches the machine on that side. Each line is a row of machines, from
left to right, by the names "lanhop peers" lists, with a dot for an empty place.

lanhop keeps the key of this machine, the list of trusted machines, the
layout, the clipboard switch and the files switch in its configuration
directory. Set LANHOP_CONFIG_DIR to use another directory.
`

type usageError string

func (e usageError) Error() string {
	return string(e)
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	return exitCodeFor(dispatch(args[0], args[1:], stdin, stdout), stderr)
}

func exitCodeFor(err error, stderr io.Writer) int {
	var misuse usageError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &misuse):
		fmt.Fprintf(stderr, "lanhop: %s\n\n%s", misuse, usage)
		return 2
	case errors.Is(err, errAskAgainAfterARestart):
		return 1
	case errors.Is(err, errRestartForTheUpdate):
		return exitStatusForTheRestart
	default:
		fmt.Fprintln(stderr, "lanhop:", err)
		return 1
	}
}

func dispatch(command string, args []string, stdin io.Reader, stdout io.Writer) error {
	switch command {
	case "version":
		fmt.Fprintln(stdout, "lanhop", version)
		return nil
	case "devices":
		return listDevices(stdout)
	case "receive":
		return receive(args, stdout)
	case "pair":
		return pair(args, stdin, stdout)
	case "add":
		return add(args, stdout)
	case "join":
		return join(args, stdout)
	case "share":
		return share(args, stdout)
	case "status":
		return showStatus(args, stdout)
	case "update":
		return updateThisMachine(args, stdout)
	case "updates":
		return switchUpdates(args, stdout)
	case "peers":
		return listPeers(stdout)
	case "forget":
		return forget(args, stdout)
	case "layout":
		return showLayout(args, stdout)
	case "clipboard":
		return switchClipboard(args, stdout)
	case "files":
		return switchFiles(args, stdout)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return nil
	}
	return usageError(fmt.Sprintf("unknown command %q", command))
}
