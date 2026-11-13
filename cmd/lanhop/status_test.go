package main

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/rodrigogaraujo/lanhop/internal/status"
	"github.com/rodrigogaraujo/lanhop/internal/trust"
)

const centralAddress = "192.168.1.9:24745"

var errNoRouteFromTheTerminal = errors.New("dial tcp 192.168.1.9:24745: connect: no route to host")

func reachable(string) error   { return nil }
func unreachable(string) error { return errNoRouteFromTheTerminal }

func lanhopStatus(t *testing.T) string {
	t.Helper()
	var out bytes.Buffer
	if err := showStatus(nil, &out); err != nil {
		t.Fatalf("lanhop status: %v", err)
	}
	return out.String()
}

func expectState(t *testing.T, line, state string, mentions ...string) {
	t.Helper()
	if !strings.HasPrefix(line, state+": ") {
		t.Errorf("status %q, want it to start with %q", line, state+": ")
	}
	for _, mention := range mentions {
		if !strings.Contains(line, mention) {
			t.Errorf("status %q, want it to mention %q", line, mention)
		}
	}
}

func TestStatusWithoutAServiceSaysItIsStopped(t *testing.T) {
	useConfigDirectory(t)
	expectState(t, lanhopStatus(t), "stopped", "not running")
}

func TestStatusOfAServiceThatEndedGivesItsLastError(t *testing.T) {
	directory := useConfigDirectory(t)
	ended := status.Report{PID: 0, State: status.Connected, Machines: []string{"iMac"}, Error: "this machine is not paired"}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := status.Write(filepath.Join(directory, status.FileName), ended); err != nil {
		t.Fatal(err)
	}
	expectState(t, lanhopStatus(t), "stopped", "not running", "this machine is not paired")
}

func TestStatusReadsWhatTheRunningServiceWrote(t *testing.T) {
	useConfigDirectory(t)
	openStatus().set(status.Report{State: status.Connected, Machines: []string{"iMac"}, Address: centralAddress})
	expectState(t, lanhopStatus(t), "connected", `"iMac"`, centralAddress)
}

func TestServiceThatEndsWritesThatItStoppedAndWhy(t *testing.T) {
	useConfigDirectory(t)
	file := openStatus()
	file.set(status.Report{State: status.Connected, Machines: []string{"iMac"}, Address: centralAddress})
	file.ended(errors.New("the capture stopped"))
	expectState(t, lanhopStatus(t), "stopped", "the capture stopped")
}

func TestEachStateOfARunningServiceHasItsOwnLine(t *testing.T) {
	for _, test := range []struct {
		report   status.Report
		state    string
		mentions []string
	}{
		{status.Report{State: status.Accessibility}, "accessibility", []string{"Privacy & Security > Accessibility"}},
		{status.Report{State: status.Connecting, Machines: []string{"iMac"}, Address: centralAddress}, "connecting", []string{`"iMac"`, centralAddress}},
		{status.Report{State: status.Connected, Machines: []string{"iMac"}, Address: centralAddress}, "connected", []string{`"iMac"`, centralAddress}},
		{status.Report{State: status.Failing, Machines: []string{"iMac"}, Address: centralAddress, Error: "connection refused"}, "failing", []string{`"iMac"`, "connection refused", "keeps trying"}},
		{status.Report{State: status.Waiting}, "waiting", []string{"no machine is connected"}},
		{status.Report{State: status.Sharing, Machines: []string{"desk"}}, "sharing", []string{`with "desk".`}},
		{status.Report{State: status.Sharing, Machines: []string{"desk", "laptop", "tablet"}}, "sharing", []string{`"desk", "laptop" and "tablet"`}},
		{status.Report{State: "napping"}, "napping", []string{"does not know"}},
	} {
		line := describeStatus(test.report, true, reachable, "linux")
		expectState(t, line, test.state, test.mentions...)
	}
}

func TestNoRouteThatThisTerminalGetsThroughIsMacOSBlockingLanhop(t *testing.T) {
	report := status.Report{State: status.NoRoute, Machines: []string{"iMac"}, Address: centralAddress, Error: "no route to host"}
	line := describeStatus(report, true, reachable, "darwin")
	expectState(t, line, "blocked", `"iMac"`, centralAddress, "this terminal can", "Privacy & Security > Local Network")
}

func TestNoRouteThatThisTerminalGetsThroughOnLinuxNamesTheError(t *testing.T) {
	report := status.Report{State: status.NoRoute, Machines: []string{"iMac"}, Address: centralAddress, Error: "no route to host"}
	line := describeStatus(report, true, reachable, "linux")
	expectState(t, line, "blocked", "this terminal can", "no route to host")
	if strings.Contains(line, "Local Network") {
		t.Errorf("status %q names a setting of macOS on Linux", line)
	}
}

func TestNoRouteForTheTerminalTooMeansTheCentralCannotBeReached(t *testing.T) {
	report := status.Report{State: status.NoRoute, Machines: []string{"iMac"}, Address: centralAddress, Error: "no route to host"}
	line := describeStatus(report, true, unreachable, "darwin")
	expectState(t, line, "unreachable", `"iMac"`, centralAddress, errNoRouteFromTheTerminal.Error(), "lanhop share")
}

func TestNoRouteIsToldApartFromOtherFailures(t *testing.T) {
	noRoute := fmt.Errorf("transport: cannot connect to iMac at %s: %w", centralAddress, &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.EHOSTUNREACH)})
	if state := stateOfFailure(noRoute); state != status.NoRoute {
		t.Errorf("stateOfFailure(%v) = %q, want %q", noRoute, state, status.NoRoute)
	}
	refused := fmt.Errorf("transport: cannot connect: %w", os.NewSyscallError("connect", syscall.ECONNREFUSED))
	if state := stateOfFailure(refused); state != status.Failing {
		t.Errorf("stateOfFailure(%v) = %q, want %q", refused, state, status.Failing)
	}
}

func TestTryingAnAddressFromHereSucceedsOnlyWhereSomethingListens(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := reach(address); err != nil {
		t.Errorf("reach(%s) = %v while a listener is there", address, err)
	}
	listener.Close()
	if err := reach(address); err == nil {
		t.Errorf("reach(%s) = nil after the listener closed", address)
	}
}

func TestReceiverWritesEachChangeOfItsConnection(t *testing.T) {
	useConfigDirectory(t)
	report := connectionReport{stdout: &bytes.Buffer{}, status: openStatus(), sharer: trust.Peer{Name: "iMac", Address: centralAddress}}
	report.connecting()
	expectState(t, lanhopStatus(t), "connecting", `"iMac"`)
	report.failed(fmt.Errorf("dial: %w", syscall.ECONNREFUSED))
	expectState(t, lanhopStatus(t), "failing", "connection refused")
	report.connected()
	expectState(t, lanhopStatus(t), "connected", `"iMac"`, centralAddress)
	report.waitingForPermission()
	expectState(t, lanhopStatus(t), "accessibility")
}

func TestSharerWritesWhichMachinesAreConnected(t *testing.T) {
	useConfigDirectory(t)
	changed := reportReceivers(openStatus())
	changed(nil)
	expectState(t, lanhopStatus(t), "waiting")
	changed([]string{"desk", "laptop"})
	expectState(t, lanhopStatus(t), "sharing", `"desk" and "laptop"`)
}

func TestStatusTakesNoArgument(t *testing.T) {
	var misuse usageError
	if err := showStatus([]string{"now"}, &bytes.Buffer{}); !errors.As(err, &misuse) {
		t.Errorf("showStatus with an argument = %v, want a usage error", err)
	}
}
