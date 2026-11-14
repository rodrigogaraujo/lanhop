//go:build darwin && cgo

package update

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func appSignedAdHoc(t *testing.T, identifier string) string {
	t.Helper()
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(program)
	if err != nil {
		t.Fatal(err)
	}
	plist, err := os.ReadFile("../../packaging/macos/Info.plist")
	if err != nil {
		t.Fatal(err)
	}
	app := filepath.Join(t.TempDir(), "lanhop.app")
	if err := os.MkdirAll(filepath.Join(app, "Contents", "MacOS"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "Contents", "MacOS", "lanhop"), content, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), bytes.ReplaceAll(plist, []byte("@VERSION@"), []byte("0.0.1")), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("codesign", "--force", "--sign", "-", "--identifier", identifier, app).CombinedOutput(); err != nil {
		t.Fatalf("codesign: %v\n%s", err, out)
	}
	return app
}

func TestAnAppSignedAdHocIsNotTaken(t *testing.T) {
	check, err := AppCheck(AppIdentifier, []string{currentLeaf})
	if err != nil {
		t.Fatal(err)
	}
	if err := check(appSignedAdHoc(t, AppIdentifier)); !errors.Is(err, ErrAppNotOurs) {
		t.Fatalf("check of an app signed ad hoc = %v, want ErrAppNotOurs", err)
	}
}

func TestAMissingAppIsNotTaken(t *testing.T) {
	check, err := AppCheck(AppIdentifier, []string{currentLeaf})
	if err != nil {
		t.Fatal(err)
	}
	if err := check(filepath.Join(t.TempDir(), "lanhop.app")); err == nil {
		t.Fatal("check of a missing app took it")
	}
}

func TestTheSecurityFrameworkChecksTheRequirement(t *testing.T) {
	apps := os.Getenv("LANHOP_TEST_SIGNED_APPS")
	if apps == "" {
		t.Skip("LANHOP_TEST_SIGNED_APPS names no folder of apps signed with throwaway certificates; ci.yml makes one")
	}
	leaf, err := os.ReadFile(filepath.Join(apps, "project.leaf"))
	if err != nil {
		t.Fatal(err)
	}
	otherLeaf, err := os.ReadFile(filepath.Join(apps, "other.leaf"))
	if err != nil {
		t.Fatal(err)
	}
	kept := projectCertificateLeaves
	t.Cleanup(func() { projectCertificateLeaves = kept })

	for _, leaves := range [][]string{
		{strings.TrimSpace(string(leaf))},
		{strings.TrimSpace(string(otherLeaf)), strings.TrimSpace(string(leaf))},
	} {
		projectCertificateLeaves = leaves
		check := ProjectAppCheck()
		if err := check(filepath.Join(apps, "project.app")); err != nil {
			t.Errorf("with the hashes %q, the app signed with the certificate of the project was refused: %v", leaves, err)
		}
	}

	projectCertificateLeaves = []string{strings.TrimSpace(string(leaf))}
	check := ProjectAppCheck()
	for _, refused := range []string{"other.app", "ad-hoc.app", "other-identifier.app"} {
		if err := check(filepath.Join(apps, refused)); !errors.Is(err, ErrAppNotOurs) {
			t.Errorf("check of %s = %v, want ErrAppNotOurs", refused, err)
		}
	}
}
