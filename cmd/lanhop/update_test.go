//go:build unix

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
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
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type releaseServer struct {
	mutex     sync.Mutex
	files     map[string][]byte
	latest    string
	archives  atomic.Int32
	hangs     bool
	signature ed25519.PrivateKey
}

func (r *releaseServer) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if r.hangs {
		time.Sleep(2 * time.Second)
	}
	if request.URL.Path == "/releases/latest" {
		http.Redirect(response, request, "/releases/tag/"+r.latest, http.StatusFound)
		return
	}
	r.mutex.Lock()
	content, found := r.files[request.URL.Path]
	r.mutex.Unlock()
	if !found {
		http.NotFound(response, request)
		return
	}
	if strings.HasSuffix(request.URL.Path, ".tar.gz") {
		r.archives.Add(1)
	}
	response.Write(content)
}

func archiveRoot() string {
	return fmt.Sprintf("lanhop-%s-%s", runtime.GOOS, runtime.GOARCH)
}

func programSaying(version string) string {
	return "#!/bin/sh\necho 'lanhop " + version + "'\n"
}

func (r *releaseServer) publish(t *testing.T, version, from string) {
	t.Helper()
	var archive bytes.Buffer
	zipped := gzip.NewWriter(&archive)
	entries := tar.NewWriter(zipped)
	name := archiveRoot() + "/lanhop"
	if runtime.GOOS == "darwin" {
		name = archiveRoot() + "/lanhop.app/Contents/MacOS/lanhop"
	}
	content := programSaying(version)
	if err := entries.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	entries.Write([]byte(content))
	entries.Close()
	zipped.Close()
	updateFile := []byte("version " + version + "\nfrom " + from + "\n")
	sums := fmt.Sprintf("%x  %s.tar.gz\n%x  UPDATE\n", sha256.Sum256(archive.Bytes()), archiveRoot(), sha256.Sum256(updateFile))
	at := "/releases/download/" + version + "/"
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.files[at+archiveRoot()+".tar.gz"] = archive.Bytes()
	r.files[at+"UPDATE"] = updateFile
	r.files[at+"SHA256SUMS"] = []byte(sums)
	r.files[at+"SHA256SUMS.sig"] = ed25519.Sign(r.signature, []byte(sums))
}

type installedMachine struct {
	home     string
	program  string
	commands string
	config   string
}

func servingReleases(t *testing.T) *releaseServer {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	served := &releaseServer{files: map[string][]byte{}, signature: private}
	server := httptest.NewServer(served)
	t.Cleanup(server.Close)
	keys, address, check, patience := projectReleaseKeys, releasesAddress, checkNewApp, answerPatience
	projectReleaseKeys = []ed25519.PublicKey{public}
	releasesAddress = server.URL + "/releases"
	checkNewApp = func(string) error { return nil }
	t.Cleanup(func() {
		projectReleaseKeys, releasesAddress, checkNewApp, answerPatience = keys, address, check, patience
	})
	return served
}

func machineWithLanhop(t *testing.T, version string) installedMachine {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	machine := installedMachine{home: home, commands: filepath.Join(home, "commands"), config: useConfigDirectory(t)}
	fakes := filepath.Join(home, "fakes")
	for _, command := range []string{"launchctl", "systemctl"} {
		writeExecutable(t, filepath.Join(fakes, command), "#!/bin/sh\necho \"$(basename \"$0\") $*\" >>'"+machine.commands+"'\n")
	}
	t.Setenv("PATH", fakes+string(os.PathListSeparator)+os.Getenv("PATH"))
	if runtime.GOOS == "darwin" {
		machine.program = filepath.Join(home, "Applications", "lanhop.app", "Contents", "MacOS", "lanhop")
		writeExecutable(t, machine.program, programSaying(version))
		agent := "<plist><dict><key>ProgramArguments</key>\n<array>\n<string>" + machine.program + "</string>\n<string>receive</string>\n</array></dict></plist>\n"
		writeExecutable(t, filepath.Join(home, "Library", "LaunchAgents", agentLabel+".plist"), agent)
		return machine
	}
	machine.program = filepath.Join(home, ".local", "bin", "lanhop")
	writeExecutable(t, machine.program, programSaying(version))
	writeExecutable(t, filepath.Join(home, ".config", "systemd", "user", unitName), "[Service]\nExecStart=\""+machine.program+"\" receive\nRestart=on-failure\n")
	return machine
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func (m installedMachine) says(t *testing.T) string {
	t.Helper()
	said, err := exec.Command(m.program, "version").Output()
	if err != nil {
		t.Fatalf("the installed lanhop does not run: %v", err)
	}
	return strings.TrimSpace(string(said))
}

func (m installedMachine) restarts() []string {
	content, err := os.ReadFile(m.commands)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(content)), "\n")
}

func restartCommand() string {
	if runtime.GOOS == "darwin" {
		return fmt.Sprintf("launchctl kickstart -k gui/%d/%s", os.Getuid(), agentLabel)
	}
	return "systemctl --user restart " + unitName
}

func TestUpdateMovesThisMachineToTheLatestReleaseAndRestartsItsService(t *testing.T) {
	releases := servingReleases(t)
	releases.latest = "v0.1.3"
	releases.publish(t, "v0.1.3", "v0.1.2")
	machine := machineWithLanhop(t, "v0.1.2")
	output := &screen{}

	if err := updateThisMachine(nil, output); err != nil {
		t.Fatalf("lanhop update: %v", err)
	}
	if said := machine.says(t); said != "lanhop v0.1.3" {
		t.Errorf("the installed lanhop says %q, want lanhop v0.1.3", said)
	}
	if !strings.Contains(output.String(), "lanhop v0.1.2 → v0.1.3") || !strings.Contains(output.String(), "restarted on the new version") {
		t.Errorf("lanhop update printed %q", output.String())
	}
	if got := machine.restarts(); len(got) != 1 || got[0] != restartCommand() {
		t.Errorf("the service was restarted with %q, want %q once", got, restartCommand())
	}
}

func TestUpdateOnTheLatestReleaseChangesNothing(t *testing.T) {
	releases := servingReleases(t)
	releases.latest = "v0.1.2"
	machine := machineWithLanhop(t, "v0.1.2")
	output := &screen{}

	if err := updateThisMachine(nil, output); err != nil {
		t.Fatalf("lanhop update: %v", err)
	}
	if !strings.Contains(output.String(), "Nothing changed") || machine.says(t) != "lanhop v0.1.2" || len(machine.restarts()) != 0 {
		t.Errorf("lanhop update on the latest release printed %q, left %q and restarted %q", output.String(), machine.says(t), machine.restarts())
	}
	if releases.archives.Load() != 0 {
		t.Errorf("lanhop update downloaded an archive although nothing was to change")
	}
}

func TestUpdateRefusesWhatItCannotOrMustNotReplace(t *testing.T) {
	for _, test := range []struct {
		name      string
		installed string
		args      []string
		publish   [2]string
		noKeys    bool
		mentions  string
	}{
		{"a build of the source", "dev", nil, [2]string{"v0.1.3", "v0.1.2"}, false, "a build of the source"},
		{"an older release without --older", "v0.1.2", []string{"--version", "v0.1.1"}, [2]string{"v0.1.1", "v0.1.1"}, false, "--older"},
		{"a release that needs the installer", "v0.1.2", nil, [2]string{"v0.1.3", "v0.1.3"}, false, "installer line"},
		{"a build without a release key", "v0.1.2", nil, [2]string{"v0.1.3", "v0.1.2"}, true, "no release key"},
	} {
		t.Run(test.name, func(t *testing.T) {
			releases := servingReleases(t)
			releases.latest = test.publish[0]
			releases.publish(t, test.publish[0], test.publish[1])
			if test.noKeys {
				projectReleaseKeys = nil
			}
			machine := machineWithLanhop(t, test.installed)
			err := updateThisMachine(test.args, &screen{})
			if err == nil || !strings.Contains(err.Error(), test.mentions) {
				t.Fatalf("lanhop update = %v, want it to mention %q", err, test.mentions)
			}
			if said := machine.says(t); said != "lanhop "+test.installed {
				t.Errorf("the installed lanhop says %q, want it untouched", said)
			}
			if len(machine.restarts()) != 0 {
				t.Errorf("the service was restarted after a refusal: %q", machine.restarts())
			}
		})
	}
	var usage usageError
	if err := updateThisMachine([]string{"--older"}, &screen{}); !errors.As(err, &usage) {
		t.Errorf("lanhop update --older without --version = %v, want a usage error", err)
	}
}

func TestOlderMovesBackAndStopsFollowingTheCentral(t *testing.T) {
	releases := servingReleases(t)
	releases.publish(t, "v0.1.1", "v0.1.1")
	machine := machineWithLanhop(t, "v0.1.2")
	output := &screen{}

	if err := updateThisMachine([]string{"--version", "v0.1.1", "--older"}, output); err != nil {
		t.Fatalf("lanhop update --older: %v", err)
	}
	if said := machine.says(t); said != "lanhop v0.1.1" {
		t.Errorf("the installed lanhop says %q, want lanhop v0.1.1", said)
	}
	if switched, err := os.ReadFile(filepath.Join(machine.config, updatesFileName)); err != nil || strings.TrimSpace(string(switched)) != "off" {
		t.Errorf("the updates switch holds %q, %v, want off", switched, err)
	}
	if !strings.Contains(output.String(), "no longer follows") {
		t.Errorf("lanhop update --older printed %q, want it to say that following is off", output.String())
	}
}

func TestTwoUpdatesAtTheSameTimeUpdateOnce(t *testing.T) {
	releases := servingReleases(t)
	releases.latest = "v0.1.3"
	releases.publish(t, "v0.1.3", "v0.1.2")
	machine := machineWithLanhop(t, "v0.1.2")

	results := make(chan error, 2)
	for range 2 {
		go func() { results <- updateThisMachine(nil, &screen{}) }()
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Errorf("lanhop update: %v", err)
		}
	}
	if said := machine.says(t); said != "lanhop v0.1.3" {
		t.Errorf("the installed lanhop says %q, want lanhop v0.1.3", said)
	}
	if got := releases.archives.Load(); got != 1 {
		t.Errorf("the archive was downloaded %d times, want once", got)
	}
	if got := machine.restarts(); len(got) != 1 {
		t.Errorf("the service was restarted %d times, want once: %q", len(got), got)
	}
}

func TestUpdateFindsTheProgramThroughTheServiceWhenItIsReachedByALink(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("on macOS the launch agent names the program inside lanhop.app")
	}
	releases := servingReleases(t)
	releases.latest = "v0.1.3"
	releases.publish(t, "v0.1.3", "v0.1.2")
	machine := machineWithLanhop(t, "v0.1.2")
	real := filepath.Join(machine.home, "tools", "lanhop")
	writeExecutable(t, real, programSaying("v0.1.2"))
	os.Remove(machine.program)
	if err := os.Symlink(real, machine.program); err != nil {
		t.Fatal(err)
	}

	if err := updateThisMachine(nil, &screen{}); err != nil {
		t.Fatalf("lanhop update: %v", err)
	}
	if said := machine.says(t); said != "lanhop v0.1.3" {
		t.Errorf("the link reaches a lanhop that says %q, want lanhop v0.1.3", said)
	}
	if target, err := os.Readlink(machine.program); err != nil || target != real {
		t.Errorf("the link points to %q, %v, want it unchanged", target, err)
	}
}

func TestUpdateGivesUpWhenGitHubDoesNotAnswer(t *testing.T) {
	releases := servingReleases(t)
	releases.latest = "v0.1.3"
	releases.hangs = true
	answerPatience = 200 * time.Millisecond
	machine := machineWithLanhop(t, "v0.1.2")
	started := time.Now()
	if err := updateThisMachine(nil, &screen{}); err == nil {
		t.Fatalf("lanhop update succeeded although GitHub did not answer")
	}
	if took := time.Since(started); took > 1500*time.Millisecond {
		t.Errorf("lanhop update waited %v, want it to give up after its patience", took)
	}
	if said := machine.says(t); said != "lanhop v0.1.2" {
		t.Errorf("the installed lanhop says %q, want it untouched", said)
	}
}

func TestUpdateRefusesAProgramOutsideALanhopApp(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("only macOS keeps lanhop inside lanhop.app")
	}
	releases := servingReleases(t)
	releases.latest = "v0.1.3"
	releases.publish(t, "v0.1.3", "v0.1.2")
	machine := machineWithLanhop(t, "v0.1.2")
	loose := filepath.Join(machine.home, "bin", "lanhop")
	writeExecutable(t, loose, programSaying("v0.1.2"))
	agent := "<plist><dict><key>ProgramArguments</key>\n<array>\n<string>" + loose + "</string>\n</array></dict></plist>\n"
	writeExecutable(t, filepath.Join(machine.home, "Library", "LaunchAgents", agentLabel+".plist"), agent)

	err := updateThisMachine(nil, &screen{})
	if err == nil || !strings.Contains(err.Error(), "not inside a lanhop.app") {
		t.Fatalf("lanhop update of a program outside lanhop.app = %v, want a refusal", err)
	}
	if releases.archives.Load() != 0 {
		t.Errorf("lanhop update downloaded an archive for a program it cannot replace")
	}
}
