//go:build linux && containertest

package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/rodrigogaraujo/lanhop/internal/x11"
)

const usage = `usage:
  xprobe pointer          prints where the pointer is: X Y
  xprobe warp X Y         moves the pointer to X Y
  xprobe owner SELECTION  prints the window that owns SELECTION, 0 for none`

func main() {
	if err := probe(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "xprobe:", err)
		os.Exit(1)
	}
}

func probe(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("no command\n%s", usage)
	}
	display, err := x11.OpenDisplayProbe()
	if err != nil {
		return err
	}
	defer display.Close()
	switch {
	case args[0] == "pointer" && len(args) == 1:
		x, y, err := display.Pointer()
		if err != nil {
			return err
		}
		fmt.Println(x, y)
		return nil
	case args[0] == "warp" && len(args) == 3:
		x, errX := strconv.Atoi(args[1])
		y, errY := strconv.Atoi(args[2])
		if errX != nil || errY != nil {
			return fmt.Errorf("warp takes two whole numbers\n%s", usage)
		}
		return display.Warp(x, y)
	case args[0] == "owner" && len(args) == 2:
		owner, err := display.SelectionOwner(args[1])
		if err != nil {
			return err
		}
		fmt.Println(owner)
		return nil
	}
	return fmt.Errorf("unknown command %q\n%s", args, usage)
}
