package identity

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func mustLoadOrCreate(t *testing.T, directory string) *Identity {
	t.Helper()
	identity, err := LoadOrCreate(directory)
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	return identity
}

func skipWithoutFileModes(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("file modes are not enforced on Windows")
	}
}

func TestIdentityIsCreatedOnceAndLoadedAfterwards(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "lanhop")

	created := mustLoadOrCreate(t, directory)
	loaded := mustLoadOrCreate(t, directory)

	if created.Fingerprint != loaded.Fingerprint {
		t.Errorf("fingerprint changed between runs: %s then %s", created.Fingerprint, loaded.Fingerprint)
	}
}

func TestEachMachineGetsItsOwnIdentity(t *testing.T) {
	first := mustLoadOrCreate(t, t.TempDir())
	second := mustLoadOrCreate(t, t.TempDir())

	if first.Fingerprint == second.Fingerprint {
		t.Error("two new identities share a fingerprint")
	}
}

func TestIdentityFileIsPrivateToItsOwner(t *testing.T) {
	skipWithoutFileModes(t)
	directory := filepath.Join(t.TempDir(), "lanhop")
	mustLoadOrCreate(t, directory)

	for path, want := range map[string]os.FileMode{directory: 0o700, filepath.Join(directory, fileName): 0o600} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Stat: %v", err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s has mode %04o, want %04o", path, got, want)
		}
	}
}

func TestIdentityReadableByOthersIsRefused(t *testing.T) {
	skipWithoutFileModes(t)
	directory := t.TempDir()
	mustLoadOrCreate(t, directory)
	if err := os.Chmod(filepath.Join(directory, fileName), 0o644); err != nil {
		t.Fatalf("Chmod: %v", err)
	}

	_, err := LoadOrCreate(directory)
	if err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Errorf("LoadOrCreate = %v, want an error that says how to fix the mode", err)
	}
}

func TestDamagedIdentityIsReportedAndNotReplaced(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, fileName)
	damaged := []byte("not a key")
	if err := os.WriteFile(path, damaged, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := LoadOrCreate(directory); err == nil || !strings.Contains(err.Error(), "damaged") {
		t.Errorf("LoadOrCreate = %v, want an error that says the file is damaged", err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(contents) != string(damaged) {
		t.Error("the damaged file was replaced")
	}
}

func TestCertificateCarriesAnEd25519KeyAndMatchesTheFingerprint(t *testing.T) {
	identity := mustLoadOrCreate(t, t.TempDir())

	if _, ok := identity.Certificate.PrivateKey.(ed25519.PrivateKey); !ok {
		t.Errorf("private key is %T, want ed25519.PrivateKey", identity.Certificate.PrivateKey)
	}
	if got := FingerprintOf(identity.Certificate.Leaf); got != identity.Fingerprint {
		t.Errorf("fingerprint of the certificate = %s, want %s", got, identity.Fingerprint)
	}
}

func TestCodeIsSixteenCharactersInFourGroups(t *testing.T) {
	identity := mustLoadOrCreate(t, t.TempDir())
	format := regexp.MustCompile(`^[A-Z2-7]{4}(-[A-Z2-7]{4}){3}$`)

	if code := identity.Fingerprint.Code(); !format.MatchString(code) {
		t.Errorf("Code = %q, want four groups of four base32 characters", code)
	}
}

func TestCodeDependsOnlyOnTheStartOfTheFingerprint(t *testing.T) {
	var first, second Fingerprint
	first[0], second[0] = 0xAB, 0xAB
	second[codeBytes] = 0xFF

	if first.Code() != second.Code() {
		t.Errorf("codes differ: %s and %s", first.Code(), second.Code())
	}
	second[codeBytes-1] = 0x01
	if first.Code() == second.Code() {
		t.Error("a change inside the first bytes did not change the code")
	}
}

func TestFingerprintTextRoundTrip(t *testing.T) {
	identity := mustLoadOrCreate(t, t.TempDir())

	parsed, err := ParseFingerprint(identity.Fingerprint.String())
	if err != nil {
		t.Fatalf("ParseFingerprint: %v", err)
	}
	if parsed != identity.Fingerprint {
		t.Errorf("round trip = %s, want %s", parsed, identity.Fingerprint)
	}
}

func TestInvalidFingerprintTextIsRejected(t *testing.T) {
	for _, text := range []string{"", "abcd", strings.Repeat("z", 64), strings.Repeat("a", 66)} {
		if _, err := ParseFingerprint(text); err == nil {
			t.Errorf("ParseFingerprint(%q) succeeded, want an error", text)
		}
	}
}
