package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/rodrigogaraujo/lanhop/internal/trust"
)

var trustedStudio = trust.Peer{Name: "studio", Fingerprint: someFingerprint(3), Address: "192.168.0.11:24745"}

func TestTheSharerIsTheTrustedMachineWithAnAddressOrTheOneNamedWithFrom(t *testing.T) {
	cases := []struct {
		name    string
		trusted []trust.Peer
		from    string
		want    trust.Peer
	}{
		{"the only trusted machine", []trust.Peer{trustedDesk}, "", trustedDesk},
		{"the only one with an address", []trust.Peer{trustedLaptop, trustedDesk}, "", trustedDesk},
		{"the one named, among two with an address", []trust.Peer{trustedDesk, trustedStudio}, "studio", trustedStudio},
		{"the one named, when it is the only one", []trust.Peer{trustedDesk}, "desk", trustedDesk},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := sharerAmong(c.trusted, c.from)
			if err != nil || got != c.want {
				t.Errorf("sharerAmong = %+v, %v, want %+v", got, err, c.want)
			}
		})
	}
}

func TestReceiveExplainsWhyItCannotChooseASharer(t *testing.T) {
	cases := []struct {
		name     string
		trusted  []trust.Peer
		args     []string
		mentions []string
	}{
		{"nothing is paired", nil, nil, []string{"lanhop pair ADDRESS"}},
		{"the only trusted machine has no address", []trust.Peer{trustedLaptop}, nil, []string{"lanhop pair ADDRESS"}},
		{"two machines have an address", []trust.Peer{trustedDesk, trustedLaptop, trustedStudio}, nil, []string{"desk, studio", "--from NAME"}},
		{"the machine named is not trusted", []trust.Peer{trustedDesk}, []string{"--from", "printer"}, []string{`"printer"`, "lanhop peers"}},
		{"the machine named has no address", []trust.Peer{trustedDesk, trustedLaptop}, []string{"--from", "laptop"}, []string{`"laptop"`, "lanhop pair ADDRESS"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			trustAlready(t, useConfigDirectory(t), c.trusted...)
			var stdout, stderr bytes.Buffer

			if code := run(append([]string{"receive"}, c.args...), nobodyTyping(), &stdout, &stderr); code != 1 {
				t.Errorf("exit code = %d, want 1", code)
			}

			for _, mention := range c.mentions {
				if !strings.Contains(stderr.String(), mention) {
					t.Errorf("stderr = %q, want it to mention %q", stderr.String(), mention)
				}
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want nothing", stdout.String())
			}
		})
	}
}

func TestReceiveUsedWronglyShowsUsage(t *testing.T) {
	cases := map[string][]string{
		"an argument":        {"receive", "192.168.0.10"},
		"an unknown option":  {"receive", "--to", "desk"},
		"from without value": {"receive", "--from"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			trustAlready(t, useConfigDirectory(t), trustedDesk)
			var stdout, stderr bytes.Buffer

			if code := run(args, nobodyTyping(), &stdout, &stderr); code != 2 {
				t.Errorf("exit code = %d, want 2", code)
			}
			if !strings.Contains(stderr.String(), "Usage:") {
				t.Errorf("stderr = %q, want the usage", stderr.String())
			}
		})
	}
}

func TestHelpNamesReceiveAndItsOption(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"help"}, nobodyTyping(), &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	for _, mention := range []string{"lanhop receive ", "--from NAME"} {
		if !strings.Contains(stdout.String(), mention) {
			t.Errorf("help does not mention %q", mention)
		}
	}
}

func TestReceiveAskedForHelpPrintsTheUsageAndDoesNotStart(t *testing.T) {
	trustAlready(t, useConfigDirectory(t), trustedDesk)
	var stdout, stderr bytes.Buffer

	if code := run([]string{"receive", "--help"}, nobodyTyping(), &stdout, &stderr); code != 0 {
		t.Errorf("exit code = %d, stderr = %q, want 0", code, stderr.String())
	}

	if stdout.String() != usage {
		t.Errorf("stdout = %q, want only the usage", stdout.String())
	}
}

func TestAFailureThatRepeatsIsPrintedOnceUntilTheConnectionComesBack(t *testing.T) {
	refused := errors.New("connection refused")
	lost := errors.New("connection lost")
	var stdout bytes.Buffer
	report := connectionReport{stdout: &stdout, sharer: trust.Peer{Name: "desk"}}

	report.failed(refused)
	report.failed(refused)
	report.connected()
	report.failed(refused)
	report.failed(lost)
	report.failed(lost)

	want := "" +
		"Not connected: connection refused. lanhop keeps trying.\n" +
		"Connected to \"desk\".\n" +
		"Not connected: connection refused. lanhop keeps trying.\n" +
		"Not connected: connection lost. lanhop keeps trying.\n"
	if got := stdout.String(); got != want {
		t.Errorf("printed:\n%s\nwant:\n%s", got, want)
	}
}
