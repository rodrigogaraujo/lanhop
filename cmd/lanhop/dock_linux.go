//go:build linux

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/edge"
)

const dockLookupLimit = 2 * time.Second

func readDock() edge.Side {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	asking, cancel := context.WithTimeout(context.Background(), dockLookupLimit)
	defer cancel()
	return gnomeDesk{
		settings: func(args ...string) (string, error) {
			output, err := exec.CommandContext(asking, "gsettings", args...).Output()
			return string(output), err
		},
		installed: func(path string) bool {
			_, err := os.Stat(path)
			return err == nil
		},
		userExtensions: filepath.Join(home, userExtensionsPath),
	}.dock()
}
