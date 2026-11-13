# Implementation Plan: Updating without work

**Spec**: [spec.md](spec.md) · **Created**: 2026-11-13 · **Status**: Draft

## Goal

`lanhop update` on the central updates it, and the side machines follow it to the same version by themselves. Releases are signed, and the macOS app keeps one signature from release to release, so macOS keeps its permissions. See [ADR 10](../../docs/adr/0010-a-stable-signature-from-a-certificate-of-the-project.md) and [ADR 11](../../docs/adr/0011-signed-releases-and-side-machines-that-follow-the-central.md).

## How an update moves

```
central                                          side machine (started by its service, following on)
───────                                          ───────────────────────────────────────────────────
lanhop update
  note the version of each side machine
  find the version: releases/latest
  download, check, replace
  restart the service (kickstart / systemctl)
                                       ◄──────── connects again: hello, Features (bit 8), Version v0.1.2
  Version v0.1.3 ────────────────────────────►
  v0.1.2 is lower, not below "from",
  and bit 8 is set:
  Update v0.1.3 ─────────────────────────────►   download v0.1.3, check
                                                 wait: no focus, last leave answered,
                                                 no file transfer open
                                                 check again from the start, replace,
                                                 end with status 3
                                                 the service starts the new version
                                                 (launchd within about 10 s,
                                                 systemd after RestartSec=10)
                                       ◄──────── connects again: Version v0.1.3
  status file: the machine is on v0.1.3
lanhop update prints "office-mac v0.1.2 → v0.1.3"
```

The 60 seconds of SC-001 hold the restart of the central, the download and checks on each side machine, up to 10 seconds of restart delay on each, and the new connections.

## Checking and replacing

The checks, their order, the size limits and the way of replacing are in ADR 11. Where things are:

| What | macOS | Linux |
|---|---|---|
| Program that is replaced | `lanhop.app`, where the launch agent points | `lanhop`, where the systemd unit points |
| How lanhop finds it | `ProgramArguments` of the launch agent, links resolved, then the lanhop.app that holds it. Without a launch agent, `os.Executable` with links resolved | `ExecStart` of the systemd unit, links resolved. Without a unit, `os.Executable` with links resolved |
| New copy unpacked in | A folder made with `os.MkdirTemp` beside the app, on the same disk | A folder made with `os.MkdirTemp` beside the program, on the same disk |
| Replaced with | `renamex_np` with `RENAME_SWAP`, through cgo, then the old app is removed | `rename` of the new program over the old one |
| Restarted by | The launch agent (`KeepAlive`, `SuccessfulExit` false) | The systemd user unit (`Restart=on-failure`, `RestartSec=10`) |

A lock file named `update.lock`, in the configuration directory next to `status.lock`, keeps two updates of one machine apart, and covers the removal of folders left by a crash at start. A second update waits for the first one to end, then finds the machine already updated.

Downloads give up when GitHub does not answer within 10 seconds, or when an archive takes more than 5 minutes. A download goes on when the connection to the central drops. The downloaded files of a version are kept until it is installed, and an Update for the same version uses them instead of downloading again. Every check runs again on them before they replace anything.

On the central, `lanhop update` notes the version of each side machine from the status file, replaces the program, then restarts the service with `launchctl kickstart -k` or `systemctl --user restart`. When the service of that machine is not running, it replaces the program and says to start lanhop again.

## When lanhop refuses

- The running version is not a release: `dev` from the source code, `dev-<commit>` from a run of the workflow, or anything that does not match `vMAJOR.MINOR.PATCH`.
- The version from `releases/latest` is not above the running one. When it equals it, lanhop says that the machine is up to date and ends with status 0. When it is lower, lanhop says that it was offered an older release and replaces nothing.
- `--version` names a lower version without `--older`.
- The program is not inside a lanhop.app on macOS, or its folder cannot be written. lanhop says where it is and prints the installer line.
- The disk of the app does not offer `RENAME_SWAP`.
- `UPDATE` names another version than the one asked for, or a `from` above the running version (FR-012).

## Following the central

A side machine follows when all three hold:

1. Its service started it: `LANHOP_SERVICE=1`, which the installer sets in the launch agent and in the systemd unit from v0.1.2 on.
2. Its version is a release.
3. Following is on. `lanhop updates off` writes a file named `updates`, with the word `off`, in the configuration directory, as `lanhop clipboard off` does (spec 005). `lanhop updates on` removes it. `lanhop receive` reads it when it connects and at each focus change, and sends Features again when it changes.

Features carries the whole set of bits of a machine, held in one place in the process (`internal/features`, shared with specs 005 and 011), so that a Features frame sent for one feature never clears the bit of another.

The central sends Update to a side machine that has bit 8 set, a lower version, and a version not below the central's own `from`: once after its Version arrives, and again if bit 8 turns on later in the connection. The side machine downloads and checks at once. It replaces and restarts when it has no focus, has answered the last leave, and has no file transfer open. If the download or a check fails, it writes the reason in its log and goes on with the old version until its next connection.

## The status file

The central writes, for each side machine, its name, its version, and one of:

| Mark | Meaning |
|---|---|
| `follows` | It set bit 8, and the central can update it |
| `does not follow` | It sent a Version without bit 8: updates are off, it runs by hand, or it is not a release |
| `needs the installer` | It sent no Version within 5 seconds of connecting (0.1.1 or older), or its version is below the central's `from` |

`lanhop status` on the central shows the list. After the restart, `lanhop update` on the central reads it, prints each machine with the version noted before and the one it comes back with, and stops waiting after 90 seconds, with a line for each machine that did not come back.

## Wire

Two control frames and one Features bit, in the 8-byte format of [ADR 2](../../docs/adr/0002-linux-input-events-as-wire-format.md):

| Frame | Type | Code | Value |
|---|---|---|---|
| Version | `0xFF00` | 17 | The release version, major × 65536 + minor × 256 + patch, or 0 when it is not a release |
| Update | `0xFF00` | 16 | The version to move to, in the same form |

| Features bit | Meaning |
|---|---|
| 8 | This side machine follows the central |

Version goes both ways, right after Features. Bits 0 to 7 of Features belong to the clipboard and files, bits 8 to 15 to this spec, as agreed with spec 011. Codes 18 and up stay free.

## Release workflow

- Refuses a tag that does not match `vMAJOR.MINOR.PATCH`, or whose minor or patch is above 255.
- Fails when the functions of `install.sh` that write the launch agent, the systemd unit or the udev rule changed since the tag in `packaging/update-from`.
- Builds with the version and the `from` of `packaging/update-from` in the program, and fails when a build carries build tags or public keys other than the ones in the repository.
- Signs lanhop.app with the certificate of the project (ADR 10), from a temporary keychain, instead of ad hoc.
- Writes `UPDATE`, lists it in `SHA256SUMS`, then signs `SHA256SUMS` with the release key into `SHA256SUMS.sig` (ADR 11).
- The job that checks the macOS archives also checks that the requirement of each app has the identifier and the certificate hash kept in `packaging/macos/certificate-leaf`.
- A job verifies `SHA256SUMS.sig` and `UPDATE` through the same function that `lanhop update` uses.
- The jobs that sign run in the GitHub environment `release`. Its deployment policy allows only `v*` tags, each run needs the approval of the owner, and it holds `LANHOP_SIGNING_CERTIFICATE` (the certificate and key in PKCS#12, base64), `LANHOP_SIGNING_CERTIFICATE_PASSWORD` and `LANHOP_RELEASE_KEY` (the Ed25519 private key in PEM). Pull requests and manual runs make a certificate and a key inside the job and throw them away.

## Decisions

| Decision | Choice | Why | Given up |
|---|---|---|---|
| Signature of the macOS app | A self-signed certificate of the project. [ADR 10](../../docs/adr/0010-a-stable-signature-from-a-certificate-of-the-project.md) | The same requirement from release to release, at no cost | Gatekeeper still does not know the app |
| Trust in a download | Ed25519 signature of `SHA256SUMS` and `UPDATE`, with the public keys in lanhop. [ADR 11](../../docs/adr/0011-signed-releases-and-side-machines-that-follow-the-central.md) | A changed or older release is refused, on macOS and Linux, with no dependency | Keys to keep safe |
| Who can sign | A tag ruleset on `v*`, and an environment that only `v*` tags reach, with the owner's approval | Write access to the repository is not enough to sign | One approval on GitHub for each release |
| Changing a key | A next key and a next certificate hash, already in every machine | A change needs no installer | Two more secrets to keep offline |
| Who downloads | Each machine, from GitHub | The central can only name a version, never send a program | Side machines need the internet when they update |
| Order | The central first, the side machines when they connect again | No new local channel between `lanhop update` and the running `lanhop share`, and a machine that was off catches up later | A side machine is one version behind for up to a minute |
| Restart | Exit status 3 on a side machine, and its service starts the new version | The same path that spec 008 uses for the Accessibility permission, with no `exec` of a new program, which was never tried on macOS | Only a lanhop started by its service follows the central |
| Moment of the restart on a side machine | No focus, last leave answered, no file transfer open | The keyboard and mouse never freeze on the machine in use, and no copy or file on its way is cut | A file copy that waits on that machine for a later paste is lost |
| Telling that the service started lanhop | `LANHOP_SERVICE=1` in the launch agent and the systemd unit | Inherited variables such as `INVOCATION_ID` can reach a shell | v0.1.2 needs the installer, as it would anyway |
| Switch | `lanhop updates off`, a file in the configuration directory | The same way as the clipboard switch | Nothing |
| Control codes | 16 and 17. Bit 8 of Features | Codes 10 to 15 are files (ADR 13). Bits 0 to 7 are the clipboard and files, as agreed with spec 011 | Nothing |

## Tasks

One task, one pull request. Each verify list says what must be true before the task counts as done.

- [ ] **0. Measure what macOS keeps across two builds with the same certificate.** A small test app with its own identifier, `com.github.rodrigogaraujo.lanhop-signtest`, in a temporary folder, never the installed lanhop. Two builds signed with a test certificate: same requirement, different code hash and `LC_UUID`. The app asks whether it has Accessibility, tries a connection to a machine of the local network, and can swap a new copy of itself over its own bundle with `RENAME_SWAP`. It runs from a launch agent in a temporary folder, as lanhop does on a side machine. No VPN that blocks the local network may run during the test. → Verify, with the owner present:
  - After one approval of Accessibility and of Local Network for the first build, the second build, put at the same path by the first one swapping itself, has both with no new question.
  - The swap from the launch agent, and a swap run from a terminal as `lanhop update` on the central would, show no App Management question. The terminal used is written down.
  - The result goes in ADR 10, which is accepted or rejected by it. The test lines are removed after with `tccutil reset`, for that identifier only, and the launch agent is removed.
- [ ] **1. Keys of the project.** The owner creates the certificate of ADR 10 and its next one, and the Ed25519 key of ADR 11 and its next one. The owner puts the current ones in the environment `release`, sets its deployment policy and its approval, adds the tag ruleset, and keeps a copy of all four outside GitHub. The public keys and the certificate hashes go in the repository. → Verify:
  - `codesign` signs a test app with the certificate from a temporary keychain, and its requirement shows the hash in `packaging/macos/certificate-leaf`.
  - No private key is printed or written in a log.
- [ ] **2. Release workflow.** As in "Release workflow" above, with a manual run that does all of it on a branch with a throwaway certificate and key, and publishes nothing. → Verify that the manual run passes on the branch, and fails when:
  - the certificate hash it checks against is another one;
  - `SHA256SUMS` or `UPDATE` is changed after signing;
  - the tag is `v1.2.300` or `v1.2`;
  - the program was built with a build tag;
  - a function of `install.sh` that writes the agent, the unit or the udev rule changed since the tag in `packaging/update-from`.
- [ ] **3. `internal/update`.** Find the latest version from the address that `releases/latest` sends to. Download, check, unpack and replace, as in ADR 11. → Verify with unit tests, a local HTTP server and a test key. Each of these leaves the old program byte for byte and removes the new folder:
  - a changed archive, a changed `SHA256SUMS`, a missing signature, a signature by an unknown key;
  - an `UPDATE` of an older release offered as the new one, and a `releases/latest` that sends to an older release;
  - a `from` above the running version;
  - a version that is not `vMAJOR.MINOR.PATCH`, and a file over its size limit;
  - a path with `..`, an absolute path, a link, a hard link, a special file, a setuid bit;
  - a program that prints another version, and a rename that fails.
  - Also: a signature by the next key is accepted. A process killed between unpacking and swapping, and right after swapping, leaves a whole old or a whole new program, and the next start removes the leftover folder. On the macOS runner, an app signed with the test certificate passes the requirement check, an app signed ad hoc fails it, and a disk that does not offer `RENAME_SWAP`, faked in the test, is refused.
- [ ] **4. `lanhop update` on one machine.** The command, `--version`, `--older`, the lock, the timeouts, the refusals, and the restart of the service. → Verify with tests that use a fake `launchctl` and a fake `systemctl`, as in `install_test.go`, and a local HTTP server:
  - Two runs at the same time update once.
  - A build that is not a release, a program outside a lanhop.app, a release that needs the installer and a lower `--version` without `--older` each print why and change nothing.
  - The program is found through the launch agent or the unit when `~/.local/bin/lanhop` is a link.
  - A server that never answers ends the run after the timeout, with the old program in place.
- [ ] **5. Side machines follow the central.** Version, Update and Features bit 8 in `lanhop share` and `lanhop receive`, `lanhop updates`, `LANHOP_SERVICE=1` in the installer, and exit status 3. → Verify with an integration test: a central and five side machines over TLS on loopback, with a fake updater.
  - An older one that follows gets Update once. It restarts only after focus leaves it, its answer to the leave is sent, and an open file transfer ends.
  - A newer one is not moved back.
  - One below the central's `from` gets nothing and is marked `needs the installer`.
  - One with updates off gets nothing, then gets Update when it turns updates on.
  - One that sends no Features and drops every unknown frame, as 0.1.1 does, keeps getting keys and mouse, gets no Update, and is marked `needs the installer`.
  - A Features frame sent again for bit 8 keeps bit 0 set and does not reset the clipboard marks.
  - A side machine without `LANHOP_SERVICE=1`, and one that is not a release, never set bit 8.
- [ ] **6. Versions in the status, and `lanhop update` waits for the desk.** → Verify with tests on a fake status file that changes over time:
  - Machines are printed with their old and new versions as they come back.
  - The wait stops after 90 seconds.
  - The last lines name the machines that did not come back and the ones that need the installer.
- [ ] **7. Documents, and the test on a real desk.** README, `docs/setup-macos.md`, `SECURITY.md` and the last line of the installer say how to update, how releases are signed, and that the move to v0.1.2 asks for Accessibility once more. → Verify, with the owner present:
  - Test builds signed with the certificate of task 1, made only for the test with a build tag that release builds refuse, carrying a test key instead of the release key, and reading the address of a local folder from the environment.
  - A Mac as central with the firewall of macOS on, a Mac and the Linux server as side machines.
  - After the update: Accessibility, Local Network and the firewall need no click, each machine reaches `connected` or `sharing`, and the time of SC-001 is taken.
  - SC-001 and SC-002 count as met for the whole desk only after the first real update between two releases.
  - Then the release goes back on those machines with the installer line.
