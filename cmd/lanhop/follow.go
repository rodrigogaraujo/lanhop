package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rodrigogaraujo/lanhop/internal/features"
	"github.com/rodrigogaraujo/lanhop/internal/follow"
	"github.com/rodrigogaraujo/lanhop/internal/update"
)

const (
	serviceVariable = "LANHOP_SERVICE"
	updatesOff      = "off"
)

var (
	updateFrom              = ""
	errRestartForTheUpdate  = errors.New("lanhop ended so that its service starts the new version")
	exitStatusForTheRestart = 3
)

func releaseOf(text string) update.Version {
	release, err := update.ParseVersion(text)
	if err != nil {
		return update.Version{}
	}
	return release
}

func updatesSwitchedOn(directory string) bool {
	contents, err := os.ReadFile(filepath.Join(directory, updatesFileName))
	if err != nil {
		return true
	}
	return !strings.EqualFold(strings.TrimSpace(string(contents)), updatesOff)
}

func followsTheCentral(directory string) func() bool {
	return func() bool {
		return os.Getenv(serviceVariable) == "1" && len(projectReleaseKeys) > 0 && updatesSwitchedOn(directory)
	}
}

type machineUpdater struct {
	directory string
}

type preparedUpdate struct {
	directory string
	staged    update.Staged
	target    update.Target
}

func (u machineUpdater) Prepare(ctx context.Context, want update.Version) (follow.Prepared, error) {
	target, err := installedTarget()
	if err != nil {
		return nil, err
	}
	source := update.Source{Releases: releasesAddress, Client: patientClient(), Keys: projectReleaseKeys, CheckApp: checkNewApp}
	patient, stop := context.WithTimeout(ctx, downloadPatience)
	defer stop()
	staged, err := source.Prepare(patient, target, want, false)
	if err != nil {
		return nil, err
	}
	return preparedUpdate{directory: u.directory, staged: staged, target: target}, nil
}

func (p preparedUpdate) Install() error {
	unlock, err := waitForTheUpdateLock(p.directory)
	if err != nil {
		return err
	}
	defer unlock()
	return update.Install(p.staged, p.target)
}

func (p preparedUpdate) Discard() {
	p.staged.Discard()
}

func followerOfThisSide(ctx context.Context, directory string, announced *features.Set, restart func(), stdout io.Writer) *follow.Side {
	return follow.NewSide(ctx, follow.SideConfig{
		Running:   releaseOf(version),
		Following: followsTheCentral(directory),
		Features:  announced,
		Updater:   machineUpdater{directory: directory},
		Restart:   restart,
		Log:       stdout,
	})
}

func followerOfTheCentral(onChange func([]follow.Seat)) *follow.Central {
	return follow.NewCentral(follow.CentralConfig{Running: releaseOf(version), From: releaseOf(updateFrom), OnChange: onChange})
}

func switchUpdates(args []string, stdout io.Writer) error {
	directory, err := configDirectory()
	if err != nil {
		return err
	}
	switch {
	case len(args) == 0:
		state := "on"
		if !updatesSwitchedOn(directory) {
			state = "off"
		}
		fmt.Fprintln(stdout, state)
		return nil
	case len(args) == 1 && (args[0] == "on" || args[0] == "off"):
		if err := writeUpdatesSwitch(directory, args[0] == "on"); err != nil {
			return err
		}
		if args[0] == "on" {
			fmt.Fprintln(stdout, "This machine follows the version of the machine with the keyboard again. A running lanhop takes the change at its next focus change or connection.")
		} else {
			fmt.Fprintln(stdout, "This machine stays on its version, whatever the machine with the keyboard runs. A running lanhop takes the change at its next focus change or connection.")
		}
		return nil
	}
	return usageError("updates takes on, off, or nothing")
}
