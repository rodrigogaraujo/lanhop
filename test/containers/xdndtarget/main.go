//go:build linux && containertest

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/rodrigogaraujo/lanhop/internal/x11"
)

func main() {
	out := flag.String("out", "", "the folder that dropped files are copied into")
	x := flag.Int("x", 0, "left edge of the window")
	y := flag.Int("y", 0, "top edge of the window")
	width := flag.Int("width", 300, "width of the window")
	height := flag.Int("height", 300, "height of the window")
	flag.Parse()
	if *out == "" || flag.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "usage: xdndtarget -out FOLDER [-x X -y Y -width W -height H]")
		os.Exit(64)
	}
	if err := os.MkdirAll(*out, 0o700); err != nil {
		fmt.Fprintln(os.Stderr, "xdndtarget:", err)
		os.Exit(1)
	}
	interrupted := make(chan os.Signal, 1)
	signal.Notify(interrupted, os.Interrupt, syscall.SIGTERM)
	target, err := x11.StartXdndTestTarget(x11.XdndTestTargetConfig{X: *x, Y: *y, Width: *width, Height: *height, Take: copyInto(*out), Log: os.Stdout})
	if err != nil {
		fmt.Fprintln(os.Stderr, "xdndtarget:", err)
		os.Exit(1)
	}
	<-interrupted
	target.Close()
}

func copyInto(folder string) func(paths []string) error {
	return func(paths []string) error {
		for _, path := range paths {
			if err := copyFile(path, filepath.Join(folder, filepath.Base(path))); err != nil {
				return err
			}
		}
		return nil
	}
}

func copyFile(from, to string) error {
	source, err := os.Open(from)
	if err != nil {
		return err
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", from)
	}
	temporary, err := os.CreateTemp(filepath.Dir(to), "."+filepath.Base(to)+".*")
	if err != nil {
		return err
	}
	_, err = io.Copy(temporary, source)
	err = errors.Join(err, temporary.Close())
	if err == nil {
		err = os.Rename(temporary.Name(), to)
	}
	if err != nil {
		os.Remove(temporary.Name())
	}
	return err
}
