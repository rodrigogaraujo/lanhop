//go:build darwin && cgo

package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io/fs"
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
	if err := check(throughAnArchive(t, filepath.Join(apps, "project.app"))); err != nil {
		t.Errorf("the app signed with the certificate of the project was refused after it went through an archive: %v", err)
	}
	for _, refused := range []string{"other.app", "ad-hoc.app", "other-identifier.app", "tampered.app"} {
		if err := check(filepath.Join(apps, refused)); !errors.Is(err, ErrAppNotOurs) {
			t.Errorf("check of %s = %v, want ErrAppNotOurs", refused, err)
		}
		if err := check(throughAnArchive(t, filepath.Join(apps, refused))); !errors.Is(err, ErrAppNotOurs) {
			t.Errorf("check of %s after it went through an archive = %v, want ErrAppNotOurs", refused, err)
		}
	}
}

func throughAnArchive(t *testing.T, app string) string {
	t.Helper()
	const root = "lanhop-darwin-test"
	var packed bytes.Buffer
	zipped := gzip.NewWriter(&packed)
	archive := tar.NewWriter(zipped)
	err := filepath.WalkDir(app, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(app, path)
		if err != nil {
			return err
		}
		header.Name = root + "/lanhop.app"
		if relative != "." {
			header.Name += "/" + filepath.ToSlash(relative)
		}
		if entry.IsDir() {
			header.Name += "/"
		}
		if err := archive.WriteHeader(header); err != nil {
			return err
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		_, err = archive.Write(content)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zipped.Close(); err != nil {
		t.Fatal(err)
	}
	into := t.TempDir()
	if err := unpack(packed.Bytes(), into, root); err != nil {
		t.Fatal(err)
	}
	unpacked := filepath.Join(into, root, "lanhop.app")
	if _, err := os.Stat(filepath.Join(unpacked, "Contents", "MacOS", "lanhop")); err != nil {
		t.Fatalf("the app did not come out of the archive whole: %v", err)
	}
	return unpacked
}
