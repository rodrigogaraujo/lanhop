//go:build linux && containertest

package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/rodrigogaraujo/lanhop/internal/x11"
)

func main() {
	file := flag.String("file", "", "the file that a press in the window starts to drag")
	x := flag.Int("x", 0, "left edge of the window")
	y := flag.Int("y", 0, "top edge of the window")
	width := flag.Int("width", 300, "width of the window")
	height := flag.Int("height", 300, "height of the window")
	flag.Parse()
	if *file == "" || flag.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "usage: xdndsource -file PATH [-x X -y Y -width W -height H]")
		os.Exit(64)
	}
	interrupted := make(chan os.Signal, 1)
	signal.Notify(interrupted, os.Interrupt, syscall.SIGTERM)
	source, err := x11.StartXdndTestSource(x11.XdndTestSourceConfig{File: *file, X: *x, Y: *y, Width: *width, Height: *height, Log: os.Stdout})
	if err != nil {
		fmt.Fprintln(os.Stderr, "xdndsource:", err)
		os.Exit(1)
	}
	<-interrupted
	source.Close()
}
