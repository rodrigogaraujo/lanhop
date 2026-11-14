package main

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/update"
)

const patience = 2 * time.Minute

func main() {
	if err := verify(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "verify-release:", err)
		os.Exit(1)
	}
}

func verify(args []string) error {
	flags := flag.NewFlagSet("verify-release", flag.ContinueOnError)
	folder := flags.String("release", "", "the folder that holds the files of the release")
	versionText := flags.String("version", "", "the version of the release")
	fromText := flags.String("from", "", "the version in packaging/update-from")
	keyFile := flags.String("key", "", "a PEM public key to check the signature with, instead of the keys compiled into lanhop")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if runtime.GOOS != "linux" {
		return errors.New("it runs on Linux, where it can run the Linux program of the release")
	}
	want, err := update.ParseVersion(*versionText)
	if err != nil {
		return err
	}
	from, err := update.ParseVersion(*fromText)
	if err != nil {
		return err
	}
	keys := update.ProjectKeys()
	if *keyFile != "" {
		key, err := readPublicKey(*keyFile)
		if err != nil {
			return err
		}
		keys = []ed25519.PublicKey{key}
	}
	if len(keys) == 0 {
		return errors.New("lanhop carries no release key yet, and no -key was given")
	}

	served := httptest.NewServer(http.StripPrefix("/download/"+want.String()+"/", http.FileServer(http.Dir(*folder))))
	defer served.Close()
	beside, err := os.MkdirTemp("", "verify-release-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(beside)

	ctx, stop := context.WithTimeout(context.Background(), patience)
	defer stop()
	source := update.Source{Releases: served.URL, Keys: keys}
	target := update.Target{Program: filepath.Join(beside, "lanhop"), System: "linux", Arch: runtime.GOARCH, Running: from}
	staged, err := source.Prepare(ctx, target, want, true)
	if err != nil {
		return err
	}
	if err := staged.Discard(); err != nil {
		return err
	}
	fmt.Printf("%s: SHA256SUMS.sig, UPDATE and lanhop-linux-%s.tar.gz pass the checks of lanhop update, and the program says lanhop %s.\n", *folder, runtime.GOARCH, want)
	return nil
}

func readPublicKey(file string) (ed25519.PublicKey, error) {
	content, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(content)
	if block == nil {
		return nil, fmt.Errorf("%s holds no PEM block", file)
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	key, ok := parsed.(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("%s is not an Ed25519 public key", file)
	}
	return key, nil
}
