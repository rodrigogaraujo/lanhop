//go:build linux

package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/x11"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: lanhop-clipprobe read | write [-for 2m] <file>...")
		os.Exit(2)
	}
	board, err := x11.Open()
	if err != nil {
		fmt.Fprintln(os.Stderr, "open:", err)
		os.Exit(1)
	}
	defer board.Close()
	switch os.Args[1] {
	case "read":
		paths, filesErr := board.ReadFiles()
		fmt.Printf("ReadFiles: %q, error: %v\n", paths, filesErr)
		text, textErr := board.Read()
		fmt.Printf("Read: %q, error: %v\n", text, textErr)
	case "write":
		flags := flag.NewFlagSet("write", flag.ExitOnError)
		hold := flags.Duration("for", 2*time.Minute, "how long lanhop keeps the copy")
		flags.Parse(os.Args[2:])
		mark, err := board.WriteFiles(flags.Args())
		fmt.Printf("WriteFiles %q: mark %d, error: %v\n", flags.Args(), mark, err)
		if err != nil {
			os.Exit(1)
		}
		fmt.Printf("holding the copy for %s; paste it in Nautilus now\n", *hold)
		select {
		case <-time.After(*hold):
		case <-board.Done():
		}
		fmt.Println("done")
	default:
		fmt.Fprintln(os.Stderr, "unknown subcommand", os.Args[1])
		os.Exit(2)
	}
}
