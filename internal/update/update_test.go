package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var (
	running  = Version{0, 1, 2}
	newer    = Version{0, 1, 3}
	testKeys = newKeys()
	nextKeys = newKeys()
	unknown  = newKeys()
)

type keys struct {
	public  ed25519.PublicKey
	private ed25519.PrivateKey
}

func newKeys() keys {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	return keys{public, private}
}

type entry struct {
	name     string
	content  string
	mode     int64
	typeflag byte
	linkname string
}

type publication struct {
	version          Version
	says             Version
	from             Version
	programSays      string
	entries          []entry
	signer           ed25519.PrivateKey
	unsigned         bool
	archiveChanged   bool
	checksumsChanged bool
	checksumsPadding int
	updatePadding    int
	signatureExtra   []byte
}

type fakeGitHub struct {
	files  map[string][]byte
	latest string
}

func (g *fakeGitHub) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request.URL.Path == "/releases/latest" {
		if g.latest == "" {
			http.NotFound(response, request)
			return
		}
		http.Redirect(response, request, "/releases/tag/"+g.latest, http.StatusFound)
		return
	}
	content, found := g.files[request.URL.Path]
	if !found {
		http.NotFound(response, request)
		return
	}
	response.Write(content)
}

func serving(t *testing.T) (*fakeGitHub, Source) {
	t.Helper()
	github := &fakeGitHub{files: map[string][]byte{}}
	server := httptest.NewServer(github)
	t.Cleanup(server.Close)
	source := Source{Releases: server.URL + "/releases", Client: server.Client(), Keys: []ed25519.PublicKey{testKeys.public, nextKeys.public}}
	if runtime.GOOS == "darwin" {
		source.CheckApp = func(string) error { return nil }
	}
	return github, source
}

func root() string {
	return fmt.Sprintf("lanhop-%s-%s", runtime.GOOS, runtime.GOARCH)
}

func script(says string) string {
	return "#!/bin/sh\necho '" + says + "'\n"
}

func programEntries(says string) []entry {
	if runtime.GOOS == "darwin" {
		return []entry{
			{name: root() + "/lanhop.app/Contents/Info.plist", content: "<plist/>\n", mode: 0o644},
			{name: root() + "/lanhop.app/Contents/MacOS/lanhop", content: script(says), mode: 0o755},
			{name: root() + "/LICENSE", content: "MIT\n", mode: 0o644},
		}
	}
	return []entry{
		{name: root() + "/lanhop", content: script(says), mode: 0o755},
		{name: root() + "/LICENSE", content: "MIT\n", mode: 0o644},
	}
}

func packed(t *testing.T, entries []entry) []byte {
	t.Helper()
	var archive bytes.Buffer
	zipped := gzip.NewWriter(&archive)
	writer := tar.NewWriter(zipped)
	for _, each := range entries {
		typeflag := each.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		header := &tar.Header{Name: each.name, Mode: each.mode, Typeflag: typeflag, Linkname: each.linkname}
		if typeflag == tar.TypeReg {
			header.Size = int64(len(each.content))
		}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if typeflag == tar.TypeReg {
			if _, err := writer.Write([]byte(each.content)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zipped.Close(); err != nil {
		t.Fatal(err)
	}
	return archive.Bytes()
}

func (g *fakeGitHub) publish(t *testing.T, release publication) {
	t.Helper()
	if release.says == (Version{}) {
		release.says = release.version
	}
	if release.from == (Version{}) {
		release.from = running
	}
	if release.programSays == "" {
		release.programSays = "lanhop " + release.version.String()
	}
	if release.entries == nil {
		release.entries = programEntries(release.programSays)
	}
	if release.signer == nil {
		release.signer = testKeys.private
	}
	archiveName := root() + ".tar.gz"
	archive := packed(t, release.entries)
	update := []byte(fmt.Sprintf("version %s\nfrom %s\n", release.says, release.from) + strings.Repeat("# padding\n", release.updatePadding))
	sums := fmt.Sprintf("%x  %s\n%x  %s\n", sha256.Sum256(archive), archiveName, sha256.Sum256(update), updateName)
	sums += strings.Repeat("0000000000000000000000000000000000000000000000000000000000000000  padding\n", release.checksumsPadding)
	signature := append(ed25519.Sign(release.signer, []byte(sums)), release.signatureExtra...)
	if release.archiveChanged {
		archive = append(archive, 0)
	}
	if release.checksumsChanged {
		sums += "# changed\n"
	}
	at := "/releases/download/" + release.version.String() + "/"
	g.files[at+archiveName] = archive
	g.files[at+updateName] = update
	g.files[at+checksumsName] = []byte(sums)
	if !release.unsigned {
		g.files[at+signatureName] = signature
	}
}

func installedProgram(t *testing.T) Target {
	t.Helper()
	directory := t.TempDir()
	target := Target{System: runtime.GOOS, Arch: runtime.GOARCH, Running: running}
	if runtime.GOOS == "darwin" {
		target.Program = filepath.Join(directory, "lanhop.app")
		writeProgram(t, filepath.Join(target.Program, "Contents", "MacOS", "lanhop"), script("lanhop "+running.String()))
		return target
	}
	target.Program = filepath.Join(directory, "lanhop")
	writeProgram(t, target.Program, script("lanhop "+running.String()))
	return target
}

func writeProgram(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func versionOf(t *testing.T, target Target) string {
	t.Helper()
	program := target.Program
	if target.isApp() {
		program = filepath.Join(program, "Contents", "MacOS", "lanhop")
	}
	said, err := exec.Command(program, "version").Output()
	if err != nil {
		t.Fatalf("the installed program does not run: %v", err)
	}
	return strings.TrimSpace(string(said))
}

func expectNoStagingLeft(t *testing.T, target Target) {
	t.Helper()
	left, err := filepath.Glob(filepath.Join(filepath.Dir(target.Program), stagingPattern))
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Errorf("staging folders were left: %v", left)
	}
}

func expectUntouched(t *testing.T, target Target) {
	t.Helper()
	program := target.Program
	if target.isApp() {
		program = filepath.Join(program, "Contents", "MacOS", "lanhop")
	}
	content, err := os.ReadFile(program)
	if err != nil || string(content) != script("lanhop "+running.String()) {
		t.Errorf("the installed program holds %q, %v, want it byte for byte as it was", content, err)
	}
	expectNoStagingLeft(t, target)
}

func TestARightReleaseReplacesTheProgramInOneStep(t *testing.T) {
	github, source := serving(t)
	github.publish(t, publication{version: newer})
	target := installedProgram(t)

	staged, err := source.Prepare(context.Background(), target, newer, false)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := Install(staged, target); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if said := versionOf(t, target); said != "lanhop v0.1.3" {
		t.Errorf("the installed program says %q, want lanhop v0.1.3", said)
	}
	expectNoStagingLeft(t, target)
}

func TestASignatureByTheNextKeyIsAccepted(t *testing.T) {
	github, source := serving(t)
	github.publish(t, publication{version: newer, signer: nextKeys.private})
	target := installedProgram(t)
	staged, err := source.Prepare(context.Background(), target, newer, false)
	if err != nil {
		t.Fatalf("Prepare with the next key: %v", err)
	}
	staged.Discard()
}

func TestEveryRefusalLeavesTheOldProgramAndNoFolder(t *testing.T) {
	escaping := func(name string) []entry {
		return append(programEntries("lanhop v0.1.3"), entry{name: name, content: "x", mode: 0o644})
	}
	for _, test := range []struct {
		name    string
		release publication
		want    Version
		refused error
	}{
		{"a changed archive", publication{version: newer, archiveChanged: true}, newer, ErrChanged},
		{"changed checksums", publication{version: newer, checksumsChanged: true}, newer, ErrNotSigned},
		{"no signature", publication{version: newer, unsigned: true}, newer, ErrUnexpectedReply},
		{"a signature by an unknown key", publication{version: newer, signer: unknown.private}, newer, ErrNotSigned},
		{"an older release offered as the new one", publication{version: newer, says: running}, newer, ErrOtherVersion},
		{"a release older than this lanhop", publication{version: Version{0, 1, 1}}, Version{0, 1, 1}, ErrNotNewer},
		{"the same release as this lanhop", publication{version: running}, running, ErrNotNewer},
		{"a release that needs the installer", publication{version: newer, from: newer}, newer, ErrNeedsInstaller},
		{"checksums over their size limit", publication{version: newer, checksumsPadding: 80}, newer, ErrTooLarge},
		{"an UPDATE over its size limit", publication{version: newer, updatePadding: 500}, newer, ErrTooLarge},
		{"a signature over its size", publication{version: newer, signatureExtra: []byte{0}}, newer, ErrTooLarge},
		{"a setgid bit", publication{version: newer, entries: append(programEntries("lanhop v0.1.3"), entry{name: root() + "/tool", content: "x", mode: 0o2755})}, newer, ErrUnsafeArchive},
		{"a path with ..", publication{version: newer, entries: escaping(root() + "/../evil")}, newer, ErrUnsafeArchive},
		{"an absolute path", publication{version: newer, entries: escaping("/tmp/evil")}, newer, ErrUnsafeArchive},
		{"a path outside the root", publication{version: newer, entries: escaping("elsewhere/evil")}, newer, ErrUnsafeArchive},
		{"a link", publication{version: newer, entries: append(programEntries("lanhop v0.1.3"), entry{name: root() + "/link", typeflag: tar.TypeSymlink, linkname: "/etc/passwd"})}, newer, ErrUnsafeArchive},
		{"a hard link", publication{version: newer, entries: append(programEntries("lanhop v0.1.3"), entry{name: root() + "/hard", typeflag: tar.TypeLink, linkname: root() + "/LICENSE"})}, newer, ErrUnsafeArchive},
		{"a special file", publication{version: newer, entries: append(programEntries("lanhop v0.1.3"), entry{name: root() + "/pipe", typeflag: tar.TypeFifo})}, newer, ErrUnsafeArchive},
		{"a setuid bit", publication{version: newer, entries: append(programEntries("lanhop v0.1.3"), entry{name: root() + "/tool", content: "x", mode: 0o4755})}, newer, ErrUnsafeArchive},
		{"a program that says another version", publication{version: newer, programSays: "lanhop v9.9.9"}, newer, ErrWrongProgram},
	} {
		t.Run(test.name, func(t *testing.T) {
			github, source := serving(t)
			github.publish(t, test.release)
			target := installedProgram(t)
			_, err := source.Prepare(context.Background(), target, test.want, false)
			if !errors.Is(err, test.refused) {
				t.Fatalf("Prepare = %v, want %v", err, test.refused)
			}
			expectUntouched(t, target)
		})
	}
}

func TestOlderIsAcceptedOnlyWhenAskedFor(t *testing.T) {
	github, source := serving(t)
	older := Version{0, 1, 1}
	github.publish(t, publication{version: older, from: older})
	target := installedProgram(t)
	staged, err := source.Prepare(context.Background(), target, older, true)
	if err != nil {
		t.Fatalf("Prepare with older allowed: %v", err)
	}
	if err := Install(staged, target); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if said := versionOf(t, target); said != "lanhop v0.1.1" {
		t.Errorf("the installed program says %q, want lanhop v0.1.1", said)
	}
}

func TestAFailedReplacementLeavesTheOldProgramAndNoFolder(t *testing.T) {
	github, source := serving(t)
	github.publish(t, publication{version: newer})
	target := installedProgram(t)
	failure := errors.New("the disk refused")
	renamed, swapped := renameFile, swapApp
	renameFile = func(string, string) error { return failure }
	swapApp = func(string, string) error { return failure }
	t.Cleanup(func() { renameFile, swapApp = renamed, swapped })

	staged, err := source.Prepare(context.Background(), target, newer, false)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := Install(staged, target); !errors.Is(err, failure) {
		t.Fatalf("Install = %v, want the failure", err)
	}
	expectUntouched(t, target)
}

func TestAStopBeforeTheSwapLeavesTheOldProgramAndTheNextStartCleansUp(t *testing.T) {
	github, source := serving(t)
	github.publish(t, publication{version: newer})
	target := installedProgram(t)
	if _, err := source.Prepare(context.Background(), target, newer, false); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if said := versionOf(t, target); said != "lanhop "+running.String() {
		t.Errorf("the program says %q before the swap, want the old one", said)
	}
	if err := CleanUp(target); err != nil {
		t.Fatalf("CleanUp: %v", err)
	}
	expectUntouched(t, target)
}

func TestAStopRightAfterTheSwapLeavesTheNewProgramAndTheNextStartCleansUp(t *testing.T) {
	github, source := serving(t)
	github.publish(t, publication{version: newer})
	target := installedProgram(t)
	staged, err := source.Prepare(context.Background(), target, newer, false)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := putInPlace(staged.replacement, target); err != nil {
		t.Fatalf("putInPlace: %v", err)
	}
	if said := versionOf(t, target); said != "lanhop v0.1.3" {
		t.Errorf("the program says %q right after the swap, want the new one", said)
	}
	if err := CleanUp(target); err != nil {
		t.Fatalf("CleanUp: %v", err)
	}
	expectNoStagingLeft(t, target)
}

func TestALatestReleaseOlderThanThisOneIsRefused(t *testing.T) {
	github, source := serving(t)
	older := Version{0, 1, 1}
	github.latest = older.String()
	github.publish(t, publication{version: older, from: older})
	target := installedProgram(t)
	latest, err := source.Latest(context.Background())
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if _, err := source.Prepare(context.Background(), target, latest, false); !errors.Is(err, ErrNotNewer) {
		t.Fatalf("Prepare of an older latest = %v, want ErrNotNewer", err)
	}
	expectUntouched(t, target)
}

func TestARedirectFromHTTPSToHTTPIsRefused(t *testing.T) {
	plain := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(plain.Close)
	secure := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, plain.URL+request.URL.Path, http.StatusFound)
	}))
	t.Cleanup(secure.Close)
	source := Source{Releases: secure.URL + "/releases", Client: secure.Client(), Keys: []ed25519.PublicKey{testKeys.public}}
	if _, err := source.fetch(context.Background(), newer, checksumsName, smallFileLimit); !errors.Is(err, ErrNotHTTPS) {
		t.Errorf("a download redirected to HTTP = %v, want ErrNotHTTPS", err)
	}
}

func TestAnAppIsRefusedWhenItsSignatureCannotBeChecked(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("lanhop.app is replaced only on macOS")
	}
	github, source := serving(t)
	source.CheckApp = nil
	github.publish(t, publication{version: newer})
	target := installedProgram(t)
	if _, err := source.Prepare(context.Background(), target, newer, false); !errors.Is(err, ErrNoAppCheck) {
		t.Fatalf("Prepare without a check of the app = %v, want ErrNoAppCheck", err)
	}
	expectUntouched(t, target)
}

func TestLatestIsTheReleaseThatGitHubSendsTo(t *testing.T) {
	github, source := serving(t)
	github.latest = "v0.1.3"
	if latest, err := source.Latest(context.Background()); err != nil || latest != newer {
		t.Errorf("Latest = %v, %v, want v0.1.3", latest, err)
	}
	github.latest = "nightly"
	if _, err := source.Latest(context.Background()); !errors.Is(err, ErrNoLatest) {
		t.Errorf("Latest with a tag that is not a release = %v, want ErrNoLatest", err)
	}
	github.latest = ""
	if _, err := source.Latest(context.Background()); !errors.Is(err, ErrNoLatest) {
		t.Errorf("Latest without a redirect = %v, want ErrNoLatest", err)
	}
}

func TestOnlyReleaseVersionsAreRead(t *testing.T) {
	for _, text := range []string{"v0.1.2", "v1.255.255", "v32767.0.0"} {
		if _, err := ParseVersion(text); err != nil {
			t.Errorf("ParseVersion(%q) = %v, want a version", text, err)
		}
	}
	for _, text := range []string{"0.1.2", "v1.2", "v1.2.256", "v1.256.0", "v32768.0.0", "dev", "dev-1234abc", "v1.2.3-rc1", "v0.01.3", "v00.1.2", ""} {
		if _, err := ParseVersion(text); !errors.Is(err, ErrNotARelease) {
			t.Errorf("ParseVersion(%q) = %v, want ErrNotARelease", text, err)
		}
	}
	if got := newer.Value(); got != 259 {
		t.Errorf("v0.1.3 is worth %d on the wire, want 259", got)
	}
	if newer.Compare(running) <= 0 || running.Compare(newer) >= 0 || newer.Compare(newer) != 0 {
		t.Errorf("Compare does not order v0.1.2 before v0.1.3")
	}
}
