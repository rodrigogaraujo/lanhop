package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/layout"
	"github.com/rodrigogaraujo/lanhop/internal/layoutedit"
	"github.com/rodrigogaraujo/lanhop/internal/pairing"
	"github.com/rodrigogaraujo/lanhop/internal/trust"
)

const (
	loopbackAnyPort = "127.0.0.1:0"
	tokenBytes      = 16
	browserPatience = 2 * time.Second
	closingPatience = time.Second

	editingAt = `Arrange the desk at %s

lanhop opens that page in the browser of this machine. Drag each machine to its place and press Save.
Press Done on the page, or Ctrl+C here, to stop.
`
	browserDidNotOpen = `
lanhop could not open a browser on this machine: %v.
Open the address above in a browser on this machine. Without a browser, stop with Ctrl+C and
edit the layout file by hand: each line is a row of machines, from left to right, with a dot
for an empty place.
`
	layoutCannotBeEdited = "the layout file has problems, so it cannot be arranged by dragging:\n%s\nFix those lines by hand, or remove the file to start from an empty desk."
)

var errNoGraphicalSession = errors.New("this session has no display")

var openInBrowser = launchBrowser

func browserCommand(system string, environment func(name string) string) (name string, found bool) {
	if system == "darwin" {
		return "open", true
	}
	if environment("DISPLAY") == "" && environment("WAYLAND_DISPLAY") == "" {
		return "", false
	}
	return "xdg-open", true
}

func launchBrowser(done context.Context, address string) error {
	name, found := browserCommand(runtime.GOOS, os.Getenv)
	if !found {
		return errNoGraphicalSession
	}
	return launch(done, exec.Command(name, address), browserPatience)
}

func launch(done context.Context, command *exec.Cmd, patience time.Duration) error {
	inAProcessGroupOfItsOwn(command)
	if err := command.Start(); err != nil {
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- command.Wait() }()
	select {
	case err := <-exited:
		return err
	case <-time.After(patience):
		return nil
	case <-done.Done():
		return nil
	}
}

func newToken() (string, error) {
	secret := make([]byte, tokenBytes)
	if _, err := rand.Read(secret); err != nil {
		return "", err
	}
	return hex.EncodeToString(secret), nil
}

func editLayoutUntil(interrupted context.Context, stdout io.Writer) error {
	directory, err := configDirectory()
	if err != nil {
		return err
	}
	peersPath := filepath.Join(directory, peersFileName)
	if _, err := trust.Open(peersPath); err != nil {
		return err
	}
	editor := layoutedit.Editor{
		Path:    filepath.Join(directory, layoutFileName),
		Own:     pairing.OwnName(),
		Trusted: peersOnDisk(peersPath),
	}
	fmt.Fprintf(stdout, layoutFileIs, editor.Path)
	var problems layout.Problems
	if _, err := editor.Read(); errors.As(err, &problems) {
		return fmt.Errorf(layoutCannotBeEdited, indented(problems))
	} else if err != nil {
		return err
	}
	token, err := newToken()
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", loopbackAnyPort)
	if err != nil {
		return err
	}
	host := listener.Addr().String()
	address := "http://" + host + "/?token=" + token
	stopped, stop := context.WithCancel(interrupted)
	defer stop()
	server := &http.Server{Handler: editor.Handler(host, token, stop)}
	failed := make(chan error, 1)
	go func() { failed <- server.Serve(listener) }()

	fmt.Fprintf(stdout, editingAt, address)
	if err := openInBrowser(stopped, address); err != nil {
		fmt.Fprintf(stdout, browserDidNotOpen, err)
	}
	select {
	case err := <-failed:
		return err
	case <-stopped.Done():
	}
	closing, giveUp := context.WithTimeout(context.Background(), closingPatience)
	defer giveUp()
	if server.Shutdown(closing) != nil {
		server.Close()
	}
	return nil
}
