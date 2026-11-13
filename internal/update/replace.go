package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

const (
	stagingPattern = "lanhop-update-*"
	unpackedLimit  = 200 << 20
	setIDBits      = 0o6000
)

var (
	ErrNotNewer       = errors.New("update: the release is not newer than this lanhop")
	ErrNeedsInstaller = errors.New("update: this release needs the installer")
	ErrUnsafeArchive  = errors.New("update: the archive holds something lanhop does not unpack")
	ErrWrongProgram   = errors.New("update: the new program does not say the version of the release")
	ErrSwapUnknown    = errors.New("update: this system cannot swap the app in one step")
)

var (
	renameFile = os.Rename
	swapApp    = swapInPlace
)

type Target struct {
	Program string
	System  string
	Arch    string
	Running Version
}

type Staged struct {
	folder      string
	replacement string
	Version     Version
}

func (t Target) archiveRoot() string {
	return fmt.Sprintf("lanhop-%s-%s", t.System, t.Arch)
}

func (t Target) isApp() bool {
	return t.System == "darwin"
}

func (s Source) Prepare(ctx context.Context, target Target, want Version, older bool) (Staged, error) {
	if !older && want.Compare(target.Running) <= 0 {
		return Staged{}, fmt.Errorf("%w: %s, and this one is %s", ErrNotNewer, want, target.Running)
	}
	signed, err := s.signedRelease(ctx, want)
	if err != nil {
		return Staged{}, err
	}
	if signed.from.Compare(target.Running) > 0 {
		return Staged{}, fmt.Errorf("%w: it updates from %s on, and this one is %s", ErrNeedsInstaller, signed.from, target.Running)
	}
	archiveName := target.archiveRoot() + ".tar.gz"
	archive, err := s.fetch(ctx, want, archiveName, archiveLimit)
	if err != nil {
		return Staged{}, err
	}
	if err := matches(signed.checksums, archiveName, archive); err != nil {
		return Staged{}, err
	}
	folder, err := os.MkdirTemp(filepath.Dir(target.Program), stagingPattern)
	if err != nil {
		return Staged{}, err
	}
	staged := Staged{folder: folder, replacement: filepath.Join(folder, target.archiveRoot(), filepath.Base(target.Program)), Version: want}
	if err := staged.check(ctx, s, archive, target); err != nil {
		staged.Discard()
		return Staged{}, err
	}
	return staged, nil
}

func (st Staged) check(ctx context.Context, s Source, archive []byte, target Target) error {
	if err := unpack(archive, st.folder, target.archiveRoot()); err != nil {
		return err
	}
	if _, err := os.Lstat(st.replacement); err != nil {
		return fmt.Errorf("%w: it has no %s", ErrUnsafeArchive, filepath.Base(target.Program))
	}
	program := st.replacement
	if target.isApp() {
		if s.CheckApp != nil {
			if err := s.CheckApp(st.replacement); err != nil {
				return err
			}
		}
		program = filepath.Join(st.replacement, "Contents", "MacOS", "lanhop")
	}
	said, err := exec.CommandContext(ctx, program, "version").Output()
	if err != nil {
		return fmt.Errorf("%w: it did not run: %w", ErrWrongProgram, err)
	}
	if strings.TrimSpace(string(said)) != "lanhop "+st.Version.String() {
		return fmt.Errorf("%w: it says %q", ErrWrongProgram, strings.TrimSpace(string(said)))
	}
	return nil
}

func (st Staged) Discard() error {
	return os.RemoveAll(st.folder)
}

func Install(staged Staged, target Target) error {
	if err := putInPlace(staged.replacement, target); err != nil {
		staged.Discard()
		return err
	}
	return staged.Discard()
}

func putInPlace(replacement string, target Target) error {
	if target.isApp() {
		return swapApp(replacement, target.Program)
	}
	return renameFile(replacement, target.Program)
}

func CleanUp(target Target) error {
	leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(target.Program), stagingPattern))
	if err != nil {
		return err
	}
	var failed error
	for _, leftover := range leftovers {
		failed = errors.Join(failed, os.RemoveAll(leftover))
	}
	return failed
}

func unpack(archive []byte, into, root string) error {
	unzipped, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnsafeArchive, err)
	}
	entries := tar.NewReader(unzipped)
	var written int64
	for {
		header, err := entries.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%w: %w", ErrUnsafeArchive, err)
		}
		name, err := insideRoot(header.Name, root)
		if err != nil {
			return err
		}
		if header.Mode&setIDBits != 0 {
			return fmt.Errorf("%w: %s sets a user or group on run", ErrUnsafeArchive, header.Name)
		}
		destination := filepath.Join(into, filepath.FromSlash(name))
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(destination, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			written += header.Size
			if written > unpackedLimit {
				return fmt.Errorf("%w: what it unpacks to", ErrTooLarge)
			}
			if err := writeEntry(destination, entries, header); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%w: %s is not a plain file or folder", ErrUnsafeArchive, header.Name)
		}
	}
}

func insideRoot(name, root string) (string, error) {
	cleaned := path.Clean(name)
	if path.IsAbs(name) || cleaned == ".." || strings.HasPrefix(cleaned, "../") || (cleaned != root && !strings.HasPrefix(cleaned, root+"/")) {
		return "", fmt.Errorf("%w: %s is outside %s", ErrUnsafeArchive, name, root)
	}
	return cleaned, nil
}

func writeEntry(destination string, content io.Reader, header *tar.Header) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, os.FileMode(header.Mode)&0o755)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnsafeArchive, err)
	}
	if _, err := io.CopyN(file, content, header.Size); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}
