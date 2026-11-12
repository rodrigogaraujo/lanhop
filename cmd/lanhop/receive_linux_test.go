//go:build linux && (386 || amd64 || arm || arm64 || loong64 || riscv64)

package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/evdev"
	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/transport"
	"github.com/rodrigogaraujo/lanhop/internal/trust"
)

const (
	virtualInputNames        = "/sys/devices/virtual/input/input*/name"
	leftShift         uint16 = 42
	rightShift        uint16 = 54
)

func skipUnlessVirtualDevicesCanBeCreated(t *testing.T) {
	t.Helper()
	probe, err := evdev.CreateVirtualDevice("lanhop test probe")
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
		t.Skipf("uinput is not available here: %v", err)
	}
	if err != nil {
		t.Fatalf("CreateVirtualDevice: %v", err)
	}
	probe.Close()
}

func virtualDevicesOfLanhop(t *testing.T) []string {
	t.Helper()
	names, err := filepath.Glob(virtualInputNames)
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	var own []string
	for _, name := range names {
		if contents, err := os.ReadFile(name); err == nil && strings.TrimSpace(string(contents)) == virtualDeviceName {
			own = append(own, filepath.Dir(name))
		}
	}
	return own
}

func virtualDeviceCreatedSince(t *testing.T, before []string) string {
	t.Helper()
	created := slices.DeleteFunc(virtualDevicesOfLanhop(t), func(device string) bool { return slices.Contains(before, device) })
	if len(created) != 1 {
		t.Fatalf("the command created the virtual devices %v, want exactly one", created)
	}
	return created[0]
}

func eventNodeOf(t *testing.T, sysDevice string) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(sysDevice, "event*"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("event node of %s: matches=%v err=%v", sysDevice, matches, err)
	}
	node := filepath.Join("/dev/input", filepath.Base(matches[0]))
	if _, err := os.Stat(node); err == nil {
		return node
	}
	return makeNodeFromSysfs(t, t.TempDir(), matches[0])
}

func makeNodeFromSysfs(t *testing.T, directory, sysPath string) string {
	t.Helper()
	numbers, err := os.ReadFile(filepath.Join(sysPath, "dev"))
	if err != nil {
		t.Fatalf("read device numbers: %v", err)
	}
	majorText, minorText, found := strings.Cut(strings.TrimSpace(string(numbers)), ":")
	if !found {
		t.Fatalf("unexpected device numbers %q", numbers)
	}
	major, majorErr := strconv.ParseUint(majorText, 10, 32)
	minor, minorErr := strconv.ParseUint(minorText, 10, 32)
	if majorErr != nil || minorErr != nil {
		t.Fatalf("unexpected device numbers %q", numbers)
	}
	node := filepath.Join(directory, filepath.Base(sysPath))
	if err := syscall.Mknod(node, syscall.S_IFCHR|0o600, int(deviceNumber(major, minor))); err != nil {
		t.Skipf("cannot create a device node here: %v", err)
	}
	return node
}

func deviceNumber(major, minor uint64) uint64 {
	return (major&0xfff)<<8 | (major&^0xfff)<<32 | minor&0xff | (minor&^0xff)<<12
}

func eventsReadFrom(t *testing.T, node string) <-chan event.Event {
	t.Helper()
	device, err := evdev.Open(node)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { device.Close() })
	events := make(chan event.Event, 64)
	go func() {
		for {
			read, err := device.Read()
			if err != nil {
				return
			}
			events <- read
		}
	}()
	return events
}

func sharerWaitingFor(t *testing.T, receiverDirectory string) *transport.Listener {
	t.Helper()
	sharerDirectory := t.TempDir()
	sharer, receiver := identityIn(t, sharerDirectory), identityIn(t, receiverDirectory)
	trustAlready(t, sharerDirectory, trust.Peer{Name: "laptop", Fingerprint: receiver.Fingerprint})
	listener, err := transport.Listen("127.0.0.1:0", transport.Config{Identity: sharer}, trustedIn(t, sharerDirectory))
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { listener.Close() })
	trustAlready(t, receiverDirectory, trust.Peer{Name: "desk", Fingerprint: sharer.Fingerprint, Address: listener.Addr().String()})
	return listener
}

func acceptReceiver(t *testing.T, listener *transport.Listener) *transport.Conn {
	t.Helper()
	accepted := make(chan *transport.Conn, 1)
	go func() {
		if conn, err := listener.Accept(); err == nil {
			accepted <- conn
		}
	}()
	conn := soon(t, "the command did not connect to the sharer", accepted)
	t.Cleanup(func() { conn.Close() })
	return conn
}

func soon[T any](t *testing.T, otherwise string, arriving <-chan T) T {
	t.Helper()
	select {
	case got := <-arriving:
		return got
	case <-time.After(testPatience):
		t.Fatal(otherwise)
		var nothing T
		return nothing
	}
}

func TestReceiveReplaysTheSharerOnARealVirtualDeviceUntilInterrupted(t *testing.T) {
	skipUnlessVirtualDevicesCanBeCreated(t)
	listener := sharerWaitingFor(t, useConfigDirectory(t))
	before := virtualDevicesOfLanhop(t)
	output := &screen{}

	finished := startCommand("", output, "receive")
	sharerSide := acceptReceiver(t, listener)
	virtualDevice := virtualDeviceCreatedSince(t, before)
	read := eventsReadFrom(t, eventNodeOf(t, virtualDevice))
	err := sharerSide.Send(
		event.Key(rightShift, event.KeyPressed), event.Sync(),
		event.Enter(),
		event.Key(leftShift, event.KeyPressed), event.Sync(),
		event.Relative(event.RelX, 7), event.Relative(event.RelY, -3), event.Sync(),
		event.Relative(event.RelWheel, 1), event.Sync(),
		event.Leave(),
	)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	want := []event.Event{
		event.Key(leftShift, event.KeyPressed), event.Sync(),
		event.Relative(event.RelX, 7), event.Relative(event.RelY, -3), event.Sync(),
		event.Relative(event.RelWheel, 1), event.Sync(),
		event.Key(leftShift, event.KeyReleased), event.Sync(),
	}
	for i, wanted := range want {
		if got := soon(t, "the virtual device did not emit event "+strconv.Itoa(i), read); got != wanted {
			t.Errorf("event %d read from the virtual device = %+v, want %+v", i, got, wanted)
		}
	}

	listener.Close()
	sharerSide.Close()
	for deadline := time.Now().Add(testPatience); !strings.Contains(output.String(), "Not connected: "); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("stdout = %q, want it to say that the connection was lost", output.String())
		}
	}

	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if result := soon(t, "the command kept running after the interrupt", finished); result.exitCode != 0 || result.stderr != "" {
		t.Errorf("exit code = %d, stderr = %q, want a clean exit", result.exitCode, result.stderr)
	}
	for _, mention := range []string{`Receiving from "desk" at ` + listener.Addr().String(), `Connected to "desk".`} {
		if !strings.Contains(output.String(), mention) {
			t.Errorf("stdout = %q, want it to mention %q", output.String(), mention)
		}
	}
	for deadline := time.Now().Add(testPatience); ; time.Sleep(5 * time.Millisecond) {
		if _, err := os.Stat(virtualDevice); errors.Is(err, os.ErrNotExist) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s still exists after the command ended", virtualDevice)
		}
	}
}
