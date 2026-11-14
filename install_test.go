//go:build unix

package lanhop_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	installer     = "install.sh"
	latestRelease = "latest/download"
	wouldRun      = "would run: "

	binaryFile     = ".local/bin/lanhop"
	unitFile       = ".config/systemd/user/lanhop.service"
	ruleRecordFile = ".local/state/lanhop/udev-rule-installed"
	agentFile      = "Library/LaunchAgents/com.github.rodrigogaraujo.lanhop.plist"
	agentLogFile   = "Library/Logs/lanhop.log"
	linuxPeersFile = ".config/lanhop/peers.json"
	macPeersFile   = "Library/Application Support/lanhop/peers.json"
	appDirectory   = "Applications/lanhop.app"
	appProgram     = appDirectory + "/Contents/MacOS/lanhop"

	usageWithTheEditor    = "lanhop layout --edit   arrange the machines on the desk by dragging them"
	usageWithoutTheEditor = "lanhop layout          show where the layout file is"
	usageWithTheStatus    = "lanhop status          say what lanhop is doing on this machine"
	usageWithTheJoin      = "lanhop join ADDRESS CODE"
	usageWithTheUpdate    = "lanhop update          move this machine to the latest release of lanhop, and restart its service"

	joinTheInputGroup   = "sudo usermod -aG input ana"
	writeTheUdevRule    = `printf '%s\n' 'KERNEL=="uinput", SUBSYSTEM=="misc", GROUP="input", MODE="0660", OPTIONS+="static_node=uinput"' | sudo tee {root}/etc/udev/rules.d/60-lanhop.rules`
	loadUinputAtBoot    = `printf '%s\n' 'uinput' | sudo tee {root}/etc/modules-load.d/lanhop.conf`
	loadUinputNow       = "sudo modprobe uinput"
	reloadTheUdevRules  = "sudo udevadm control --reload-rules"
	applyTheUdevRules   = "sudo udevadm trigger"
	reloadTheUnits      = "systemctl --user daemon-reload"
	enableTheUnit       = "systemctl --user enable lanhop.service"
	restartTheUnit      = "systemctl --user restart lanhop.service"
	disableTheUnit      = "systemctl --user disable --now lanhop.service"
	deleteTheUdevRule   = "sudo rm -f {root}/etc/udev/rules.d/60-lanhop.rules {root}/etc/modules-load.d/lanhop.conf"
	openAccessibility   = "open x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility"
	openLocalNetwork    = "open x-apple.systempreferences:com.apple.preference.security?Privacy_LocalNetwork"
	addToTheFirewall    = "sudo {root}/usr/libexec/ApplicationFirewall/socketfilterfw --add {root}/Applications/lanhop.app"
	unblockInFirewall   = "sudo {root}/usr/libexec/ApplicationFirewall/socketfilterfw --unblockapp {root}/Applications/lanhop.app"
	stopTheAgent        = "launchctl bootout gui/501/com.github.rodrigogaraujo.lanhop"
	startTheAgent       = "launchctl bootstrap gui/501 {home}/" + agentFile
	restartTheAgent     = "launchctl kickstart -k gui/501/com.github.rodrigogaraujo.lanhop"
	notInTheInputGroup  = "ana staff"
	alreadyInInputGroup = "ana input staff"
)

const fakeLanhop = `#!/bin/sh
printf '%%s [configuration %%s]\n' "$*" "${LANHOP_CONFIG_DIR-default}" >>'%[1]s'
case "$1" in
version) echo 'lanhop %[2]s' ;;
help) echo '%[3]s' ;;
layout) echo 'The layout file is /desk/layout' ;;
pair)
  read -r typed
  printf 'typed %%s\n' "$typed" >>'%[1]s'
  ;;
join)
  if [ -e '%[4]s.refuses' ]; then
    echo 'lanhop: 10.0.0.5 is not waiting for a machine to join' >&2
    exit 1
  fi
  if [ -e '%[4]s.joined' ]; then
    echo 'This machine already trusts "iMac", the central of this join code.'
  else
    : >'%[4]s.joined'
    echo 'Joined "iMac" at 10.0.0.5:24745.'
  fi
  ;;
status)
  if [ -s '%[4]s' ]; then
    head -n 1 '%[4]s'
    if [ "$(wc -l <'%[4]s')" -gt 1 ]; then
      tail -n +2 '%[4]s' >'%[4]s.next'
      mv '%[4]s.next' '%[4]s'
    fi
  else
    echo 'stopped: lanhop is not running here.'
  fi
  ;;
esac
`

const fakeLaunchctlThatFailsTheFirstBootstrap = `#!/bin/sh
echo "launchctl $*" >>'%[1]s'
if [ "$1" = bootstrap ] && [ ! -e '%[2]s' ]; then
  : >'%[2]s'
  echo 'Bootstrap failed: 5: Input/output error' >&2
  exit 5
fi
`

const fakeSleep = `#!/bin/sh
now=$(cat '%[1]s' 2>/dev/null || echo 1000)
echo $((now + ${1:-1})) >'%[1]s'
`

const fakeDate = `#!/bin/sh
if [ "$1" = +%%s ]; then
  cat '%[1]s' 2>/dev/null || echo 1000
else
  exec /bin/date "$@"
fi
`

const fakeFirewall = `#!/bin/sh
case "$1" in
--getglobalstate) echo 'Firewall is %[1]s. (State = %[2]d)' ;;
--getblockall) echo 'Firewall has block all state set to %[3]s.' ;;
esac
`

const fakeID = `#!/bin/sh
case "$*" in
-un) echo ana ;;
-u) echo 501 ;;
-Gn) echo '%[1]s' ;;
'-Gn ana') echo '%[1]s' ;;
*)
  echo "id was not expected to be called with $*" >&2
  exit 1
  ;;
esac
`

const fakeSystemCommand = `#!/bin/sh
echo "$(basename "$0") $*" >>'%[1]s'
exit %[2]d
`

const fakeSudo = `#!/bin/sh
exec "$@"
`

const unitOfTheService = `[Unit]
Description=lanhop %[2]s
Documentation=https://github.com/rodrigogaraujo/lanhop

[Service]
ExecStart="%[1]s" %[2]s
Environment=LANHOP_SERVICE=1
Restart=on-failure
RestartSec=10

[Install]
WantedBy=default.target
`

const agentOfTheService = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.github.rodrigogaraujo.lanhop</string>
%[4]s  <key>ProgramArguments</key>
  <array>
    <string>%[1]s</string>
    <string>%[2]s</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <dict>
    <key>SuccessfulExit</key>
    <false/>
  </dict>
  <key>EnvironmentVariables</key>
  <dict>
    <key>LANHOP_SERVICE</key>
    <string>1</string>
  </dict>
  <key>ProcessType</key>
  <string>Interactive</string>
  <key>StandardOutPath</key>
  <string>%[3]s</string>
  <key>StandardErrorPath</key>
  <string>%[3]s</string>
</dict>
</plist>
`

const appAssociation = `  <key>AssociatedBundleIdentifiers</key>
  <array>
    <string>com.github.rodrigogaraujo.lanhop</string>
  </array>
`

const infoOfTheApp = `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
  <key>CFBundleIdentifier</key>
  <string>com.github.rodrigogaraujo.lanhop</string>
</dict>
</plist>
`

var builds = []string{"linux-amd64", "linux-arm64", "darwin-arm64", "darwin-amd64"}

type releases struct {
	t             *testing.T
	root          string
	url           string
	calls         string
	statuses      string
	usage         string
	withoutTheApp bool
}

func newReleases(t *testing.T) *releases {
	t.Helper()
	root := t.TempDir()
	served := httptest.NewServer(http.FileServer(http.Dir(root)))
	t.Cleanup(served.Close)
	published := &releases{
		t:        t,
		root:     root,
		url:      served.URL,
		calls:    filepath.Join(t.TempDir(), "calls"),
		statuses: filepath.Join(t.TempDir(), "statuses"),
		usage:    usageWithTheEditor,
	}
	published.publish(latestRelease, "1.0.0")
	return published
}

func (r *releases) publish(directory, version string) {
	r.t.Helper()
	var sums strings.Builder
	for _, build := range builds {
		name := "lanhop-" + build
		program := fmt.Sprintf(fakeLanhop, r.calls, version+" "+build, r.usage, r.statuses)
		files := []archived{{name + "/lanhop", program, 0o700}}
		if strings.HasPrefix(build, "darwin-") && !r.withoutTheApp {
			files = []archived{
				{name + "/lanhop.app/Contents/Info.plist", infoOfTheApp, 0o644},
				{name + "/lanhop.app/Contents/MacOS/lanhop", program, 0o700},
			}
		}
		files = append(files, archived{name + "/LICENSE", "MIT License\n", 0o644}, archived{name + "/README.md", "# lanhop\n", 0o644})
		archive := packed(r.t, files...)
		r.write(filepath.Join(directory, name+".tar.gz"), archive)
		fmt.Fprintf(&sums, "%x  %s.tar.gz\n", sha256.Sum256(archive), name)
	}
	r.write(filepath.Join(directory, "SHA256SUMS"), []byte(sums.String()))
}

func (r *releases) withTheStatus() *releases {
	r.t.Helper()
	r.usage = usageWithTheEditor + "\n" + usageWithTheStatus
	r.publish(latestRelease, "1.0.0")
	return r
}

func (r *releases) thatAsksAgainByItself() *releases {
	r.t.Helper()
	r.usage = usageWithTheEditor + "\n" + usageWithTheStatus + "\nlanhop ended to ask macOS again for the Accessibility permission"
	r.publish(latestRelease, "1.0.0")
	return r
}

func (r *releases) withTheJoin() *releases {
	r.t.Helper()
	r.usage += "\n" + usageWithTheJoin
	r.publish(latestRelease, "1.0.0")
	return r
}

func (r *releases) refusesTheJoin() {
	r.t.Helper()
	writeFile(r.t, r.statuses+".refuses", nil, 0o644)
}

func (r *releases) statusSays(lines ...string) {
	r.t.Helper()
	writeFile(r.t, r.statuses, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}

func (r *releases) write(name string, content []byte) {
	r.t.Helper()
	writeFile(r.t, filepath.Join(r.root, name), content, 0o644)
}

func (r *releases) damage(name string) {
	r.t.Helper()
	path := filepath.Join(r.root, name)
	content, err := os.ReadFile(path)
	if err != nil {
		r.t.Fatal(err)
	}
	writeFile(r.t, path, append(content, 0), 0o644)
}

func (r *releases) lanhopStarts(call string) bool {
	r.t.Helper()
	for patience := 10 * time.Second; patience > 0; patience -= 10 * time.Millisecond {
		if slices.Contains(r.lanhopWasRunWith(), call) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func (r *releases) lanhopWasRunWith() []string {
	r.t.Helper()
	content, err := os.ReadFile(r.calls)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		r.t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
}

type archived struct {
	name    string
	content string
	mode    int64
}

func packed(t *testing.T, contents ...archived) []byte {
	t.Helper()
	var archive bytes.Buffer
	zipped := gzip.NewWriter(&archive)
	files := tar.NewWriter(zipped)
	for _, file := range contents {
		header := &tar.Header{Name: file.name, Mode: file.mode, Size: int64(len(file.content)), Typeflag: tar.TypeReg}
		if err := files.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := files.Write([]byte(file.content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := errors.Join(files.Close(), zipped.Close()); err != nil {
		t.Fatal(err)
	}
	return archive.Bytes()
}

func writeFile(t *testing.T, path string, content []byte, mode fs.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, mode); err != nil {
		t.Fatal(err)
	}
}

type machine struct {
	t        *testing.T
	shell    string
	home     string
	root     string
	aside    string
	scratch  string
	onPath   []string
	settings map[string]string
}

func newMachine(t *testing.T, shell, system, architecture string, from *releases) *machine {
	t.Helper()
	m := &machine{t: t, shell: shell, home: t.TempDir(), root: t.TempDir(), aside: t.TempDir(), scratch: t.TempDir()}
	m.settings = map[string]string{
		"LANHOP_RELEASES_URL":   from.url,
		"LANHOP_SYSTEM":         system,
		"LANHOP_MACHINE":        architecture,
		"LANHOP_SYSTEM_ROOT":    m.root,
		"LANHOP_PRINT_COMMANDS": "1",
		"LANHOP_TERMINAL":       filepath.Join(m.aside, "no terminal"),
		"SHELL":                 "/bin/sh",
	}
	m.withGroups(notInTheInputGroup)
	clock := filepath.Join(m.aside, "clock")
	writeFile(t, filepath.Join(m.aside, "bin", "sleep"), []byte(fmt.Sprintf(fakeSleep, clock)), 0o755)
	writeFile(t, filepath.Join(m.aside, "bin", "date"), []byte(fmt.Sprintf(fakeDate, clock)), 0o755)
	if err := os.MkdirAll(m.inRoot("Applications"), 0o755); err != nil {
		t.Fatal(err)
	}
	return m
}

func (m *machine) inRoot(name string) string {
	return filepath.Join(m.root, filepath.FromSlash(name))
}

func (m *machine) linkTarget(name string) string {
	m.t.Helper()
	target, err := os.Readlink(m.path(name))
	if err != nil {
		m.t.Fatalf("%s is not a link: %v", name, err)
	}
	return target
}

func (m *machine) withGroups(groups string) {
	m.t.Helper()
	writeFile(m.t, filepath.Join(m.aside, "bin", "id"), []byte(fmt.Sprintf(fakeID, groups)), 0o755)
}

func (m *machine) withFirewall(state string, number int, blockAll string) {
	m.t.Helper()
	writeFile(m.t, m.inRoot("usr/libexec/ApplicationFirewall/socketfilterfw"), []byte(fmt.Sprintf(fakeFirewall, state, number, blockAll)), 0o755)
}

func (m *machine) withRealSystemCommands() {
	m.t.Helper()
	delete(m.settings, "LANHOP_PRINT_COMMANDS")
	for _, command := range []string{"usermod", "modprobe", "udevadm", "systemctl", "launchctl", "open"} {
		m.systemCommandEndsWith(command, 0)
	}
	writeFile(m.t, filepath.Join(m.aside, "bin", "sudo"), []byte(fakeSudo), 0o755)
	for _, directory := range []string{"etc/udev/rules.d", "etc/modules-load.d"} {
		if err := os.MkdirAll(filepath.Join(m.root, directory), 0o755); err != nil {
			m.t.Fatal(err)
		}
	}
}

func (m *machine) systemCommandEndsWith(command string, code int) {
	m.t.Helper()
	fake := fmt.Sprintf(fakeSystemCommand, filepath.Join(m.aside, "system commands"), code)
	writeFile(m.t, filepath.Join(m.aside, "bin", command), []byte(fake), 0o755)
}

func (m *machine) systemCommandsRun() []string {
	m.t.Helper()
	content, err := os.ReadFile(filepath.Join(m.aside, "system commands"))
	if errors.Is(err, fs.ErrNotExist) {
		return []string{}
	}
	if err != nil {
		m.t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
}

func (m *machine) systemFiles() map[string]string {
	m.t.Helper()
	found := map[string]string{}
	err := filepath.WalkDir(m.root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		relative, err := filepath.Rel(m.root, path)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(path)
		found[filepath.ToSlash(relative)] = string(content)
		return err
	})
	if err != nil {
		m.t.Fatal(err)
	}
	return found
}

func (m *machine) typing(lines ...string) {
	m.t.Helper()
	terminal := filepath.Join(m.aside, "terminal")
	writeFile(m.t, terminal, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
	m.settings["LANHOP_TERMINAL"] = terminal
}

func (m *machine) terminalThatWaitsForTyping() *os.File {
	m.t.Helper()
	path := filepath.Join(m.aside, "terminal that waits")
	if output, err := exec.Command("mkfifo", path).CombinedOutput(); err != nil {
		m.t.Fatalf("mkfifo: %v\n%s", err, output)
	}
	terminal, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		m.t.Fatal(err)
	}
	m.t.Cleanup(func() { terminal.Close() })
	m.settings["LANHOP_TERMINAL"] = path
	return terminal
}

func (m *machine) installerPipedIntoTheShell(arguments ...string) *exec.Cmd {
	m.t.Helper()
	script, err := os.ReadFile(installer)
	if err != nil {
		m.t.Fatal(err)
	}
	command := exec.Command(m.shell, append([]string{"-s", "--"}, arguments...)...)
	command.Stdin = bytes.NewReader(script)
	path := append([]string{filepath.Join(m.aside, "bin")}, m.onPath...)
	path = append(path, os.Getenv("PATH"))
	command.Env = []string{"HOME=" + m.home, "TMPDIR=" + m.scratch, "PATH=" + strings.Join(path, string(os.PathListSeparator))}
	for name, value := range m.settings {
		command.Env = append(command.Env, name+"="+value)
	}
	return command
}

func (m *machine) run(arguments ...string) (string, int) {
	m.t.Helper()
	output, err := m.installerPipedIntoTheShell(arguments...).CombinedOutput()
	var failed *exec.ExitError
	switch {
	case err == nil:
		return string(output), 0
	case errors.As(err, &failed):
		return string(output), failed.ExitCode()
	}
	m.t.Fatalf("%s did not run the installer: %v", m.shell, err)
	return "", 0
}

func (m *machine) mustRun(arguments ...string) string {
	m.t.Helper()
	output, code := m.run(arguments...)
	if code != 0 {
		m.t.Fatalf("exit code = %d, want 0\n%s", code, output)
	}
	return output
}

func (m *machine) path(name string) string {
	return filepath.Join(m.home, filepath.FromSlash(name))
}

func (m *machine) read(name string) string {
	m.t.Helper()
	content, err := os.ReadFile(m.path(name))
	if err != nil {
		m.t.Fatal(err)
	}
	return string(content)
}

func (m *machine) files() []string {
	m.t.Helper()
	found := []string{}
	err := filepath.WalkDir(m.home, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		relative, err := filepath.Rel(m.home, path)
		found = append(found, filepath.ToSlash(relative))
		return err
	})
	if err != nil {
		m.t.Fatal(err)
	}
	return found
}

func (m *machine) installedVersion() string {
	m.t.Helper()
	output, err := exec.Command(m.path(binaryFile), "version").Output()
	if err != nil {
		m.t.Fatalf("the installed lanhop does not run: %v", err)
	}
	return strings.TrimSpace(string(output))
}

func (m *machine) named(names ...string) []string {
	described := make([]string, len(names))
	for i, name := range names {
		described[i] = strings.NewReplacer("{home}", m.home, "{root}", m.root).Replace(name)
	}
	return described
}

func commandsIn(output string) []string {
	commands := []string{}
	for _, line := range strings.Split(output, "\n") {
		if _, command, found := strings.Cut(line, wouldRun); found {
			commands = append(commands, command)
		}
	}
	return commands
}

func configured(calls ...string) []string {
	described := make([]string, len(calls))
	for i, call := range calls {
		described[i] = call + " [configuration default]"
	}
	return described
}

func inEveryShell(t *testing.T, behaviour func(t *testing.T, shell string)) {
	t.Helper()
	for _, shell := range []string{"sh", "dash", "bash"} {
		if _, err := exec.LookPath(shell); err != nil && shell != "sh" {
			continue
		}
		t.Run(shell, func(t *testing.T) { behaviour(t, shell) })
	}
}

func shownToTheUser(commands ...string) string {
	return "    " + strings.Join(commands, "\n    ") + "\n\n"
}

func expectSame(t *testing.T, what string, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("%s:\n got  %q\n want %q", what, got, want)
	}
}

func expectMention(t *testing.T, output string, mentions ...string) {
	t.Helper()
	for _, mention := range mentions {
		if !strings.Contains(output, mention) {
			t.Errorf("the output does not say %q:\n%s", mention, output)
		}
	}
}

func expectNoQuestion(t *testing.T, output string) {
	t.Helper()
	for _, question := range []string{"[y/N]", "[y/n]", "Press Enter", "type its address"} {
		if strings.Contains(output, question) {
			t.Errorf("the output asks %q although nobody can answer:\n%s", question, output)
		}
	}
}

func TestEveryShellParsesTheInstaller(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		if output, err := exec.Command(shell, "-n", installer).CombinedOutput(); err != nil {
			t.Errorf("%s -n %s: %v\n%s", shell, installer, err, output)
		}
	})
}

func TestInstallsTheBuildOfThisSystemAndArchitecture(t *testing.T) {
	machines := []struct {
		system, architecture, build string
	}{
		{"Linux", "x86_64", "linux-amd64"},
		{"Linux", "amd64", "linux-amd64"},
		{"Linux", "aarch64", "linux-arm64"},
		{"Darwin", "arm64", "darwin-arm64"},
		{"Darwin", "x86_64", "darwin-amd64"},
	}
	inEveryShell(t, func(t *testing.T, shell string) {
		for _, found := range machines {
			t.Run(found.system+" on "+found.architecture, func(t *testing.T) {
				m := newMachine(t, shell, found.system, found.architecture, newReleases(t))
				output := m.mustRun()
				if got, want := m.installedVersion(), "lanhop 1.0.0 "+found.build; got != want {
					t.Errorf("installed %q, want %q", got, want)
				}
				info, err := os.Stat(m.path(binaryFile))
				if err != nil {
					t.Fatal(err)
				}
				if info.Mode().Perm() != 0o755 {
					t.Errorf("mode of the binary = %o, want 755", info.Mode().Perm())
				}
				if found.system == "Darwin" {
					expectMention(t, output, "Installed lanhop 1.0.0 "+found.build+" in "+m.inRoot(appDirectory)+"\n", "The lanhop command in "+m.path(binaryFile)+" runs it.")
					if target := m.linkTarget(binaryFile); target != m.inRoot(appProgram) {
						t.Errorf("%s links to %s, want the program in the app, %s", binaryFile, target, m.inRoot(appProgram))
					}
				} else {
					expectMention(t, output, "Installed lanhop 1.0.0 "+found.build+" in "+m.path(binaryFile))
				}
			})
		}
	})
}

func TestWithoutATerminalOnlyTheBinaryIsInstalledAndNothingRuns(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		for _, system := range []string{"Linux", "Darwin"} {
			t.Run(system, func(t *testing.T) {
				published := newReleases(t)
				m := newMachine(t, shell, system, "arm64", published)
				output := m.mustRun()
				expectSame(t, "files in the home directory", m.files(), []string{binaryFile})
				expectSame(t, "system commands", commandsIn(output), []string{})
				expectSame(t, "runs of lanhop", published.lanhopWasRunWith(), configured("version"))
				expectMention(t, output, "nothing else was changed on this machine", "LANHOP_ROLE=share", "LANHOP_YES=1")
				expectNoQuestion(t, output)
			})
		}
	})
}

func TestSetsUpTheRoleThatWasAnswered(t *testing.T) {
	setups := []struct {
		name     string
		system   string
		groups   string
		settings map[string]string
		typed    []string
		commands []string
		files    []string
		lanhop   []string
	}{
		{
			name:     "Linux with the keyboard shares, joins the input group and enables the unit for the next login",
			system:   "Linux",
			typed:    []string{"y", "y", "y", "y", "yes", "y"},
			commands: []string{joinTheInputGroup, reloadTheUnits, enableTheUnit},
			files:    []string{unitFile, binaryFile},
			lanhop:   append(configured("version", "pair"), append([]string{"typed yes"}, configured("help", "layout --edit")...)...),
		},
		{
			name:     "Linux without the keyboard receives and also gets the udev rule",
			system:   "Linux",
			typed:    []string{"n", "y", "y", "y", "192.168.0.7", "yes"},
			commands: []string{joinTheInputGroup, writeTheUdevRule, loadUinputAtBoot, loadUinputNow, reloadTheUdevRules, applyTheUdevRules, reloadTheUnits, enableTheUnit},
			files:    []string{unitFile, binaryFile, ruleRecordFile},
			lanhop:   append(configured("version", "pair 192.168.0.7"), "typed yes"),
		},
		{
			name:     "Linux already in the input group is not asked to join it and starts at once",
			system:   "Linux",
			groups:   alreadyInInputGroup,
			typed:    []string{"y", "y", "n", "n"},
			commands: []string{reloadTheUnits, enableTheUnit, restartTheUnit},
			files:    []string{unitFile, binaryFile},
			lanhop:   configured("version", "help"),
		},
		{
			name:     "Linux where every step is refused keeps the unit off and runs nothing",
			system:   "Linux",
			typed:    []string{"n", "n", "n", "n", ""},
			commands: []string{},
			files:    []string{unitFile, binaryFile},
			lanhop:   configured("version"),
		},
		{
			name:     "Linux with an unclear answer about the keyboard gets the binary and nothing else",
			system:   "Linux",
			typed:    []string{"maybe", "y", "y", "y", "y"},
			commands: []string{},
			files:    []string{binaryFile},
			lanhop:   configured("version"),
		},
		{
			name:     "Linux with Enter alone about the keyboard gets the binary and nothing else",
			system:   "Linux",
			typed:    []string{"", "y", "y", "y", "y"},
			commands: []string{},
			files:    []string{binaryFile},
			lanhop:   configured("version"),
		},
		{
			name:     "Linux with only the role given, and nobody to ask, writes the unit and runs nothing",
			system:   "Linux",
			settings: map[string]string{"LANHOP_ROLE": "receive"},
			commands: []string{},
			files:    []string{unitFile, binaryFile},
			lanhop:   configured("version"),
		},
		{
			name:     "Linux unattended agrees to every step and pairs with nobody",
			system:   "Linux",
			settings: map[string]string{"LANHOP_ROLE": "receive", "LANHOP_YES": "1"},
			typed:    []string{"n", "n", "n", "n"},
			commands: []string{joinTheInputGroup, writeTheUdevRule, loadUinputAtBoot, loadUinputNow, reloadTheUdevRules, applyTheUdevRules, reloadTheUnits, enableTheUnit},
			files:    []string{unitFile, binaryFile, ruleRecordFile},
			lanhop:   configured("version"),
		},
		{
			name:     "macOS with the keyboard shares from a launch agent that starts after the permission",
			system:   "Darwin",
			typed:    []string{"y", "y", "", "y", "yes", "y"},
			commands: []string{openAccessibility, stopTheAgent, startTheAgent},
			files:    []string{binaryFile, agentFile},
			lanhop:   append(configured("version", "pair"), append([]string{"typed yes"}, configured("help", "layout --edit")...)...),
		},
		{
			name:     "macOS without the keyboard receives from a launch agent",
			system:   "Darwin",
			typed:    []string{"n", "y", "", "10.0.0.5", "yes"},
			commands: []string{openAccessibility, stopTheAgent, startTheAgent},
			files:    []string{binaryFile, agentFile},
			lanhop:   append(configured("version", "pair 10.0.0.5"), "typed yes"),
		},
		{
			name:     "macOS where the launch agent is refused writes no file that would start lanhop at login",
			system:   "Darwin",
			typed:    []string{"y", "n", "n", "n"},
			commands: []string{},
			files:    []string{binaryFile},
			lanhop:   configured("version", "help"),
		},
		{
			name:     "macOS with only the role given, and nobody to ask, writes no launch agent",
			system:   "Darwin",
			settings: map[string]string{"LANHOP_ROLE": "share"},
			commands: []string{},
			files:    []string{binaryFile},
			lanhop:   configured("version", "help"),
		},
		{
			name:     "macOS unattended installs and starts the launch agent without waiting for anybody",
			system:   "Darwin",
			settings: map[string]string{"LANHOP_ROLE": "share", "LANHOP_YES": "1"},
			typed:    []string{"n", "n", "n", "n"},
			commands: []string{openAccessibility, stopTheAgent, startTheAgent},
			files:    []string{binaryFile, agentFile},
			lanhop:   configured("version", "help"),
		},
	}
	inEveryShell(t, func(t *testing.T, shell string) {
		for _, setup := range setups {
			t.Run(setup.name, func(t *testing.T) {
				published := newReleases(t)
				m := newMachine(t, shell, setup.system, "x86_64", published)
				for name, value := range setup.settings {
					m.settings[name] = value
				}
				if setup.groups != "" {
					m.withGroups(setup.groups)
				}
				if setup.typed != nil {
					m.typing(setup.typed...)
				}
				output := m.mustRun()
				expectSame(t, "system commands", commandsIn(output), m.named(setup.commands...))
				expectSame(t, "files in the home directory", m.files(), setup.files)
				expectSame(t, "runs of lanhop", published.lanhopWasRunWith(), setup.lanhop)
				if setup.typed == nil || setup.settings["LANHOP_YES"] == "1" {
					expectNoQuestion(t, output)
				}
				if strings.Contains(output, "lanhop installer:") {
					t.Errorf("the installer reports a problem:\n%s", output)
				}
			})
		}
	})
}

func TestOnlyAClearYesAgreesToAStep(t *testing.T) {
	answers := []struct {
		typed  string
		agrees bool
	}{
		{"y", true},
		{"Y", true},
		{"yes", true},
		{"Yes", true},
		{"YES", true},
		{"", false},
		{"n", false},
		{"no", false},
		{"maybe", false},
		{"yes please", false},
	}
	inEveryShell(t, func(t *testing.T, shell string) {
		for _, answer := range answers {
			t.Run(fmt.Sprintf("typing %q", answer.typed), func(t *testing.T) {
				m := newMachine(t, shell, "Linux", "x86_64", newReleases(t))
				m.settings["LANHOP_ROLE"] = "share"
				m.typing(answer.typed)
				want := []string{}
				if answer.agrees {
					want = []string{joinTheInputGroup}
				}
				expectSame(t, "system commands", commandsIn(m.mustRun()), want)
			})
		}
	})
}

func TestShowsTheCommandsOfAStepBeforeAskingToRunThem(t *testing.T) {
	steps := []struct {
		name     string
		role     string
		groups   string
		commands []string
		question string
	}{
		{
			name:     "joining the input group",
			role:     "share",
			groups:   notInTheInputGroup,
			commands: []string{joinTheInputGroup},
			question: "Run it now? [y/N] ",
		},
		{
			name:     "the udev rule",
			role:     "receive",
			groups:   notInTheInputGroup,
			commands: []string{writeTheUdevRule, loadUinputAtBoot, loadUinputNow, reloadTheUdevRules, applyTheUdevRules},
			question: "Run them now? [y/N] ",
		},
		{
			name:     "enabling the unit for the next login",
			role:     "share",
			groups:   notInTheInputGroup,
			commands: []string{reloadTheUnits, enableTheUnit},
			question: "Run them now, so that lanhop starts every time you log in? [y/N] ",
		},
		{
			name:     "enabling the unit and starting it at once",
			role:     "share",
			groups:   alreadyInInputGroup,
			commands: []string{reloadTheUnits, enableTheUnit, restartTheUnit},
			question: "Run them now, so that lanhop starts every time you log in? [y/N] ",
		},
	}
	inEveryShell(t, func(t *testing.T, shell string) {
		for _, step := range steps {
			t.Run(step.name, func(t *testing.T) {
				m := newMachine(t, shell, "Linux", "x86_64", newReleases(t))
				m.settings["LANHOP_ROLE"] = step.role
				m.withGroups(step.groups)
				m.typing("n")
				output := m.mustRun()
				expectMention(t, output, shownToTheUser(m.named(step.commands...)...)+step.question)
				expectSame(t, "system commands", commandsIn(output), []string{})
			})
		}
	})
}

func TestCtrlCWhileLanhopPairWaitsStopsLanhopAndNotTheInstaller(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		published := newReleases(t)
		m := newMachine(t, shell, "Linux", "x86_64", published)
		terminal := m.terminalThatWaitsForTyping()
		if _, err := terminal.WriteString("y\nn\nn\ny\n"); err != nil {
			t.Fatal(err)
		}
		var output bytes.Buffer
		installing := m.installerPipedIntoTheShell()
		installing.Stdout = &output
		installing.Stderr = &output
		installing.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := installing.Start(); err != nil {
			t.Fatal(err)
		}
		everyProcessOfTheInstaller := -installing.Process.Pid
		ended := make(chan error, 1)
		go func() { ended <- installing.Wait() }()
		giveUp := time.AfterFunc(time.Minute, func() { syscall.Kill(everyProcessOfTheInstaller, syscall.SIGKILL) })
		defer giveUp.Stop()

		if !published.lanhopStarts("pair [configuration default]") {
			syscall.Kill(everyProcessOfTheInstaller, syscall.SIGKILL)
			t.Fatalf("lanhop pair did not start: %v\n%s", <-ended, output.String())
		}
		if err := syscall.Kill(everyProcessOfTheInstaller, syscall.SIGINT); err != nil {
			t.Fatal(err)
		}
		if !published.lanhopStarts("help [configuration default]") {
			syscall.Kill(everyProcessOfTheInstaller, syscall.SIGKILL)
			t.Fatalf("the installer did not carry on after Ctrl+C: %v\n%s", <-ended, output.String())
		}
		if _, err := terminal.WriteString("n\n"); err != nil {
			t.Fatal(err)
		}
		if err := <-ended; err != nil {
			t.Fatalf("the installer ended with %v\n%s", err, output.String())
		}
		expectSame(t, "runs of lanhop", published.lanhopWasRunWith(), configured("version", "pair", "help"))
		expectMention(t, output.String(), `the machines are not paired. Run "lanhop pair" again`, "Open it now? [y/N] ", "To remove lanhop:")
	})
}

func TestTheUnitRunsTheInstalledBinaryInTheChosenRole(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		for _, role := range []string{"share", "receive"} {
			t.Run(role, func(t *testing.T) {
				m := newMachine(t, shell, "Linux", "x86_64", newReleases(t))
				m.settings["LANHOP_ROLE"] = role
				m.mustRun()
				if got, want := m.read(unitFile), fmt.Sprintf(unitOfTheService, m.path(binaryFile), role); got != want {
					t.Errorf("unit:\n%s\nwant:\n%s", got, want)
				}
			})
		}
	})
}

func TestTheLaunchAgentRunsTheInstalledBinaryInTheChosenRole(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		for _, role := range []string{"share", "receive"} {
			t.Run(role, func(t *testing.T) {
				m := newMachine(t, shell, "Darwin", "arm64", newReleases(t))
				m.settings["LANHOP_ROLE"] = role
				m.settings["LANHOP_YES"] = "1"
				output := m.mustRun()
				if got, want := m.read(agentFile), fmt.Sprintf(agentOfTheService, m.inRoot(appProgram), role, m.path(agentLogFile), appAssociation); got != want {
					t.Errorf("launch agent:\n%s\nwant:\n%s", got, want)
				}
				expectMention(t, output, "System Settings > Privacy & Security > Accessibility", "the program to allow is the lanhop app", "Choose lanhop in "+m.inRoot("Applications")+" and click Open")
			})
		}
	})
}

func TestRefusingTheLaunchAgentLeavesTheOneOfAnEarlierInstallAndSaysSo(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		m := newMachine(t, shell, "Darwin", "arm64", newReleases(t))
		m.settings["LANHOP_ROLE"] = "share"
		m.settings["LANHOP_YES"] = "1"
		m.mustRun()
		earlier := m.read(agentFile)

		delete(m.settings, "LANHOP_YES")
		m.settings["LANHOP_ROLE"] = "receive"
		m.typing("n", "")
		output := m.mustRun()
		if now := m.read(agentFile); now != earlier {
			t.Errorf("the launch agent changed although it was refused:\n%s", now)
		}
		expectSame(t, "system commands", commandsIn(output), []string{})
		expectMention(t, output, "The launch agent of an earlier install is still there")
	})
}

func TestTheUdevRuleThatIsAlreadyThereIsNotWrittenAgain(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		m := newMachine(t, shell, "Linux", "x86_64", newReleases(t))
		writeFile(t, filepath.Join(m.root, "etc/udev/rules.d/60-lanhop.rules"), []byte("written by hand\n"), 0o644)
		m.settings["LANHOP_ROLE"] = "receive"
		m.settings["LANHOP_YES"] = "1"
		output := m.mustRun()
		expectSame(t, "system commands", commandsIn(output), []string{joinTheInputGroup, reloadTheUnits, enableTheUnit})
		expectSame(t, "files in the home directory", m.files(), []string{unitFile, binaryFile})
	})
}

func TestRunsTheSystemCommandsWhenNotAskedToPrintThem(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		m := newMachine(t, shell, "Linux", "x86_64", newReleases(t))
		m.withRealSystemCommands()
		m.settings["LANHOP_ROLE"] = "receive"
		m.settings["LANHOP_YES"] = "1"
		output := m.mustRun()
		expectSame(t, "system commands that ran", m.systemCommandsRun(), []string{
			"usermod -aG input ana",
			"modprobe uinput",
			"udevadm control --reload-rules",
			"udevadm trigger",
			"systemctl --user daemon-reload",
			"systemctl --user enable lanhop.service",
		})
		wantFiles := map[string]string{
			"etc/udev/rules.d/60-lanhop.rules": `KERNEL=="uinput", SUBSYSTEM=="misc", GROUP="input", MODE="0660", OPTIONS+="static_node=uinput"` + "\n",
			"etc/modules-load.d/lanhop.conf":   "uinput\n",
		}
		if got := m.systemFiles(); !maps.Equal(got, wantFiles) {
			t.Errorf("files of the system = %q, want %q", got, wantFiles)
		}
		if strings.Contains(output, wouldRun) {
			t.Errorf("the output says %q although the commands ran:\n%s", wouldRun, output)
		}

		m.mustRun()
		if again := m.systemCommandsRun(); slices.Contains(again[6:], "modprobe uinput") {
			t.Errorf("the second run set up uinput again: %q", again[6:])
		}

		m.mustRun("--uninstall")
		if got := m.systemFiles(); len(got) != 0 {
			t.Errorf("files of the system after uninstall = %q, want none", got)
		}
		expectSame(t, "files in the home directory", m.files(), []string{})
	})
}

func TestStartsTheLaunchAgentAfterStoppingTheOneThatRuns(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		m := newMachine(t, shell, "Darwin", "arm64", newReleases(t))
		m.withRealSystemCommands()
		m.settings["LANHOP_ROLE"] = "share"
		m.settings["LANHOP_YES"] = "1"
		m.mustRun()
		expectSame(t, "system commands that ran", m.systemCommandsRun(), m.named(openAccessibility, stopTheAgent, startTheAgent))
	})
}

func TestCarriesOnWhenASystemCommandFails(t *testing.T) {
	failures := []struct {
		name    string
		system  string
		command string
		says    string
		files   []string
	}{
		{
			name:    "sudo on Linux, which leaves the udev rule unrecorded",
			system:  "Linux",
			command: "sudo",
			says:    "that did not work",
			files:   []string{unitFile, binaryFile},
		},
		{
			name:    "systemctl on a system without systemd",
			system:  "Linux",
			command: "systemctl",
			says:    "systemd did not take it",
			files:   []string{unitFile, binaryFile, ruleRecordFile},
		},
		{
			name:    "modprobe after the udev rule was written",
			system:  "Linux",
			command: "modprobe",
			says:    "the rule is written, but it could not be applied now",
			files:   []string{unitFile, binaryFile, ruleRecordFile},
		},
		{
			name:    "launchctl on macOS",
			system:  "Darwin",
			command: "launchctl",
			says:    "launchd did not start it now",
			files:   []string{binaryFile, agentFile},
		},
		{
			name:    "open on macOS",
			system:  "Darwin",
			command: "open",
			says:    "System Settings did not open",
			files:   []string{binaryFile, agentFile},
		},
	}
	inEveryShell(t, func(t *testing.T, shell string) {
		for _, failure := range failures {
			t.Run(failure.name, func(t *testing.T) {
				m := newMachine(t, shell, failure.system, "x86_64", newReleases(t))
				m.withRealSystemCommands()
				m.systemCommandEndsWith(failure.command, 1)
				m.settings["LANHOP_ROLE"] = "receive"
				m.settings["LANHOP_YES"] = "1"
				output := m.mustRun()
				expectMention(t, output, failure.says, "Two machines must be paired once", "To remove lanhop:")
				expectSame(t, "files in the home directory", m.files(), failure.files)
			})
		}
	})
}

func TestPairingUsesTheConfigurationTheServiceWillUse(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		published := newReleases(t)
		m := newMachine(t, shell, "Linux", "x86_64", published)
		m.settings["LANHOP_CONFIG_DIR"] = "/somewhere/else"
		m.typing("y", "n", "n", "y", "yes", "n")
		m.mustRun()
		runs := published.lanhopWasRunWith()
		if !slices.Contains(runs, "pair [configuration default]") {
			t.Errorf("lanhop pair did not run with the default configuration directory: %q", runs)
		}
	})
}

func TestAMachineWithoutTheEditorIsToldWhereTheLayoutFileIs(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		published := newReleases(t)
		published.usage = usageWithoutTheEditor
		published.publish(latestRelease, "1.0.0")
		m := newMachine(t, shell, "Linux", "x86_64", published)
		m.typing("y", "n", "n", "n", "y")
		output := m.mustRun()
		expectSame(t, "runs of lanhop", published.lanhopWasRunWith(), configured("version", "help", "layout"))
		expectMention(t, output, "by hand", "The layout file is /desk/layout")
	})
}

func TestTheLastLineSaysHowToUpdateWhenTheInstalledLanhopCan(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		for _, c := range []struct {
			usage string
			says  bool
		}{
			{usageWithTheEditor + "\n" + usageWithTheStatus, false},
			{usageWithTheEditor + "\n" + usageWithTheStatus + "\n" + usageWithTheUpdate, true},
		} {
			published := newReleases(t)
			published.usage = c.usage
			published.publish(latestRelease, "1.0.0")
			m := newMachine(t, shell, "Linux", "x86_64", published)
			m.typing("y")
			output := m.mustRun()
			expectMention(t, output, "To remove lanhop:")
			if says := strings.Contains(output, "To update lanhop: lanhop update"); says != c.says {
				t.Errorf("with the usage %q, the installer says how to update: %t, want %t\n%s", c.usage, says, c.says, output)
			}
		}
	})
}

func TestInstallsTheVersionThatWasAskedFor(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		published := newReleases(t)
		published.publish("download/v0.9.0", "0.9.0")
		m := newMachine(t, shell, "Linux", "aarch64", published)
		m.settings["LANHOP_VERSION"] = "v0.9.0"
		m.mustRun()
		if got, want := m.installedVersion(), "lanhop 0.9.0 linux-arm64"; got != want {
			t.Errorf("installed %q, want %q", got, want)
		}
	})
}

func TestInstallsInTheDirectoryThatWasAskedFor(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		m := newMachine(t, shell, "Linux", "x86_64", newReleases(t))
		tools := m.path("my tools")
		m.settings["LANHOP_INSTALL_DIR"] = tools
		m.settings["LANHOP_ROLE"] = "share"
		output := m.mustRun()
		expectSame(t, "files in the home directory", m.files(), []string{unitFile, "my tools/lanhop"})
		expectMention(t, output, tools+" is not on your PATH", `export PATH="`+tools+`:$PATH"`)
		if !strings.Contains(m.read(unitFile), `ExecStart="`+tools+`/lanhop" share`+"\n") {
			t.Errorf("the unit does not run the binary in %s:\n%s", tools, m.read(unitFile))
		}
	})
}

func TestSaysNothingAboutThePathWhenTheDirectoryIsOnIt(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		m := newMachine(t, shell, "Linux", "x86_64", newReleases(t))
		m.onPath = []string{m.path(".local/bin")}
		if output := m.mustRun(); strings.Contains(output, "is not on your PATH") {
			t.Errorf("the output talks about the PATH:\n%s", output)
		}
	})
}

func TestRefusesToInstallAndChangesNothing(t *testing.T) {
	refusals := []struct {
		name      string
		system    string
		settings  map[string]string
		arguments []string
		spoil     func(published *releases)
		code      int
		says      string
	}{
		{
			name:   "when the archive does not match its checksum",
			system: "Linux",
			spoil:  func(published *releases) { published.damage(latestRelease + "/lanhop-linux-amd64.tar.gz") },
			code:   1,
			says:   "the download does not match its checksum",
		},
		{
			name:   "when the list of checksums does not name the archive",
			system: "Darwin",
			spoil: func(published *releases) {
				published.write(latestRelease+"/SHA256SUMS", []byte("0000  lanhop-linux-amd64.tar.gz\n"))
			},
			code: 1,
			says: "SHA256SUMS of that release does not list lanhop-darwin-amd64.tar.gz",
		},
		{
			name:     "when the release does not exist",
			system:   "Linux",
			settings: map[string]string{"LANHOP_VERSION": "v7.7.7"},
			code:     1,
			says:     "could not download",
		},
		{
			name:     "on a system without a build",
			system:   "FreeBSD",
			code:     1,
			says:     "lanhop has no build for FreeBSD",
			settings: map[string]string{"LANHOP_ROLE": "share", "LANHOP_YES": "1"},
		},
		{
			name:     "on an architecture without a build",
			system:   "Linux",
			settings: map[string]string{"LANHOP_MACHINE": "riscv64", "LANHOP_ROLE": "share", "LANHOP_YES": "1"},
			code:     1,
			says:     "lanhop has no build for riscv64",
		},
		{
			name:     "with a role that is neither share nor receive",
			system:   "Linux",
			settings: map[string]string{"LANHOP_ROLE": "both", "LANHOP_YES": "1"},
			code:     1,
			says:     "LANHOP_ROLE must be share or receive",
		},
		{
			name:     "into a directory given as a relative path",
			system:   "Linux",
			settings: map[string]string{"LANHOP_INSTALL_DIR": "tools", "LANHOP_YES": "1"},
			code:     1,
			says:     "LANHOP_INSTALL_DIR must be an absolute path",
		},
		{
			name:      "with an option it does not know",
			system:    "Linux",
			arguments: []string{"--purge"},
			code:      2,
			says:      "unknown option --purge",
		},
	}
	inEveryShell(t, func(t *testing.T, shell string) {
		for _, refusal := range refusals {
			t.Run(refusal.name, func(t *testing.T) {
				published := newReleases(t)
				if refusal.spoil != nil {
					refusal.spoil(published)
				}
				m := newMachine(t, shell, refusal.system, "x86_64", published)
				for name, value := range refusal.settings {
					m.settings[name] = value
				}
				output, code := m.run(refusal.arguments...)
				if code != refusal.code {
					t.Errorf("exit code = %d, want %d", code, refusal.code)
				}
				expectMention(t, output, refusal.says)
				expectSame(t, "files in the home directory", m.files(), []string{})
				expectSame(t, "system commands", commandsIn(output), []string{})
				expectSame(t, "runs of lanhop", published.lanhopWasRunWith(), nil)
			})
		}
	})
}

func TestADamagedDownloadLeavesTheInstalledVersionInPlace(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		published := newReleases(t)
		m := newMachine(t, shell, "Linux", "x86_64", published)
		m.mustRun()
		published.publish(latestRelease, "2.0.0")
		published.damage(latestRelease + "/lanhop-linux-amd64.tar.gz")
		if output, code := m.run(); code != 1 {
			t.Errorf("exit code = %d, want 1\n%s", code, output)
		}
		if got, want := m.installedVersion(), "lanhop 1.0.0 linux-amd64"; got != want {
			t.Errorf("installed %q, want %q", got, want)
		}
		expectSame(t, "files in the home directory", m.files(), []string{binaryFile})
	})
}

func TestLeavesNothingInTheTemporaryDirectory(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		for _, download := range []string{"installed", "refused"} {
			t.Run("after a download that was "+download, func(t *testing.T) {
				published := newReleases(t)
				if download == "refused" {
					published.damage(latestRelease + "/lanhop-linux-amd64.tar.gz")
				}
				m := newMachine(t, shell, "Linux", "x86_64", published)
				m.run()
				left, err := os.ReadDir(m.scratch)
				if err != nil {
					t.Fatal(err)
				}
				if len(left) != 0 {
					t.Errorf("the installer left %d entries in the temporary directory, the first is %s", len(left), left[0].Name())
				}
				if installed := len(m.files()) == 1; installed != (download == "installed") {
					t.Errorf("files in the home directory = %q after a download that was %s", m.files(), download)
				}
			})
		}
	})
}

func TestADirectoryAServiceFileCannotNameGetsTheBinaryAndNoService(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		for _, system := range []string{"Linux", "Darwin"} {
			for _, directory := range []string{`R&D`, `a"b`, `a\b`, `a$b`, `50%`, `a<b`, `a>b`} {
				t.Run(system+" in "+directory, func(t *testing.T) {
					published := newReleases(t)
					if system == "Darwin" {
						published.withoutTheApp = true
						published.publish(latestRelease, "1.0.0")
					}
					m := newMachine(t, shell, system, "x86_64", published)
					m.settings["LANHOP_INSTALL_DIR"] = m.path(directory)
					m.settings["LANHOP_ROLE"] = "share"
					m.settings["LANHOP_YES"] = "1"
					m.withGroups(alreadyInInputGroup)
					output := m.mustRun()
					expectSame(t, "files in the home directory", m.files(), []string{directory + "/lanhop"})
					expectSame(t, "system commands", commandsIn(output), []string{})
					expectMention(t, output, "lanhop cannot be started at login from "+m.path(directory+"/lanhop"))
				})
			}
		}
	})
}

func TestRunningItAgainUpgradesAndLeavesTheSameFiles(t *testing.T) {
	again := []struct {
		system   string
		commands []string
		files    []string
		service  string
	}{
		{
			system:   "Linux",
			commands: []string{writeTheUdevRule, loadUinputAtBoot, loadUinputNow, reloadTheUdevRules, applyTheUdevRules, reloadTheUnits, enableTheUnit, restartTheUnit},
			files:    []string{unitFile, binaryFile, ruleRecordFile},
			service:  unitFile,
		},
		{
			system:   "Darwin",
			commands: []string{openAccessibility, stopTheAgent, startTheAgent},
			files:    []string{binaryFile, agentFile},
			service:  agentFile,
		},
	}
	inEveryShell(t, func(t *testing.T, shell string) {
		for _, twice := range again {
			t.Run(twice.system, func(t *testing.T) {
				published := newReleases(t)
				m := newMachine(t, shell, twice.system, "x86_64", published)
				m.settings["LANHOP_ROLE"] = "receive"
				m.settings["LANHOP_YES"] = "1"
				m.withGroups(alreadyInInputGroup)
				m.mustRun()
				service := m.read(twice.service)

				published.publish(latestRelease, "2.0.0")
				output := m.mustRun()
				if got, want := m.installedVersion(), "lanhop 2.0.0 "+strings.ToLower(twice.system)+"-amd64"; got != want {
					t.Errorf("installed %q, want %q", got, want)
				}
				expectSame(t, "system commands of the second run", commandsIn(output), m.named(twice.commands...))
				expectSame(t, "files in the home directory", m.files(), twice.files)
				if again := m.read(twice.service); again != service {
					t.Errorf("the service file changed:\n%s\nwas:\n%s", again, service)
				}
				if twice.system == "Darwin" {
					expectSame(t, "entries in Applications", entriesOf(t, m.inRoot("Applications")), []string{"lanhop.app"})
				}
			})
		}
	})
}

func TestUninstallRemovesWhatWasInstalledAndKeepsTheConfiguration(t *testing.T) {
	removals := []struct {
		name     string
		system   string
		role     string
		typed    []string
		yes      bool
		kept     string
		commands []string
		files    []string
		says     []string
	}{
		{
			name:     "on Linux, with the udev rule when the user agrees",
			system:   "Linux",
			role:     "receive",
			typed:    []string{"y"},
			kept:     linuxPeersFile,
			commands: []string{disableTheUnit, reloadTheUnits, deleteTheUdevRule, reloadTheUdevRules},
			files:    []string{linuxPeersFile},
			says:     []string{"are kept in {home}/.config/lanhop\n", "sudo gpasswd -d ana input"},
		},
		{
			name:     "on Linux, with the udev rule when it runs unattended",
			system:   "Linux",
			role:     "receive",
			yes:      true,
			kept:     linuxPeersFile,
			commands: []string{disableTheUnit, reloadTheUnits, deleteTheUdevRule, reloadTheUdevRules},
			files:    []string{linuxPeersFile},
		},
		{
			name:     "on Linux, leaving the udev rule when the user refuses",
			system:   "Linux",
			role:     "receive",
			typed:    []string{"n"},
			kept:     linuxPeersFile,
			commands: []string{disableTheUnit, reloadTheUnits},
			files:    []string{linuxPeersFile, ruleRecordFile},
			says:     []string{shownToTheUser(deleteTheUdevRule, reloadTheUdevRules) + "Run them now? [y/N] ", "The rule stays."},
		},
		{
			name:     "on Linux, leaving the udev rule when there is nobody to ask",
			system:   "Linux",
			role:     "receive",
			kept:     linuxPeersFile,
			commands: []string{disableTheUnit, reloadTheUnits},
			files:    []string{linuxPeersFile, ruleRecordFile},
		},
		{
			name:     "on Linux, without touching a udev rule the installer did not write",
			system:   "Linux",
			role:     "share",
			typed:    []string{"y"},
			kept:     linuxPeersFile,
			commands: []string{disableTheUnit, reloadTheUnits},
			files:    []string{linuxPeersFile},
		},
		{
			name:     "on macOS, with the launch agent and its log",
			system:   "Darwin",
			role:     "share",
			kept:     macPeersFile,
			commands: []string{stopTheAgent},
			files:    []string{macPeersFile},
			says:     []string{"Removed {root}/Applications/lanhop.app\n", "are kept in {home}/Library/Application Support/lanhop\n", "remove lanhop from System Settings > Privacy & Security > Accessibility, and switch it off under Local Network"},
		},
	}
	inEveryShell(t, func(t *testing.T, shell string) {
		for _, removal := range removals {
			t.Run(removal.name, func(t *testing.T) {
				m := newMachine(t, shell, removal.system, "x86_64", newReleases(t))
				m.settings["LANHOP_ROLE"] = removal.role
				m.settings["LANHOP_YES"] = "1"
				m.mustRun()
				writeFile(t, m.path(removal.kept), []byte("{}\n"), 0o600)
				if removal.system == "Darwin" {
					writeFile(t, m.path(agentLogFile), []byte("Sharing\n"), 0o644)
				}

				delete(m.settings, "LANHOP_ROLE")
				if !removal.yes {
					delete(m.settings, "LANHOP_YES")
				}
				if removal.typed != nil {
					m.typing(removal.typed...)
				}
				output := m.mustRun("--uninstall")
				expectSame(t, "system commands", commandsIn(output), m.named(removal.commands...))
				expectSame(t, "files in the home directory", m.files(), removal.files)
				expectMention(t, output, m.named(removal.says...)...)
				expectSame(t, "entries in Applications", entriesOf(t, m.inRoot("Applications")), []string{})
			})
		}
	})
}

func TestUninstallWithNothingInstalledChangesNothing(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		for _, system := range []string{"Linux", "Darwin"} {
			t.Run(system, func(t *testing.T) {
				m := newMachine(t, shell, system, "x86_64", newReleases(t))
				m.typing("y", "y")
				output := m.mustRun("--uninstall")
				expectSame(t, "system commands", commandsIn(output), []string{})
				expectSame(t, "files in the home directory", m.files(), []string{})
				expectMention(t, output, "There is no lanhop in "+m.path(".local/bin"))
			})
		}
	})
}

func TestHelpNamesEveryOptionAndVariable(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		m := newMachine(t, shell, "Linux", "x86_64", newReleases(t))
		output := m.mustRun("--help")
		expectMention(t, output, "--uninstall", "LANHOP_VERSION", "LANHOP_INSTALL_DIR", "LANHOP_ROLE", "LANHOP_YES")
		expectSame(t, "files in the home directory", m.files(), []string{})
	})
}

func entriesOf(t *testing.T, directory string) []string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func TestTheAppGoesToTheApplicationsOfTheHomeWhenTheSystemOnesCannotBeWritten(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		m := newMachine(t, shell, "Darwin", "arm64", newReleases(t))
		if err := os.Remove(m.inRoot("Applications")); err != nil {
			t.Fatal(err)
		}
		m.settings["LANHOP_ROLE"] = "receive"
		m.settings["LANHOP_YES"] = "1"
		output := m.mustRun()
		expectSame(t, "files in the home directory", m.files(), []string{binaryFile, appDirectory + "/Contents/Info.plist", appProgram, agentFile})
		if target := m.linkTarget(binaryFile); target != m.path(appProgram) {
			t.Errorf("%s links to %s, want %s", binaryFile, target, m.path(appProgram))
		}
		if !strings.Contains(m.read(agentFile), "<string>"+m.path(appProgram)+"</string>") {
			t.Errorf("the launch agent does not run the app in the home directory:\n%s", m.read(agentFile))
		}
		expectMention(t, output, "Press Command+Shift+G, type "+m.path("Applications")+" and press Return")

		m.mustRun("--uninstall")
		expectSame(t, "files in the home directory after uninstall", m.files(), []string{})
	})
}

func TestAProgramOfAnEarlierInstallBecomesALinkToTheAppAndItsOldPermissionsAreNamed(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		m := newMachine(t, shell, "Darwin", "arm64", newReleases(t))
		writeFile(t, m.path(binaryFile), []byte("#!/bin/sh\necho 'lanhop v0.1.0'\n"), 0o755)
		m.settings["LANHOP_ROLE"] = "receive"
		m.settings["LANHOP_YES"] = "1"
		output := m.mustRun()
		if target := m.linkTarget(binaryFile); target != m.inRoot(appProgram) {
			t.Errorf("%s links to %s, want %s", binaryFile, target, m.inRoot(appProgram))
		}
		expectMention(t, output, m.path(binaryFile)+" now runs the lanhop app", `"a.out"`, "remove it with\nthe - button")

		again := m.mustRun()
		if strings.Contains(again, "now runs the lanhop app") {
			t.Errorf("the second run talks about an earlier program again:\n%s", again)
		}
	})
}

func TestAReleaseWithoutTheAppInstallsTheBareProgramOnAMac(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		published := newReleases(t)
		published.withoutTheApp = true
		published.publish("download/v0.1.0", "0.1.0")
		m := newMachine(t, shell, "Darwin", "arm64", published)
		m.settings["LANHOP_VERSION"] = "v0.1.0"
		m.settings["LANHOP_ROLE"] = "share"
		m.settings["LANHOP_YES"] = "1"
		output := m.mustRun()
		if got, want := m.installedVersion(), "lanhop 0.1.0 darwin-arm64"; got != want {
			t.Errorf("installed %q, want %q", got, want)
		}
		expectSame(t, "files in the home directory", m.files(), []string{binaryFile, agentFile})
		expectSame(t, "entries in Applications", entriesOf(t, m.inRoot("Applications")), []string{})
		if got, want := m.read(agentFile), fmt.Sprintf(agentOfTheService, m.path(binaryFile), "share", m.path(agentLogFile), ""); got != want {
			t.Errorf("launch agent:\n%s\nwant:\n%s", got, want)
		}
		expectMention(t, output, "Installed lanhop 0.1.0 darwin-arm64 in "+m.path(binaryFile), "type "+m.path(binaryFile))
	})
}

func TestUninstallLeavesALanhopAppThatIsNotThisOne(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		m := newMachine(t, shell, "Darwin", "arm64", newReleases(t))
		other := appDirectory + "/Contents/Info.plist"
		writeFile(t, m.inRoot(other), []byte("<plist><dict><key>CFBundleIdentifier</key><string>org.example.lanhop</string></dict></plist>\n"), 0o644)
		m.mustRun("--uninstall")
		if _, err := os.Stat(m.inRoot(other)); err != nil {
			t.Errorf("uninstall removed a lanhop.app that is not this one: %v", err)
		}
	})
}

func TestAnInstalledAppStaysWhereItIsSoThatItsServiceKeepsRunningIt(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		for _, second := range []struct {
			name     string
			settings map[string]string
			typed    []string
		}{
			{"with no role", map[string]string{"LANHOP_YES": "1"}, nil},
			{"with the launch agent refused", nil, []string{"n", "n", ""}},
		} {
			t.Run(second.name, func(t *testing.T) {
				published := newReleases(t)
				m := newMachine(t, shell, "Darwin", "arm64", published)
				if err := os.Remove(m.inRoot("Applications")); err != nil {
					t.Fatal(err)
				}
				m.settings["LANHOP_ROLE"] = "receive"
				m.settings["LANHOP_YES"] = "1"
				m.mustRun()
				if err := os.MkdirAll(m.inRoot("Applications"), 0o755); err != nil {
					t.Fatal(err)
				}
				delete(m.settings, "LANHOP_ROLE")
				delete(m.settings, "LANHOP_YES")
				for name, value := range second.settings {
					m.settings[name] = value
				}
				if second.typed != nil {
					m.typing(second.typed...)
				}
				published.publish(latestRelease, "2.0.0")
				output := m.mustRun()
				if got, want := m.installedVersion(), "lanhop 2.0.0 darwin-arm64"; got != want {
					t.Errorf("installed %q, want %q", got, want)
				}
				expectSame(t, "entries in the system Applications", entriesOf(t, m.inRoot("Applications")), []string{})
				expectSame(t, "entries in the Applications of the home", entriesOf(t, m.path("Applications")), []string{"lanhop.app"})
				if _, err := os.Stat(m.path(appProgram)); err != nil || !strings.Contains(m.read(agentFile), "<string>"+m.path(appProgram)+"</string>") {
					t.Errorf("the launch agent does not run a program that exists (%v):\n%s", err, m.read(agentFile))
				}
				if strings.Contains(output, "now runs the lanhop app") {
					t.Errorf("an update of the app is described as replacing an earlier program:\n%s", output)
				}
			})
		}
	})
}

const (
	waitsForAccessibility = "accessibility: lanhop waits for the Accessibility permission."
	triesTheCentral       = `connecting: lanhop is trying to reach "iMac" at 10.0.0.5:24745.`
	keptOffTheNetwork     = `blocked: lanhop cannot reach "iMac" at 10.0.0.5:24745, but this terminal can.`
	connectedToTheCentral = `connected: lanhop is connected to "iMac" at 10.0.0.5:24745.`
	centralCannotBeFound  = `unreachable: neither lanhop nor this terminal can reach "iMac" at 10.0.0.5:24745.`
	nobodyConnectedYet    = "waiting: lanhop shares the keyboard and mouse of this machine, and no machine is connected yet."
	sharingWithTheSide    = `sharing: lanhop shares the keyboard and mouse of this machine with "pessoal".`
	notRunningYet         = "stopped: lanhop is not running here."
)

func TestTheInstallerAsksForEachPermissionWhenTheServiceNeedsItAndEndsConnected(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		published := newReleases(t).withTheStatus()
		states := []string{notRunningYet, waitsForAccessibility, waitsForAccessibility, triesTheCentral}
		for range 17 {
			states = append(states, keptOffTheNetwork)
		}
		published.statusSays(append(states, connectedToTheCentral)...)
		m := newMachine(t, shell, "Darwin", "arm64", published)
		m.typing("n", "y", "")
		output := m.mustRun()
		expectSame(t, "system commands", commandsIn(output), m.named(stopTheAgent, startTheAgent, openAccessibility, openLocalNetwork))
		expectMention(t, output,
			"Choose lanhop in "+m.inRoot("Applications")+" and click Open",
			"Waiting until lanhop has the permission.",
			"macOS may now ask whether lanhop may find devices on your local network. Click Allow.",
			"Privacy & Security > Local Network, switch on lanhop",
			connectedToTheCentral+"\n",
		)
		if strings.Count(output, "Choose lanhop in") != 1 || strings.Count(output, "switch on lanhop") != 1 {
			t.Errorf("a permission was explained more than once:\n%s", output)
		}
		if strings.Contains(output, "Press Enter here") {
			t.Errorf("the installer asks to press Enter instead of checking:\n%s", output)
		}
		if strings.Contains(output, "lanhop installer:") {
			t.Errorf("the installer reports a problem:\n%s", output)
		}
	})
}

func TestTheInstallerEndsAsSoonAsTheServiceIsConnected(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		published := newReleases(t).withTheStatus()
		published.statusSays(connectedToTheCentral)
		m := newMachine(t, shell, "Darwin", "arm64", published)
		m.typing("n", "y", "")
		output := m.mustRun()
		expectSame(t, "system commands", commandsIn(output), m.named(stopTheAgent, startTheAgent))
		expectMention(t, output, connectedToTheCentral)
		if strings.Contains(output, "Privacy & Security > Accessibility") {
			t.Errorf("the installer explains a permission that the service has:\n%s", output)
		}
	})
}

func TestACentralThatCannotBeReachedYetIsSaidOnceAndTheWaitGoesOn(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		published := newReleases(t).withTheStatus()
		published.statusSays(triesTheCentral, centralCannotBeFound, centralCannotBeFound, centralCannotBeFound, connectedToTheCentral)
		m := newMachine(t, shell, "Darwin", "arm64", published)
		m.typing("n", "y", "")
		output := m.mustRun()
		expectMention(t, output, centralCannotBeFound+"\nWaiting until lanhop connects.\n", connectedToTheCentral)
		if strings.Count(output, centralCannotBeFound) != 1 || strings.Count(output, "Waiting until lanhop connects.") != 1 {
			t.Errorf("the central that cannot be reached, or the wait after it, is said more than once:\n%s", output)
		}
		expectLastLine(t, output, connectedToTheCentral)
	})
}

func TestTheInstallerSaysWhatTheServiceStillWaitsForWhenTheLimitRunsOut(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		published := newReleases(t).withTheStatus()
		published.statusSays(waitsForAccessibility)
		m := newMachine(t, shell, "Darwin", "arm64", published)
		m.typing("n", "y", "")
		output := m.mustRun()
		expectMention(t, output, `lanhop is not connected yet. "lanhop status" says at any time what lanhop is doing. Now:`)
		expectLastLine(t, output, waitsForAccessibility)
	})
}

func TestTheInstallerOnACentralEndsWhenASideMachineConnects(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		published := newReleases(t).withTheStatus()
		published.statusSays(nobodyConnectedYet, nobodyConnectedYet, sharingWithTheSide)
		m := newMachine(t, shell, "Darwin", "arm64", published)
		m.typing("y", "y", "n", "n")
		output := m.mustRun()
		expectMention(t, output, sharingWithTheSide)
		if strings.Contains(output, "Click Allow") {
			t.Errorf("a central is told about the alert of a side machine:\n%s", output)
		}
	})
}

func TestTheInstallerOnACentralSaysItIsReadyWhenNobodyConnectsInTime(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		published := newReleases(t).withTheStatus()
		published.statusSays(nobodyConnectedYet)
		m := newMachine(t, shell, "Darwin", "arm64", published)
		m.typing("y", "y", "n", "n")
		output := m.mustRun()
		expectMention(t, output, "lanhop is ready on this machine, and no other machine is connected yet.")
	})
}

func TestTheInstallerWithNobodyAtTheKeyboardPrintsTheStatusOnceAndOpensNothing(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		published := newReleases(t).withTheStatus()
		published.statusSays(waitsForAccessibility, connectedToTheCentral)
		m := newMachine(t, shell, "Darwin", "arm64", published)
		m.settings["LANHOP_ROLE"] = "receive"
		m.settings["LANHOP_YES"] = "1"
		output := m.mustRun()
		expectSame(t, "system commands", commandsIn(output), m.named(stopTheAgent, startTheAgent))
		expectMention(t, output, `"lanhop status" says at any time what lanhop is doing. Now:`)
		expectLastLine(t, output, waitsForAccessibility)
		expectSame(t, "runs of lanhop", published.lanhopWasRunWith(), configured("version", "status"))
		expectNoQuestion(t, output)
	})
}

func TestTheInstallerChecksTheServiceOnLinuxToo(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		published := newReleases(t).withTheStatus()
		published.statusSays(triesTheCentral, connectedToTheCentral)
		m := newMachine(t, shell, "Linux", "x86_64", published)
		m.withGroups(alreadyInInputGroup)
		m.typing("n", "y", "y", "")
		output := m.mustRun()
		expectMention(t, output, connectedToTheCentral)
		if strings.Contains(output, "Click Allow") || strings.Contains(output, "Local Network") {
			t.Errorf("a Linux machine is told about a setting of macOS:\n%s", output)
		}
	})
}

func TestTheInstallerDoesNotCheckAServiceThatDidNotStart(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		published := newReleases(t).withTheStatus()
		published.statusSays(connectedToTheCentral)
		m := newMachine(t, shell, "Linux", "x86_64", published)
		m.typing("n", "y", "y", "y", "")
		output := m.mustRun()
		if strings.Contains(output, "Checking lanhop") || slices.Contains(published.lanhopWasRunWith(), configured("status")[0]) {
			t.Errorf("the installer checks a service that starts only at the next login:\n%s", output)
		}
	})
}

func TestTheFirewallOfAMacCentralIsOpenedForTheAppWhenTheUserAgrees(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		published := newReleases(t).withTheStatus()
		published.statusSays(sharingWithTheSide)
		m := newMachine(t, shell, "Darwin", "arm64", published)
		m.withFirewall("enabled", 1, "disabled")
		m.settings["LANHOP_ROLE"] = "share"
		m.settings["LANHOP_YES"] = "1"
		output := m.mustRun()
		expectSame(t, "system commands", commandsIn(output), m.named(stopTheAgent, startTheAgent, addToTheFirewall, unblockInFirewall))
		expectMention(t, output, "The firewall of this Mac is on.", shownToTheUser(m.named(addToTheFirewall, unblockInFirewall)...))
	})
}

func TestTheFirewallIsLeftAloneWhenItIsOffOrTheMachineReceives(t *testing.T) {
	cases := []struct {
		name, role, state string
		number            int
	}{
		{"a central with the firewall off", "share", "disabled", 0},
		{"a side machine with the firewall on", "receive", "enabled", 1},
	}
	inEveryShell(t, func(t *testing.T, shell string) {
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				published := newReleases(t).withTheStatus()
				published.statusSays(connectedToTheCentral)
				m := newMachine(t, shell, "Darwin", "arm64", published)
				m.withFirewall(c.state, c.number, "disabled")
				m.settings["LANHOP_ROLE"] = c.role
				m.settings["LANHOP_YES"] = "1"
				output := m.mustRun()
				expectSame(t, "system commands", commandsIn(output), m.named(stopTheAgent, startTheAgent))
				if strings.Contains(output, "The firewall of this Mac") {
					t.Errorf("the installer talks about the firewall:\n%s", output)
				}
			})
		}
	})
}

func TestAProgramOfAnEarlierInstallIsNamedEvenWhenNoServiceIsSetUp(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		m := newMachine(t, shell, "Darwin", "arm64", newReleases(t))
		writeFile(t, m.path(binaryFile), []byte("#!/bin/sh\necho 'lanhop v0.1.0'\n"), 0o755)
		output := m.mustRun()
		expectMention(t, output, m.path(binaryFile)+" now runs the lanhop app", `"a.out"`)
	})
}

func TestUninstallLeavesAnAppThatThisUserCannotRemoveAndRemovesTheRest(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write a directory without write permission")
	}
	inEveryShell(t, func(t *testing.T, shell string) {
		m := newMachine(t, shell, "Darwin", "arm64", newReleases(t))
		m.mustRun()
		if err := os.Chmod(m.inRoot("Applications"), 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(m.inRoot("Applications"), 0o755) })
		output := m.mustRun("--uninstall")
		expectMention(t, output, "Left "+m.inRoot(appDirectory)+", which this user cannot remove.", "Removed "+m.path(binaryFile))
		expectSame(t, "files in the home directory", m.files(), []string{})
		if _, err := os.Stat(m.inRoot(appProgram)); err != nil {
			t.Errorf("the app that could not be removed was emptied: %v", err)
		}
	})
}

func TestTheAppGetsALaunchAgentWhateverTheDirectoryOfTheCommand(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		m := newMachine(t, shell, "Darwin", "arm64", newReleases(t))
		m.settings["LANHOP_INSTALL_DIR"] = m.path("R&D")
		m.settings["LANHOP_ROLE"] = "share"
		m.settings["LANHOP_YES"] = "1"
		output := m.mustRun()
		expectSame(t, "files in the home directory", m.files(), []string{agentFile, "R&D/lanhop"})
		expectSame(t, "system commands", commandsIn(output), m.named(openAccessibility, stopTheAgent, startTheAgent))
		if target := m.linkTarget("R&D/lanhop"); target != m.inRoot(appProgram) {
			t.Errorf("R&D/lanhop links to %s, want %s", target, m.inRoot(appProgram))
		}
	})
}

func TestABlockedConnectionThatClearsUpQuicklyOpensNoPane(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		published := newReleases(t).withTheStatus()
		published.statusSays(triesTheCentral, keptOffTheNetwork, keptOffTheNetwork, connectedToTheCentral)
		m := newMachine(t, shell, "Darwin", "arm64", published)
		m.typing("n", "y", "")
		output := m.mustRun()
		expectSame(t, "system commands", commandsIn(output), m.named(stopTheAgent, startTheAgent))
		expectMention(t, output, connectedToTheCentral)
	})
}

func TestUninstallRemovesTheStatusOfTheServiceAndKeepsTheKey(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		m := newMachine(t, shell, "Darwin", "arm64", newReleases(t))
		m.mustRun()
		for _, name := range []string{"status", "status.lock"} {
			writeFile(t, m.path("Library/Application Support/lanhop/"+name), []byte("state=waiting\n"), 0o600)
		}
		writeFile(t, m.path(macPeersFile), []byte("{}\n"), 0o600)
		m.mustRun("--uninstall")
		expectSame(t, "files in the home directory", m.files(), []string{macPeersFile})
	})
}

func TestUninstallAlsoRemovesCopiesThatAnInterruptedUpdateLeft(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		m := newMachine(t, shell, "Darwin", "arm64", newReleases(t))
		m.mustRun()
		writeFile(t, m.inRoot(appDirectory+".old.4242/Contents/Info.plist"), []byte(infoOfTheApp), 0o644)
		writeFile(t, m.inRoot(appDirectory+".new.4243/Contents/Info.plist"), []byte(infoOfTheApp), 0o644)
		m.mustRun("--uninstall")
		expectSame(t, "entries in Applications", entriesOf(t, m.inRoot("Applications")), []string{})
	})
}

func expectLastLine(t *testing.T, output, want string) {
	t.Helper()
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	if last := lines[len(lines)-1]; last != want {
		t.Errorf("the last line is %q, want %q:\n%s", last, want, output)
	}
}

const notPaired = "stopped: lanhop is not running here. It ended with: this machine is not paired with a machine to receive from. First run: lanhop pair ADDRESS"

func TestAServiceThatStaysStoppedEndsTheWaitWithItsReason(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		published := newReleases(t).withTheStatus()
		published.statusSays(notPaired)
		m := newMachine(t, shell, "Darwin", "arm64", published)
		m.typing("n", "y", "")
		output := m.mustRun()
		expectLastLine(t, output, notPaired)
		if runs := len(published.lanhopWasRunWith()); runs > 40 {
			t.Errorf("lanhop ran %d times, want the wait for a stopped service to end within about 25 seconds", runs)
		}
	})
}

func TestAServiceThatRestartsIsWaitedFor(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		published := newReleases(t).withTheStatus()
		states := []string{}
		for range 12 {
			states = append(states, notRunningYet)
		}
		published.statusSays(append(states, triesTheCentral, connectedToTheCentral)...)
		m := newMachine(t, shell, "Darwin", "arm64", published)
		m.typing("n", "y", "")
		output := m.mustRun()
		expectLastLine(t, output, connectedToTheCentral)
	})
}

func TestAFailureIsSaidOnceAndTheWaitGoesOn(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		failing := `failing: lanhop cannot connect to "iMac" at 10.0.0.5:24745, and keeps trying: the other machine is not paired with this one`
		published := newReleases(t).withTheStatus()
		published.statusSays(failing, failing, failing, connectedToTheCentral)
		m := newMachine(t, shell, "Darwin", "arm64", published)
		m.typing("n", "y", "")
		output := m.mustRun()
		expectMention(t, output, failing+"\nWaiting until lanhop connects.\n")
		if strings.Count(output, failing) != 1 || strings.Count(output, "Waiting until lanhop connects.") != 1 {
			t.Errorf("the failure, or the wait after it, is not said exactly once:\n%s", output)
		}
		expectLastLine(t, output, connectedToTheCentral)
	})
}

func TestABlockedServiceOnLinuxIsSaidWithoutASettingOfMacOS(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		blocked := `blocked: lanhop cannot reach "iMac" at 10.0.0.5:24745, but this terminal can. lanhop failed with: no route to host`
		published := newReleases(t).withTheStatus()
		states := []string{triesTheCentral}
		for range 20 {
			states = append(states, blocked)
		}
		published.statusSays(append(states, connectedToTheCentral)...)
		m := newMachine(t, shell, "Linux", "x86_64", published)
		m.withGroups(alreadyInInputGroup)
		m.typing("n", "y", "y", "")
		output := m.mustRun()
		for _, command := range commandsIn(output) {
			if strings.HasPrefix(command, "open ") {
				t.Errorf("Linux ran %q", command)
			}
		}
		expectMention(t, output, blocked+"\nWaiting until lanhop connects.\n")
		if strings.Count(output, blocked) != 1 || strings.Count(output, "Waiting until lanhop connects.") != 1 || strings.Contains(output, "System Settings") || strings.Contains(output, "Click Allow") {
			t.Errorf("Linux got a setting of macOS, or the line not exactly once:\n%s", output)
		}
		expectLastLine(t, output, connectedToTheCentral)
	})
}

func TestStatusThatSaysNothingEndsTheWaitWithAPointerToTheCommand(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		published := newReleases(t).withTheStatus()
		published.statusSays("")
		m := newMachine(t, shell, "Darwin", "arm64", published)
		m.typing("n", "y", "")
		output := m.mustRun()
		expectLastLine(t, output, `silent: lanhop status said nothing. Run "lanhop status" to see why.`)
	})
}

func TestStatesThatKeepChangingStillEndAtTheLimit(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		published := newReleases(t).withTheStatus()
		states := []string{}
		for len(states) < 700 {
			states = append(states, notRunningYet, notRunningYet, notRunningYet, triesTheCentral)
		}
		published.statusSays(states...)
		m := newMachine(t, shell, "Darwin", "arm64", published)
		m.typing("n", "y", "")
		output := m.mustRun()
		expectMention(t, output, `lanhop is not connected yet. "lanhop status" says at any time what lanhop is doing. Now:`+"\n"+notRunningYet+"\n")
		expectLastLine(t, output, notRunningYet)
		if runs := len(published.lanhopWasRunWith()); runs > 620 {
			t.Errorf("lanhop status ran %d times, want the check to end at its limit of 600 seconds", runs)
		}
	})
}

func TestNobodyAtTheKeyboardWaitsBrieflyForTheServiceToStartAndExplainsAMissingPermission(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		published := newReleases(t).withTheStatus()
		published.statusSays(notRunningYet, notRunningYet, notRunningYet, waitsForAccessibility)
		m := newMachine(t, shell, "Darwin", "arm64", published)
		m.settings["LANHOP_ROLE"] = "receive"
		m.settings["LANHOP_YES"] = "1"
		output := m.mustRun()
		expectSame(t, "system commands", commandsIn(output), m.named(stopTheAgent, startTheAgent))
		expectMention(t, output, "the program to allow is the lanhop app")
		expectLastLine(t, output, waitsForAccessibility)
		expectNoQuestion(t, output)
	})
}

func TestTheFirewallThatBlocksEverythingIsExplainedInsteadOfOpened(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		published := newReleases(t).withTheStatus()
		published.statusSays(sharingWithTheSide)
		m := newMachine(t, shell, "Darwin", "arm64", published)
		m.withFirewall("enabled", 2, "enabled")
		m.settings["LANHOP_ROLE"] = "share"
		m.settings["LANHOP_YES"] = "1"
		output := m.mustRun()
		expectSame(t, "system commands", commandsIn(output), m.named(stopTheAgent, startTheAgent))
		expectMention(t, output, `switch off "Block all incoming connections"`)
	})
}

func TestTheFirewallStepThatIsRefusedRunsNothing(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		published := newReleases(t).withTheStatus()
		published.statusSays(sharingWithTheSide)
		m := newMachine(t, shell, "Darwin", "arm64", published)
		m.withFirewall("enabled", 1, "disabled")
		m.typing("y", "y", "n", "n", "n")
		output := m.mustRun()
		expectSame(t, "system commands", commandsIn(output), m.named(stopTheAgent, startTheAgent))
		expectMention(t, output, shownToTheUser(m.named(addToTheFirewall, unblockInFirewall)...), "Not run. The other machines cannot connect until the firewall lets lanhop in")
	})
}

func TestTheInstallerRestartsAServiceThatStillWaitsForAccessibilitySoThatItSeesThePermission(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		published := newReleases(t).withTheStatus()
		states := []string{}
		for range 20 {
			states = append(states, waitsForAccessibility)
		}
		published.statusSays(append(states, connectedToTheCentral)...)
		m := newMachine(t, shell, "Darwin", "arm64", published)
		m.typing("n", "y", "")
		output := m.mustRun()
		expectSame(t, "system commands", commandsIn(output), m.named(stopTheAgent, startTheAgent, openAccessibility, restartTheAgent))
		expectMention(t, output, "1. If the list already has a lanhop or an a.out, select it and remove it with the\n     - button.")
		expectLastLine(t, output, connectedToTheCentral)
	})
}

func TestABootstrapThatFailsRightAfterTheStopIsTriedAgain(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		m := newMachine(t, shell, "Darwin", "arm64", newReleases(t))
		m.withRealSystemCommands()
		fake := fmt.Sprintf(fakeLaunchctlThatFailsTheFirstBootstrap, filepath.Join(m.aside, "system commands"), filepath.Join(m.aside, "bootstrap failed once"))
		writeFile(t, filepath.Join(m.aside, "bin", "launchctl"), []byte(fake), 0o755)
		m.settings["LANHOP_ROLE"] = "receive"
		m.settings["LANHOP_YES"] = "1"
		output := m.mustRun()
		expectSame(t, "system commands that ran", m.systemCommandsRun(), m.named(openAccessibility, stopTheAgent, startTheAgent, startTheAgent))
		expectMention(t, output, "lanhop is running, and it starts every time you log in.")
		if strings.Contains(output, "launchd did not start it now") || strings.Contains(output, "Bootstrap failed") {
			t.Errorf("the installer gave up after the first bootstrap:\n%s", output)
		}
	})
}

func TestALanhopThatAsksAgainByItselfIsNotRestartedByTheInstaller(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		published := newReleases(t).thatAsksAgainByItself()
		states := []string{}
		for range 20 {
			states = append(states, waitsForAccessibility)
		}
		published.statusSays(append(states, connectedToTheCentral)...)
		m := newMachine(t, shell, "Darwin", "arm64", published)
		m.typing("n", "y", "")
		output := m.mustRun()
		expectSame(t, "system commands", commandsIn(output), m.named(stopTheAgent, startTheAgent, openAccessibility))
		expectMention(t, output, "lanhop notices the permission by itself within about 20 seconds.")
	})
}

func TestALanhopThatDoesNotAskAgainGetsTheCommandThatRestartsIt(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		published := newReleases(t).withTheStatus()
		published.statusSays(waitsForAccessibility)
		m := newMachine(t, shell, "Darwin", "arm64", published)
		m.settings["LANHOP_ROLE"] = "receive"
		m.settings["LANHOP_YES"] = "1"
		output := m.mustRun()
		expectMention(t, output, "If lanhop does not notice the permission, restart it with:\n  launchctl kickstart -k gui/501/com.github.rodrigogaraujo.lanhop")
	})
}

const (
	joinLine   = "10.0.0.5 RF4K-Q7MZ-2XPA-HB3T-W6NC-5LDE"
	joinCalled = "join 10.0.0.5 RF4K-Q7MZ-2XPA-HB3T-W6NC-5LDE"
)

func TestAJoinLineInstallsASideMachineWithoutAQuestionAndJoinsBeforeTheService(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		published := newReleases(t).withTheJoin()
		m := newMachine(t, shell, "Linux", "x86_64", published)
		m.settings["LANHOP_JOIN"] = joinLine
		m.typing()
		output := m.mustRun()
		expectNoQuestion(t, output)
		expectSame(t, "system commands", commandsIn(output), m.named(joinTheInputGroup, writeTheUdevRule, loadUinputAtBoot, loadUinputNow, reloadTheUdevRules, applyTheUdevRules, reloadTheUnits, enableTheUnit))
		expectSame(t, "files in the home directory", m.files(), []string{unitFile, binaryFile, ruleRecordFile})
		expectSame(t, "runs of lanhop", published.lanhopWasRunWith(), configured("version", "help", joinCalled))
		expectMention(t, output, `Joined "iMac" at 10.0.0.5:24745.`)
		if strings.Contains(output, "lanhop pair") || strings.Contains(output, "lanhop installer:") {
			t.Errorf("the installer explains pairing or reports a problem after a join:\n%s", output)
		}
		if !strings.Contains(m.read(unitFile), "receive") {
			t.Errorf("the unit does not receive:\n%s", m.read(unitFile))
		}
	})
}

func TestAJoinLineOnAMacStillWaitsForThePermissionsUntilConnected(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		published := newReleases(t).withTheStatus().withTheJoin()
		published.statusSays(notRunningYet, waitsForAccessibility, triesTheCentral, connectedToTheCentral)
		m := newMachine(t, shell, "Darwin", "arm64", published)
		m.settings["LANHOP_JOIN"] = joinLine
		m.typing()
		output := m.mustRun()
		expectNoQuestion(t, output)
		expectMention(t, output, "Waiting until lanhop has the permission.", connectedToTheCentral+"\n")
		calls := published.lanhopWasRunWith()
		joined := slices.Index(calls, joinCalled+" [configuration default]")
		if joined < 0 || joined > slices.Index(calls, "status [configuration default]") {
			t.Errorf("lanhop was run with %q, want the join before the first status", calls)
		}
		expectSame(t, "files in the home directory", m.files(), []string{binaryFile, agentFile})
	})
}

func TestARefusedJoinStopsTheInstallerBeforeAnyService(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		for _, system := range []string{"Linux", "Darwin"} {
			published := newReleases(t).withTheJoin()
			published.refusesTheJoin()
			m := newMachine(t, shell, system, "arm64", published)
			m.settings["LANHOP_JOIN"] = joinLine
			output, code := m.run()
			if code == 0 {
				t.Fatalf("%s: exit code 0 after a refused join:\n%s", system, output)
			}
			expectMention(t, output, "did not join", "lanhop add again")
			expectSame(t, system+": system commands", commandsIn(output), []string{})
			expectSame(t, system+": files in the home directory", m.files(), []string{binaryFile})
		}
	})
}

func TestAJoinLineTypedAgainAfterTheJoinRunsToTheEnd(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		published := newReleases(t).withTheJoin()
		m := newMachine(t, shell, "Linux", "x86_64", published)
		m.settings["LANHOP_JOIN"] = joinLine
		m.mustRun()
		output := m.mustRun()
		expectMention(t, output, `already trusts "iMac"`)
		expectSame(t, "files in the home directory", m.files(), []string{unitFile, binaryFile, ruleRecordFile})
		if strings.Contains(output, "did not join") {
			t.Errorf("the second run reports a failed join:\n%s", output)
		}
	})
}

func TestAJoinLineAddsLanhopToThePathWithoutAQuestion(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		published := newReleases(t).withTheJoin()
		m := newMachine(t, shell, "Linux", "x86_64", published)
		m.settings["LANHOP_JOIN"] = joinLine
		m.settings["SHELL"] = "/usr/bin/zsh"
		m.typing()
		output := m.mustRun()
		expectNoQuestion(t, output)
		expectMention(t, output, "Added "+m.path(".local/bin")+" to the PATH in "+m.path(".zshrc"))
	})
}

func TestAJoinLineThatCannotBeUsedStopsBeforeDownloading(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		for _, settings := range []map[string]string{
			{"LANHOP_JOIN": joinLine, "LANHOP_ROLE": "share"},
			{"LANHOP_JOIN": "10.0.0.5"},
			{"LANHOP_JOIN": "10.0.0.5 CODE extra"},
			{"LANHOP_JOIN": "10.0.0.5 $(reboot)"},
			{"LANHOP_JOIN": "10.0.0.5 "},
			{"LANHOP_JOIN": " RF4K-Q7MZ-2XPA-HB3T-W6NC-5LDE"},
			{"LANHOP_JOIN": "10.0.0.5\tRF4K-Q7MZ-2XPA-HB3T-W6NC-5LDE"},
		} {
			published := newReleases(t)
			m := newMachine(t, shell, "Linux", "x86_64", published)
			maps.Copy(m.settings, settings)
			output, code := m.run()
			if code == 0 || !strings.Contains(output, "LANHOP_") {
				t.Errorf("settings %q: exit code %d, want a refusal that names the setting:\n%s", settings, code, output)
			}
			expectSame(t, "files in the home directory", m.files(), []string{})
		}
	})
}

func TestTheInstallerPutsLanhopOnThePathInTheFileOfTheShell(t *testing.T) {
	for _, setup := range []struct {
		name, system, shell string
		existing            []string
		profile             string
		zdotdir             string
	}{
		{"zsh on Linux", "Linux", "/usr/bin/zsh", nil, ".zshrc", ""},
		{"zsh on macOS", "Darwin", "/bin/zsh", nil, ".zshrc", ""},
		{"bash on Linux", "Linux", "/bin/bash", nil, ".bashrc", ""},
		{"bash on macOS with a profile", "Darwin", "/bin/bash", []string{".profile"}, ".profile", ""},
		{"bash on macOS without one", "Darwin", "/bin/bash", nil, ".bash_profile", ""},
		{"zsh with ZDOTDIR", "Linux", "/usr/bin/zsh", []string{"zsh/.keep"}, ".zshrc", "zsh"},
	} {
		t.Run(setup.name, func(t *testing.T) {
			published := newReleases(t)
			m := newMachine(t, "sh", setup.system, "arm64", published)
			if setup.zdotdir != "" {
				m.settings["ZDOTDIR"] = m.path(setup.zdotdir)
				setup.profile = setup.zdotdir + "/" + setup.profile
			}
			for _, name := range setup.existing {
				writeFile(t, m.path(name), []byte("# mine\n"), 0o644)
			}
			m.settings["SHELL"] = setup.shell
			m.settings["LANHOP_YES"] = "1"
			wanted := `export PATH="` + m.path(".local/bin") + `:$PATH"`

			output := m.mustRun()
			expectMention(t, output, "Added "+m.path(".local/bin")+" to the PATH in "+m.path(setup.profile))
			m.mustRun()
			if got := strings.Count(m.read(setup.profile), wanted); got != 1 {
				t.Errorf("%s holds the PATH line %d times after two runs, want once:\n%s", setup.profile, got, m.read(setup.profile))
			}
		})
	}
}

func TestThePathLineWaitsForAYesAndOtherShellsGetTheAdvice(t *testing.T) {
	published := newReleases(t)
	m := newMachine(t, "sh", "Linux", "x86_64", published)
	m.settings["SHELL"] = "/usr/bin/zsh"
	m.typing("n")
	output := m.mustRun()
	expectMention(t, output, "Add it in "+m.path(".zshrc")+"? [y/N]", "is not on your PATH")
	if slices.Contains(m.files(), ".zshrc") {
		t.Errorf("the installer wrote .zshrc after a no")
	}

	agreeing := newMachine(t, "sh", "Linux", "x86_64", newReleases(t))
	agreeing.settings["SHELL"] = "/usr/bin/zsh"
	agreeing.typing("y")
	output = agreeing.mustRun()
	expectMention(t, output, "Added "+agreeing.path(".local/bin")+" to the PATH in "+agreeing.path(".zshrc"))

	unwritable := newMachine(t, "sh", "Linux", "x86_64", newReleases(t))
	if err := os.MkdirAll(unwritable.path(".zshrc"), 0o755); err != nil {
		t.Fatal(err)
	}
	unwritable.settings["SHELL"] = "/usr/bin/zsh"
	unwritable.settings["LANHOP_YES"] = "1"
	output = unwritable.mustRun()
	expectMention(t, output, "could not write in "+unwritable.path(".zshrc"), "add this line")

	oddFolder := newMachine(t, "sh", "Linux", "x86_64", newReleases(t))
	oddFolder.settings["SHELL"] = "/usr/bin/zsh"
	oddFolder.settings["LANHOP_YES"] = "1"
	oddFolder.settings["LANHOP_INSTALL_DIR"] = oddFolder.path("odd$folder")
	output = oddFolder.mustRun()
	expectMention(t, output, "add this line")
	if slices.Contains(oddFolder.files(), ".zshrc") {
		t.Errorf("the installer wrote a PATH line for a folder with a dollar sign")
	}

	other := newMachine(t, "sh", "Linux", "x86_64", newReleases(t))
	other.settings["SHELL"] = "/usr/bin/fish"
	other.settings["LANHOP_YES"] = "1"
	output = other.mustRun()
	expectMention(t, output, "is not on your PATH. To run lanhop by its name, add this line")
	expectSame(t, "files in the home directory", other.files(), []string{binaryFile})
}

func TestAJoinLineWithAReleaseThatCannotJoinStopsAndSaysWhy(t *testing.T) {
	inEveryShell(t, func(t *testing.T, shell string) {
		for _, system := range []string{"Linux", "Darwin"} {
			published := newReleases(t)
			m := newMachine(t, shell, system, "arm64", published)
			m.settings["LANHOP_JOIN"] = joinLine
			output, code := m.run()
			if code == 0 {
				t.Fatalf("%s: exit code 0 with a release that cannot join:\n%s", system, output)
			}
			expectMention(t, output, "cannot join a machine yet", "LANHOP_VERSION", "lanhop version")
			expectSame(t, system+": runs of lanhop", published.lanhopWasRunWith(), configured("version", "help"))
			expectSame(t, system+": system commands", commandsIn(output), []string{})
			expectSame(t, system+": files in the home directory", m.files(), []string{binaryFile})
		}
	})
}
