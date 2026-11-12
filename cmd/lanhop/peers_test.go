package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rodrigogaraujo/lanhop/internal/trust"
)

var (
	trustedLaptop = trust.Peer{Name: "laptop", Fingerprint: someFingerprint(1)}
	trustedDesk   = trust.Peer{Name: "desk", Fingerprint: someFingerprint(2), Address: "192.168.0.10:24745"}
)

func TestPeersListsNameCodeAndAddressOfEachTrustedMachine(t *testing.T) {
	trustAlready(t, useConfigDirectory(t), trustedLaptop, trustedDesk)
	var stdout, stderr bytes.Buffer

	if code := run([]string{"peers"}, nobodyTyping(), &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}

	want := "laptop\t" + trustedLaptop.Fingerprint.Code() + "\n" +
		"desk\t" + trustedDesk.Fingerprint.Code() + "\t192.168.0.10:24745\n"
	if got := stdout.String(); got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestPeersSaysWhenNoMachineIsTrusted(t *testing.T) {
	useConfigDirectory(t)
	var stdout, stderr bytes.Buffer

	if code := run([]string{"peers"}, nobodyTyping(), &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}

	if got := stdout.String(); !strings.Contains(got, "No machine is trusted yet") || !strings.Contains(got, "lanhop pair") {
		t.Errorf("stdout = %q, want a line that says nothing is trusted and how to pair", got)
	}
}

func TestPeersFailsOnADamagedListInsteadOfShowingItEmpty(t *testing.T) {
	directory := useConfigDirectory(t)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, peersFileName), []byte("not json"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	var stdout, stderr bytes.Buffer

	if code := run([]string{"peers"}, nobodyTyping(), &stdout, &stderr); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "damaged") || stdout.Len() != 0 {
		t.Errorf("stdout = %q, stderr = %q, want only an error that says the file is damaged", stdout.String(), stderr.String())
	}
}

func TestForgetRemovesOnlyTheNamedMachine(t *testing.T) {
	directory := useConfigDirectory(t)
	trustAlready(t, directory, trustedLaptop, trustedDesk)
	var stdout, stderr bytes.Buffer

	if code := run([]string{"forget", "laptop"}, nobodyTyping(), &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}

	if got := trustedIn(t, directory).Peers(); !slices.Equal(got, []trust.Peer{trustedDesk}) {
		t.Errorf("after forgetting laptop the machine trusts %+v, want only %+v", got, trustedDesk)
	}
	if got, want := stdout.String(), "Forgot \"laptop\".\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestForgetOfAnUnknownNameFailsAndKeepsEveryMachine(t *testing.T) {
	directory := useConfigDirectory(t)
	trustAlready(t, directory, trustedLaptop, trustedDesk)
	var stdout, stderr bytes.Buffer

	if code := run([]string{"forget", "printer"}, nobodyTyping(), &stdout, &stderr); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}

	if !strings.Contains(stderr.String(), `"printer"`) {
		t.Errorf("stderr = %q, want it to name the machine that is not trusted", stderr.String())
	}
	if got := trustedIn(t, directory).Peers(); !slices.Equal(got, []trust.Peer{trustedLaptop, trustedDesk}) {
		t.Errorf("the machine trusts %+v, want %+v and %+v", got, trustedLaptop, trustedDesk)
	}
}

func TestConfigDirectoryComesFromTheEnvironmentVariable(t *testing.T) {
	chosen := filepath.Join(t.TempDir(), "elsewhere")
	t.Setenv(configDirectoryVariable, chosen)

	if got, err := configDirectory(); err != nil || got != chosen {
		t.Errorf("configDirectory = %q, %v, want %q", got, err, chosen)
	}
}

func TestConfigDirectoryDefaultsToLanhopUnderTheUserConfigDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv(configDirectoryVariable, "")
	base, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("UserConfigDir: %v", err)
	}

	got, err := configDirectory()

	if err != nil || got != filepath.Join(base, "lanhop") || !strings.HasPrefix(got, home) {
		t.Errorf("configDirectory = %q, %v, want %q", got, err, filepath.Join(base, "lanhop"))
	}
}
