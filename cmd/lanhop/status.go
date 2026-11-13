package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/status"
)

const reachTimeout = 3 * time.Second

type statusFile struct {
	owner     *status.Owner
	directory string
}

func openStatus() statusFile {
	directory, err := configDirectory()
	if err != nil {
		return statusFile{}
	}
	return statusFile{owner: status.OwnerOf(directory), directory: directory}
}

func (f statusFile) set(report status.Report) {
	if f.owner == nil {
		return
	}
	f.owner.Write(report)
}

func (f statusFile) lastReport() status.Report {
	if f.directory == "" {
		return status.Report{}
	}
	report, err := status.Read(f.directory)
	if err != nil {
		return status.Report{}
	}
	return report
}

func (f statusFile) ended(err error) {
	if f.owner == nil {
		return
	}
	if _, missing := os.Stat(f.directory); missing != nil && !f.owner.Owns() {
		return
	}
	report := status.Report{State: status.Stopped}
	if err != nil {
		report.Error = err.Error()
	}
	f.owner.Write(report)
}

func stateOfFailure(err error) status.State {
	var failed *net.OpError
	if errors.As(err, &failed) && failed.Op == "dial" {
		return status.NotReached
	}
	return status.Failing
}

func showStatus(args []string, stdout io.Writer) error {
	if len(args) > 0 {
		return usageError("status takes no argument")
	}
	directory, err := configDirectory()
	if err != nil {
		return err
	}
	report, err := status.Read(directory)
	if errors.Is(err, fs.ErrNotExist) {
		report = status.Report{State: status.Stopped}
	} else if err != nil {
		return err
	}
	fmt.Fprintln(stdout, describeStatus(report, status.Running(directory), reach, runtime.GOOS))
	return nil
}

func reach(address string) error {
	conn, err := net.DialTimeout("tcp", address, reachTimeout)
	if err != nil {
		return err
	}
	return conn.Close()
}

func describeStatus(report status.Report, running bool, reach func(address string) error, system string) string {
	if !running || report.State == status.Stopped {
		if report.State == status.Stopped && report.Error != "" {
			return "stopped: lanhop is not running here. It ended with: " + report.Error
		}
		return "stopped: lanhop is not running here."
	}
	machines := quotedNames(report.Machines)
	switch report.State {
	case status.Accessibility:
		return "accessibility: lanhop waits for the Accessibility permission. Allow lanhop in System Settings > Privacy & Security > Accessibility."
	case status.Connecting:
		return fmt.Sprintf("connecting: lanhop is trying to reach %s at %s.", machines, report.Address)
	case status.Connected:
		return fmt.Sprintf("connected: lanhop is connected to %s at %s.", machines, report.Address)
	case status.Failing:
		return fmt.Sprintf("failing: lanhop cannot connect to %s at %s, and keeps trying: %s", machines, report.Address, report.Error)
	case status.NotReached:
		return describeNotReached(report, machines, reach, system)
	case status.Waiting:
		return "waiting: lanhop shares the keyboard and mouse of this machine, and no machine is connected yet."
	case status.Sharing:
		return fmt.Sprintf("sharing: lanhop shares the keyboard and mouse of this machine with %s.", machines)
	}
	return fmt.Sprintf("%s: lanhop is in a state that this version of lanhop does not know.", report.State)
}

func describeNotReached(report status.Report, machines string, reach func(address string) error, system string) string {
	if err := reach(report.Address); err != nil {
		return fmt.Sprintf("unreachable: neither lanhop nor this terminal can reach %s at %s: %s. Check that it is on, that lanhop share runs on it and that its address did not change.", machines, report.Address, err)
	}
	if system == "darwin" {
		return fmt.Sprintf("blocked: lanhop cannot reach %s at %s, but this terminal can. macOS keeps lanhop off the local network: allow lanhop in System Settings > Privacy & Security > Local Network.", machines, report.Address)
	}
	return fmt.Sprintf("blocked: lanhop cannot reach %s at %s, but this terminal can. lanhop failed with: %s", machines, report.Address, report.Error)
}

func quotedNames(names []string) string {
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = fmt.Sprintf("%q", name)
	}
	switch len(quoted) {
	case 0:
		return "the other machine"
	case 1:
		return quoted[0]
	}
	return strings.Join(quoted[:len(quoted)-1], ", ") + " and " + quoted[len(quoted)-1]
}
