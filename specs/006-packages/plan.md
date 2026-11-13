# Implementation Plan: Installer and release builds

**Spec**: [spec.md](spec.md) · **Created**: 2026-11-12 · **Status**: Draft

## Goal

A version tag becomes a release with one archive per system and architecture. One shell script downloads the right archive, checks it, installs it for the current user and sets lanhop up to start at login.

## Release builds

`.github/workflows/release.yml` runs on a tag `v*` and on every pull request.

```
build (4 jobs) ──► package ──► publish (only on a tag)
one binary each    4 archives   GitHub release with the
                   SHA256SUMS   archives and SHA256SUMS
                   install.sh
                   installs one
```

| Archive | Built on | How |
|---|---|---|
| `lanhop-linux-amd64.tar.gz` | Linux runner | `CGO_ENABLED=0`, static |
| `lanhop-linux-arm64.tar.gz` | Linux runner | `CGO_ENABLED=0`, static, cross-compiled |
| `lanhop-darwin-arm64.tar.gz` | macOS runner | cgo |
| `lanhop-darwin-amd64.tar.gz` | macOS runner | cgo, cross-compiled with the same toolchain |

- Each archive unpacks to one directory, `lanhop-SYSTEM-ARCHITECTURE`, with `lanhop`, `LICENSE` and `README.md`. Since [spec 008](../008-permissions-checked/plan.md), the macOS archives hold `lanhop.app` instead of `lanhop`.
- The names carry no version, so the installer finds the latest release at a fixed address, `releases/latest/download/NAME`, with no call to the GitHub API.
- The version goes into the binary with `-ldflags "-X main.version=..."`: the name of the tag on a tag, `dev-COMMIT` on a pull request.
- Each build job checks what it built: the system and architecture of the file, that the Linux binary is static, the oldest macOS the binary accepts, and the version it reports.
- The package job installs the Linux x86-64 archive it has just packed, with `install.sh`, and runs the installed program. So the names the workflow writes and the names the installer reads cannot drift apart.
- Only the publish job may write to the repository, and it runs only on a tag. A pull request never gets that permission.

## The installer

`install.sh`, at the root of the repository, POSIX `sh`. Everything is inside functions and the last line calls `main`, so a download that was cut in the middle runs nothing.

```
find the system and the architecture ─► download the archive and SHA256SUMS ─► check ─► ~/.local/bin/lanhop
        │
        ▼
"are the keyboard and mouse plugged into this machine?" ─► share or receive
        │
        ├─ Linux:  input group (sudo) ─► udev rule for uinput (sudo, receive only) ─► systemd user unit ─► enable
        └─ macOS:  launch agent ─► Accessibility pane ─► wait ─► start
        │
        ▼
pairing ─► layout (share only)
```

- **Questions** are read from `/dev/tty`. Only `y` or `yes` agrees. With no terminal, every question counts as refused.
- **`LANHOP_YES=1`** agrees to every question and never reads the terminal. Steps that need a person are then left out: running `lanhop pair`, the layout editor and the wait for Accessibility.
- **Commands of the system** (`sudo`, `systemctl`, `launchctl`, `open`) go through one function. It runs them, shows them indented before a question, or prints them in the test mode. What is shown and what is run come from the same lines of the script.
- **A step that fails** is reported and the installer carries on, so a wrong `sudo` password does not leave the rest undone.
- **Linux, at the end**: when the session already has the `input` group, the unit is enabled and started. Otherwise it is only enabled, and starts at the next login.
- **macOS**: the launch agent is stopped and started again on every run, so an update takes effect.
- **Uninstall** removes the service, the program and, on macOS, the log. The udev rule is removed only when the file `~/.local/state/lanhop/udev-rule-installed` says that the installer wrote it, and after asking.

## Decisions

| Decision | Choice | Why | Given up |
|---|---|---|---|
| Where the program goes | `~/.local/bin`, as the current user | No root, and each user who runs lanhop has their own key anyway | One install for every user of a machine |
| Finding the latest release | Fixed archive names and `releases/latest/download` | No API, no JSON to read in a shell script, no rate limit | The version in the name of the file |
| Download | `curl` only | It is what the one line already uses | Machines with only `wget` |
| Integrity | SHA-256 from `SHA256SUMS` of the same release | Catches a damaged or cut download | It is not a signature: whoever can replace the archive can replace the list |
| macOS build for x86-64 | Cross-compiled on the arm64 runner | One kind of macOS runner for both builds | It is never run by the workflow |
| Oldest macOS | `MACOSX_DEPLOYMENT_TARGET=11.0` | It is the oldest that Go 1.23 supports. Without it the C parts target the system of the runner | Nothing |
| udev rule on a sharer | Not offered | Sharing only reads `/dev/input`. Only a receiver creates devices through `uinput` | A sharer that later becomes a receiver runs the installer again |
| File loaded at start for `uinput` | `/etc/modules-load.d/lanhop.conf` | A name of its own can be removed safely on uninstall. The guide used `uinput.conf` and now uses this name too | Nothing |
| Launch agent when the user refuses | No file is written | A file in `~/Library/LaunchAgents` starts at the next login whether or not it was loaded, so writing it would start lanhop against the answer | The user runs the installer again to get it |
| systemd unit when the user refuses | The file is written, not enabled | A unit that is not enabled does nothing, and the user can enable it later with one command | Nothing |
| Restart | After a failure, 10 seconds later, on both systems | lanhop ends with an error while it is not paired or not allowed. With a restart it starts working by itself after `lanhop pair` or after the permission | One line in the log every 10 seconds until then |
| Stopping for good | A clean exit is not restarted | `pkill lanhop` stays a way out, as the macOS guide promises | Nothing |
| Paths in a unit or a launch agent | A path with `"`, `\`, `$`, `%`, `<`, `>` or `&` gets no service | Escaping for two file formats in a shell script is easy to get wrong. A space is fine | Those directories. The program is still installed there |
| `LANHOP_CONFIG_DIR` | The installer runs `lanhop pair` and `lanhop layout` without it | The service does not see that variable, so pairing must go where the service will look | Using the installer with another configuration directory |
| Signing | None | It needs a paid Apple developer account | A browser download is blocked, and the Accessibility permission must be given again after each update. Both are documented |

## Test mode

The tests run the real script, piped into `sh`, `dash` and `bash`, in a temporary home directory, against releases served by a local web server.

| Variable | What it does |
|---|---|
| `LANHOP_RELEASES_URL` | Where the releases are, instead of GitHub |
| `LANHOP_SYSTEM`, `LANHOP_MACHINE` | Stand in for `uname -s` and `uname -m` |
| `LANHOP_TERMINAL` | A file to read the answers from, instead of `/dev/tty` |
| `LANHOP_SYSTEM_ROOT` | A directory that stands for `/` where the installer looks for the udev rule |
| `LANHOP_PRINT_COMMANDS=1` | Prints each command of the system as `would run: ...` instead of running it, and goes on as if it had worked |

The archives of the tests hold a shell script named `lanhop` that records how it was called. Other tests leave `LANHOP_PRINT_COMMANDS` out and put stand-ins for `sudo`, `systemctl`, `launchctl` and the others first on the `PATH`, to cover the lines that really run commands.

## Tasks

- [ ] **1. Release workflow.** Build, check, pack and, on a tag, publish. → Verify: on a pull request, the four build jobs pass their checks, the package job installs with `install.sh`, and the run keeps the four binaries and the archives.
- [ ] **2. Installer.** `install.sh` with install, set up and `--uninstall`. → Verify: `install_test.go` runs it for Linux and macOS as share and as receive, with every question agreed, refused and unanswered, twice in a row, with a wrong checksum, with an unknown system, and with system commands that fail. `shellcheck` is clean.
- [ ] **3. Documentation.** The Install section of the README, and both setup guides pointing to the installer first. → Verify: the guides still describe every step by hand.
- [ ] **4. First release.** Push the first tag. → Verify: the release has four archives and `SHA256SUMS`, and the one line installs it on a Linux machine and on a Mac.
- [ ] **5. Real machines.** Run the installer by hand on a Linux machine with systemd and on a Mac, as share and as receive, then uninstall. → Verify: the checks listed in the pull request of tasks 1 to 3.
- [ ] **6. Homebrew formula.** A tap that installs from the same archives. → Verify: `brew install` on a Mac.

## Done when

- [ ] A person installs lanhop on a Linux machine and on a Mac with the one line, pairs them and uses them, without reading anything but what the installer prints (SC-001).
- [ ] After `--uninstall`, only the configuration directory is left (SC-003).

## Risks

- **Nothing here ran on a real machine.** The tests cover what the script decides and which commands it runs, not what systemd, launchd and macOS do with them. Task 5 exists for that.
- **`launchctl bootout` followed at once by `bootstrap`** may fail while the old lanhop is still ending. The installer then says that lanhop starts at the next login.
- **Adding a bare program to Accessibility** goes through a file chooser that hides `~/.local`. The installer tells the user to type the path. If that proves too hard, the program can move into an application bundle.
- **The x86-64 build for macOS is cross-compiled and never run** by the workflow. Someone with an Intel Mac has to try it. On 2026-11-13 the 0.1.0 build for x86-64 ran on an x86-64 machine running macOS, as the machine with the keyboard, and four other machines each connected to it during the night.
- **`curl | sh`** runs whatever the main branch holds at that moment. The README says how to read the script first.
