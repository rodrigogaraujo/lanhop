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
	"net"
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

	"github.com/rodrigogaraujo/lanhop/internal/status"
	"github.com/rodrigogaraujo/lanhop/internal/update"
)

type releaseServer struct {
	mutex        sync.Mutex
	files        map[string][]byte
	latest       string
	archives     atomic.Int32
	hangs        bool
	signature    ed25519.PrivateKey
	archiveAsked chan struct{}
	holdArchive  chan struct{}
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
		if r.holdArchive != nil {
			r.archiveAsked <- struct{}{}
			<-r.holdArchive
		}
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

func requireTheSwapOfApps(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "darwin" && !update.SwapsApps {
		t.Skip("this build of lanhop cannot swap lanhop.app, which needs cgo")
	}
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
		fake := "#!/bin/sh\nif [ \"$2\" = is-active ]; then\n  [ ! -e '" + filepath.Join(home, "inactive") + "' ]\n  exit\nfi\necho \"$(basename \"$0\") $*\" >>'" + machine.commands + "'\n"
		writeExecutable(t, filepath.Join(fakes, command), fake)
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
	requireTheSwapOfApps(t)
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
	requireTheSwapOfApps(t)
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
	requireTheSwapOfApps(t)
	releases := servingReleases(t)
	releases.latest = "v0.1.3"
	releases.publish(t, "v0.1.3", "v0.1.2")
	machine := machineWithLanhop(t, "v0.1.2")

	releases.archiveAsked = make(chan struct{}, 2)
	releases.holdArchive = make(chan struct{})
	results := make(chan error, 2)
	go func() { results <- updateThisMachine(nil, &screen{}) }()
	<-releases.archiveAsked
	go func() { results <- updateThisMachine(nil, &screen{}) }()
	select {
	case <-releases.archiveAsked:
		t.Errorf("the second update asked for the archive while the first one was still downloading it")
	case <-time.After(300 * time.Millisecond):
	}
	close(releases.holdArchive)
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
	requireTheSwapOfApps(t)
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
	err := updateThisMachine(nil, &screen{})
	var timeout net.Error
	if !errors.As(err, &timeout) || !timeout.Timeout() {
		t.Fatalf("lanhop update with GitHub silent = %v, want a timeout", err)
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

func TestUpdateLeavesAServiceThatIsNotRunningAlone(t *testing.T) {
	requireTheSwapOfApps(t)
	if runtime.GOOS == "darwin" {
		t.Skip("launchctl kickstart fails by itself on an agent that is not loaded")
	}
	releases := servingReleases(t)
	releases.latest = "v0.1.3"
	releases.publish(t, "v0.1.3", "v0.1.2")
	machine := machineWithLanhop(t, "v0.1.2")
	writeExecutable(t, filepath.Join(machine.home, "inactive"), "")
	output := &screen{}

	if err := updateThisMachine(nil, output); err != nil {
		t.Fatalf("lanhop update: %v", err)
	}
	if got := machine.restarts(); len(got) != 0 {
		t.Errorf("a service that was not running was restarted: %q", got)
	}
	if !strings.Contains(output.String(), "Start lanhop again") {
		t.Errorf("lanhop update printed %q, want it to say to start lanhop again", output.String())
	}
}

func TestALatestReleaseOlderThanThisOneNamesTheWayBack(t *testing.T) {
	releases := servingReleases(t)
	releases.latest = "v0.1.1"
	releases.publish(t, "v0.1.1", "v0.1.1")
	machine := machineWithLanhop(t, "v0.1.2")
	err := updateThisMachine(nil, &screen{})
	if err == nil || !strings.Contains(err.Error(), "lanhop update --version v0.1.1 --older") {
		t.Fatalf("lanhop update with an older latest = %v, want the command that moves back", err)
	}
	if said := machine.says(t); said != "lanhop v0.1.2" {
		t.Errorf("the installed lanhop says %q, want it untouched", said)
	}
}

func TestAFailedMoveBackKeepsFollowingTheCentral(t *testing.T) {
	releases := servingReleases(t)
	releases.publish(t, "v0.1.1", "v0.1.3")
	machine := machineWithLanhop(t, "v0.1.2")
	if err := updateThisMachine([]string{"--version", "v0.1.1", "--older"}, &screen{}); err == nil {
		t.Fatalf("lanhop update --older succeeded with a release that needs the installer")
	}
	if _, err := os.Stat(filepath.Join(machine.config, updatesFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the updates switch was written after a failed move back: %v", err)
	}
}

func TestUpdateRefusesAProgramThatIsNotNamedLanhop(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("on macOS the program is checked through lanhop.app")
	}
	releases := servingReleases(t)
	releases.latest = "v0.1.3"
	releases.publish(t, "v0.1.3", "v0.1.2")
	machine := machineWithLanhop(t, "v0.1.2")
	wrapper := filepath.Join(machine.home, "bin", "start-lanhop")
	writeExecutable(t, wrapper, programSaying("v0.1.2"))
	writeExecutable(t, filepath.Join(machine.home, ".config", "systemd", "user", unitName), "[Service]\nExecStart=\""+wrapper+"\" receive\n")

	err := updateThisMachine(nil, &screen{})
	if err == nil || !strings.Contains(err.Error(), "not named lanhop") {
		t.Fatalf("lanhop update of %s = %v, want a refusal", wrapper, err)
	}
}

func TestUpdateSaysWhenTheServiceStartsAProgramThatIsGone(t *testing.T) {
	releases := servingReleases(t)
	releases.latest = "v0.1.3"
	machine := machineWithLanhop(t, "v0.1.2")
	os.Remove(machine.program)
	err := updateThisMachine(nil, &screen{})
	if err == nil || !strings.Contains(err.Error(), "not there any more") {
		t.Fatalf("lanhop update with the program gone = %v, want it to say so", err)
	}
}

func centralBefore(t *testing.T, seats ...status.Seat) statusFile {
	t.Helper()
	names := make([]string, len(seats))
	for i, seat := range seats {
		names[i] = seat.Name
	}
	record := openStatus()
	record.set(status.Report{State: status.Sharing, Machines: names, Seats: seats})
	return record
}

func quickDesk(t *testing.T, patience time.Duration) {
	t.Helper()
	before, interval := deskPatience, deskPollInterval
	deskPatience, deskPollInterval = patience, 20*time.Millisecond
	t.Cleanup(func() { deskPatience, deskPollInterval = before, interval })
}

func TestUpdateOnTheCentralWaitsForEachSideMachineAndNamesTheOthers(t *testing.T) {
	requireTheSwapOfApps(t)
	releases := servingReleases(t)
	releases.latest = "v0.1.3"
	releases.publish(t, "v0.1.3", "v0.1.2")
	machine := machineWithLanhop(t, "v0.1.2")
	quickDesk(t, 3*time.Second)
	record := centralBefore(t,
		status.Seat{Name: "office-mac", Version: "v0.1.2", Mark: "follows"},
		status.Seat{Name: "old", Mark: "needs the installer"},
		status.Seat{Name: "desk", Version: "v0.1.2", Mark: "does not follow"},
	)
	go func() {
		for len(machine.restarts()) == 0 {
			time.Sleep(10 * time.Millisecond)
		}
		record.set(status.Report{State: status.Waiting})
		time.Sleep(50 * time.Millisecond)
		record.set(status.Report{State: status.Sharing, Machines: []string{"office-mac"}, Seats: []status.Seat{{Name: "office-mac", Version: "v0.1.2", Mark: "follows"}}})
		time.Sleep(50 * time.Millisecond)
		record.set(status.Report{State: status.Sharing, Machines: []string{"office-mac"}, Seats: []status.Seat{{Name: "office-mac", Version: "v0.1.3", Mark: "follows"}}})
	}()
	output := &screen{}

	if err := updateThisMachine(nil, output); err != nil {
		t.Fatalf("lanhop update: %v", err)
	}
	for _, part := range []string{"office-mac v0.1.2 → v0.1.3", "old cannot update by itself", "LANHOP_ROLE=receive", "desk does not follow this machine"} {
		if !strings.Contains(output.String(), part) {
			t.Errorf("lanhop update printed %q, want it to say %q", output.String(), part)
		}
	}
}

func TestUpdateOnTheCentralStopsWaitingAndNamesWhoDidNotComeBack(t *testing.T) {
	requireTheSwapOfApps(t)
	releases := servingReleases(t)
	releases.latest = "v0.1.3"
	releases.publish(t, "v0.1.3", "v0.1.2")
	machineWithLanhop(t, "v0.1.2")
	quickDesk(t, 300*time.Millisecond)
	centralBefore(t, status.Seat{Name: "office-mac", Version: "v0.1.2", Mark: "follows"})
	output := &screen{}

	started := time.Now()
	if err := updateThisMachine(nil, output); err != nil {
		t.Fatalf("lanhop update: %v", err)
	}
	if !strings.Contains(output.String(), "office-mac did not come back on v0.1.3") {
		t.Errorf("lanhop update printed %q, want it to name office-mac", output.String())
	}
	if took := time.Since(started); took > 2*time.Second {
		t.Errorf("lanhop update waited %v, want it to stop at its patience", took)
	}
}

func TestUpdateOnASideMachineDoesNotWaitForADesk(t *testing.T) {
	requireTheSwapOfApps(t)
	releases := servingReleases(t)
	releases.latest = "v0.1.3"
	releases.publish(t, "v0.1.3", "v0.1.2")
	machineWithLanhop(t, "v0.1.2")
	quickDesk(t, 5*time.Second)
	openStatus().set(status.Report{State: status.Connected, Machines: []string{"imac"}, Address: centralAddress, Seats: []status.Seat{{Name: "office-mac", Version: "v0.1.2", Mark: "follows"}}})
	output := &screen{}
	started := time.Now()
	if err := updateThisMachine(nil, output); err != nil {
		t.Fatalf("lanhop update: %v", err)
	}
	if strings.Contains(output.String(), "Waiting up to") || time.Since(started) > 2*time.Second {
		t.Errorf("lanhop update on a side machine waited for a desk: %q", output.String())
	}
}

func TestUpdateDoesNotWaitWhenTheServiceDidNotRestart(t *testing.T) {
	requireTheSwapOfApps(t)
	if runtime.GOOS == "darwin" {
		t.Skip("launchctl kickstart decides alone whether the agent restarts")
	}
	releases := servingReleases(t)
	releases.latest = "v0.1.3"
	releases.publish(t, "v0.1.3", "v0.1.2")
	machine := machineWithLanhop(t, "v0.1.2")
	writeExecutable(t, filepath.Join(machine.home, "inactive"), "")
	quickDesk(t, 5*time.Second)
	centralBefore(t, status.Seat{Name: "office-mac", Version: "v0.1.2", Mark: "follows"}, status.Seat{Name: "old", Mark: "needs the installer"})
	output := &screen{}
	started := time.Now()
	if err := updateThisMachine(nil, output); err != nil {
		t.Fatalf("lanhop update: %v", err)
	}
	if strings.Contains(output.String(), "Waiting up to") || strings.Contains(output.String(), "did not come back") || time.Since(started) > 2*time.Second {
		t.Errorf("lanhop update waited for side machines although the service did not restart: %q", output.String())
	}
	if !strings.Contains(output.String(), "old cannot update by itself") {
		t.Errorf("lanhop update printed %q, want it to still name the machine that needs the installer", output.String())
	}
}

func TestMovingBackOnTheCentralWaitsForNobody(t *testing.T) {
	requireTheSwapOfApps(t)
	releases := servingReleases(t)
	releases.publish(t, "v0.1.1", "v0.1.1")
	machineWithLanhop(t, "v0.1.2")
	quickDesk(t, 5*time.Second)
	centralBefore(t, status.Seat{Name: "office-mac", Version: "v0.1.2", Mark: "follows"})
	output := &screen{}
	started := time.Now()
	if err := updateThisMachine([]string{"--version", "v0.1.1", "--older"}, output); err != nil {
		t.Fatalf("lanhop update --older: %v", err)
	}
	if strings.Contains(output.String(), "Waiting up to") || time.Since(started) > 2*time.Second {
		t.Errorf("lanhop update --older waited for a side machine the central never moves down: %q", output.String())
	}
}

func TestAWaitingMachineThatNowNeedsTheInstallerIsNamedSo(t *testing.T) {
	requireTheSwapOfApps(t)
	releases := servingReleases(t)
	releases.latest = "v0.1.3"
	releases.publish(t, "v0.1.3", "v0.1.2")
	machine := machineWithLanhop(t, "v0.1.2")
	quickDesk(t, 3*time.Second)
	record := centralBefore(t, status.Seat{Name: "office-mac", Version: "v0.1.2", Mark: "follows"})
	go func() {
		for len(machine.restarts()) == 0 {
			time.Sleep(10 * time.Millisecond)
		}
		record.set(status.Report{State: status.Sharing, Machines: []string{"office-mac"}, Seats: []status.Seat{{Name: "office-mac", Version: "v0.1.2", Mark: "needs the installer"}}})
	}()
	output := &screen{}
	if err := updateThisMachine(nil, output); err != nil {
		t.Fatalf("lanhop update: %v", err)
	}
	if !strings.Contains(output.String(), "office-mac cannot update by itself") || strings.Contains(output.String(), "did not come back") {
		t.Errorf("lanhop update printed %q, want office-mac named as needing the installer", output.String())
	}
}

func TestTheReleaseBuildSetsTheVariablesThatHoldTheVersions(t *testing.T) {
	_, thisFile, _, found := runtime.Caller(0)
	if !found {
		t.Fatal("the test does not know where its source file is")
	}
	workflow, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), "..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for name, variable := range map[string]*string{"main.version": &version, "main.updateFrom": &updateFrom} {
		if !strings.Contains(string(workflow), "-X "+name+"=") {
			t.Errorf("the release workflow does not set %s, which holds %q in this build", name, *variable)
		}
	}
}
