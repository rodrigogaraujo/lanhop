package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/identity"
	"github.com/rodrigogaraujo/lanhop/internal/status"
	"github.com/rodrigogaraujo/lanhop/internal/transport"
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

func leftBehind(t *testing.T, directory, contents string) {
	t.Helper()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, status.FileName), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestStatusOfAServiceThatEndedGivesItsLastError(t *testing.T) {
	leftBehind(t, useConfigDirectory(t), "state=stopped\nerror=this machine is not paired\n")
	expectState(t, lanhopStatus(t), "stopped", "not running", "this machine is not paired")
}

func TestStatusOfAServiceThatDiedWhileTryingDoesNotRepeatItsLastTry(t *testing.T) {
	leftBehind(t, useConfigDirectory(t), "state=failing\nmachine=iMac\nerror=connection refused\n")
	line := lanhopStatus(t)
	expectState(t, line, "stopped", "not running")
	if strings.Contains(line, "connection refused") {
		t.Errorf("status %q repeats the last try of a service that is gone", line)
	}
}

const holdsTheStatus = "LANHOP_TEST_HOLDS_THE_STATUS"

func serviceInAnotherProcess(t *testing.T, directory string) func() {
	t.Helper()
	service := exec.Command(os.Args[0], "-test.run=^TestHelperHoldsTheStatusLikeAService$")
	service.Env = append(os.Environ(), configDirectoryVariable+"="+directory, holdsTheStatus+"=1")
	release, err := service.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Start(); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); !status.Running(directory); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the service never took the status")
		}
	}
	return func() {
		release.Close()
		service.Wait()
	}
}

func TestHelperHoldsTheStatusLikeAService(t *testing.T) {
	if os.Getenv(holdsTheStatus) == "" {
		t.Skip("only runs as the helper of another test")
	}
	openStatus().set(status.Report{State: status.Connected, Machines: []string{"iMac"}, Address: centralAddress})
	io.ReadAll(os.Stdin)
}

func TestASecondLanhopLeavesTheStatusOfTheServiceAlone(t *testing.T) {
	release := serviceInAnotherProcess(t, useConfigDirectory(t))
	defer release()
	byHand := openStatus()
	byHand.set(status.Report{State: status.Accessibility})
	byHand.ended(errors.New("address already in use"))
	expectState(t, lanhopStatus(t), "connected", `"iMac"`)
}

func TestShareKeepsOneStatusFromThePermissionToItsEnd(t *testing.T) {
	useConfigDirectory(t)
	record := openStatus()
	record.set(status.Report{State: status.Accessibility})
	changed := reportReceivers(openStatus(), time.Now, nil)
	changed(nil)
	expectState(t, lanhopStatus(t), "waiting")
	changed([]string{"desk"})
	expectState(t, lanhopStatus(t), "sharing", `"desk"`)
	record.ended(errors.New("the capture of the keyboard and mouse stopped"))
	expectState(t, lanhopStatus(t), "stopped", "the capture of the keyboard and mouse stopped")
}

func TestEndingBeforeTheConfigurationDirectoryExistsCreatesNothing(t *testing.T) {
	directory := useConfigDirectory(t)
	openStatus().ended(errors.New("sharing is not supported here"))
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Errorf("the configuration directory was created: %v", err)
	}
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

func TestNotReachedButThisTerminalGetsThroughIsMacOSBlockingLanhop(t *testing.T) {
	report := status.Report{State: status.NotReached, Machines: []string{"iMac"}, Address: centralAddress, Error: "no route to host"}
	line := describeStatus(report, true, reachable, "darwin")
	expectState(t, line, "blocked", `"iMac"`, centralAddress, "this terminal can", "Privacy & Security > Local Network")
}

func TestNotReachedButThisTerminalGetsThroughOnLinuxNamesTheError(t *testing.T) {
	report := status.Report{State: status.NotReached, Machines: []string{"iMac"}, Address: centralAddress, Error: "no route to host"}
	line := describeStatus(report, true, reachable, "linux")
	expectState(t, line, "blocked", "this terminal can", "no route to host")
	if strings.Contains(line, "Local Network") {
		t.Errorf("status %q names a setting of macOS on Linux", line)
	}
}

func TestNotReachedFromTheTerminalTooMeansTheCentralCannotBeReached(t *testing.T) {
	report := status.Report{State: status.NotReached, Machines: []string{"iMac"}, Address: centralAddress, Error: "no route to host"}
	line := describeStatus(report, true, unreachable, "darwin")
	expectState(t, line, "unreachable", `"iMac"`, centralAddress, errNoRouteFromTheTerminal.Error(), "lanhop share")
}

type trustingNobody struct{}

func (trustingNobody) Find(identity.Fingerprint) (trust.Peer, bool) { return trust.Peer{}, false }

func dialFailure(t *testing.T, peer trust.Peer) error {
	t.Helper()
	local, err := identity.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := transport.Dial(ctx, transport.Config{Identity: local}, peer)
	if err == nil {
		conn.Close()
		t.Fatalf("dialing %s succeeded", peer.Address)
	}
	return err
}

func TestAConnectionThatCouldNotOpenIsNotReached(t *testing.T) {
	noRoute := fmt.Errorf("transport: cannot connect to iMac at %s: %w", centralAddress, &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.EHOSTUNREACH)})
	if state := stateOfFailure(noRoute); state != status.NotReached {
		t.Errorf("stateOfFailure(%v) = %q, want %q", noRoute, state, status.NotReached)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := listener.Addr().String()
	listener.Close()
	refused := dialFailure(t, trust.Peer{Name: "iMac", Address: closed})
	if state := stateOfFailure(refused); state != status.NotReached {
		t.Errorf("stateOfFailure(%v) = %q, want %q", refused, state, status.NotReached)
	}
}

func TestACentralWithAnotherKeyIsAFailureAndNotAnUnreachedCentral(t *testing.T) {
	central, err := identity.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	listener, err := transport.Listen("127.0.0.1:0", transport.Config{Identity: central}, trustingNobody{})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	mismatch := dialFailure(t, trust.Peer{Name: "iMac", Address: listener.Addr().String(), Fingerprint: identity.Fingerprint{9}})
	if state := stateOfFailure(mismatch); state != status.Failing {
		t.Errorf("stateOfFailure(%v) = %q, want %q", mismatch, state, status.Failing)
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
	report := connectionReport{stdout: &bytes.Buffer{}, record: openStatus(), sharer: trust.Peer{Name: "iMac", Address: centralAddress}}
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
	changed := reportReceivers(openStatus(), time.Now, nil)
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
