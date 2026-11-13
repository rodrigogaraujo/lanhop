# Implementation Plan: Updating without work

**Spec**: [spec.md](spec.md) · **Created**: 2026-11-13 · **Status**: Draft

## Goal

`lanhop update` on the central updates it, and the side machines follow it to the same version by themselves. Releases are signed, and the macOS app keeps one signature from release to release, so macOS keeps its permissions. See [ADR 10](../../docs/adr/0010-a-stable-signature-from-a-certificate-of-the-project.md) and [ADR 11](../../docs/adr/0011-signed-releases-and-side-machines-that-follow-the-central.md).

## How an update moves

```
central                                        side machine (as its service, following on)
───────                                        ───────────────────────────────────────────
lanhop update
  find the version: releases/latest
  download, check, replace
  end with status 3 ──► the service starts
                        the new version
                                     ◄──────── connects again: hello, Features (bit 2), Version 0.1.2
  Version 0.1.3 ───────────────────────────►
  0.1.2 is lower and bit 2 is set:
  Update 0.1.3 ────────────────────────────►   download 0.1.3, check
                                               wait until it does not have focus
                                               replace, end with status 3
                                               the service starts the new version
                                     ◄──────── connects again: Version 0.1.3
  status file: the machine is on 0.1.3
lanhop update prints "office-mac 0.1.2 → 0.1.3"
```

## Checking and replacing

The order of the checks is in ADR 11. Where things are:

| What | macOS | Linux |
|---|---|---|
| Program that is replaced | `lanhop.app`, in `/Applications` or `~/Applications`, where the installer put it | `lanhop`, in `~/.local/bin` or the folder given at install time |
| How lanhop finds it | The bundle that holds the running program | The running program, through `os.Executable` |
| New copy unpacked in | A new folder beside the app, on the same disk | A new folder beside the program, on the same disk |
| Replaced with | A rename of the new app over the old one, after moving the old one aside | A rename of the new program over the old one |
| Restarted by | The launch agent (`KeepAlive`, `SuccessfulExit` false) | The systemd user unit (`Restart=on-failure`) |

A lock file in the state directory keeps two updates of one machine apart. The second one waits for the first one to end, then finds the machine already updated.

On the central, `lanhop update` replaces the program, then restarts the service with `launchctl kickstart -k` or `systemctl --user restart`. When lanhop does not run as a service on that machine, it replaces the program and says to start lanhop again.

## When lanhop refuses

- The version is `dev`: a build of the source code is not replaced.
- The program is not where the installer puts it, or its folder cannot be written. lanhop says where it is and prints the installer line.
- The release says, in `UPDATE`, that it needs the installer from a version above this one (FR-012).
- The latest release is the running version: it says so and ends with status 0.

## Following the central

A side machine follows when all three hold:

1. It runs as its service: `XPC_SERVICE_NAME` is the label of the launch agent on macOS, `INVOCATION_ID` is set by systemd on Linux.
2. Its version is a release.
3. Following is on. `lanhop updates off` writes a file named `updates`, with the word `off`, in the configuration directory, as `lanhop clipboard off` does (spec 005). `lanhop updates on` removes it. `lanhop receive` reads it when it connects and at each focus change, and sends Features again when it changes.

The central sends Update to a side machine that has bit 2 set and a lower version, once per connection. The side machine downloads and checks at once, keeps the checked copy ready while it has focus, and replaces and restarts when focus leaves it or when it has no focus already. If the download or a check fails, it writes the reason in its log and goes on with the old version until its next connection.

## The status file

The central writes, for each side machine, its name, its version, and one of:

| Mark | Meaning |
|---|---|
| `follows` | It set bit 2 |
| `does not follow` | It sent a Version without bit 2: updates are off, it runs by hand, or it is a build of the source |
| `needs the installer` | It sent no Version: 0.1.1 or older |

`lanhop status` on the central shows the list. `lanhop update` on the central reads it after the restart, prints each machine as it comes back on the new version, and stops waiting after 90 seconds, with a line for each machine that did not come back.

## Wire

Two control frames and one Features bit, in the 8-byte format of [ADR 2](../../docs/adr/0002-linux-input-events-as-wire-format.md):

| Frame | Type | Code | Value |
|---|---|---|---|
| Version | `0xFF00` | 17 | The release version, major × 65536 + minor × 256 + patch, or 0 for `dev` |
| Update | `0xFF00` | 16 | The version to move to, in the same form |

| Features bit | Meaning |
|---|---|
| 2 | This side machine follows the central |

Version goes both ways, right after Features. Codes 18 and up stay free.

## Release workflow

- Signs lanhop.app with the certificate of the project (ADR 10), from a temporary keychain, instead of ad hoc.
- Writes `UPDATE` from `packaging/update-from`, a file in the repository with one version in it.
- Lists `UPDATE` in `SHA256SUMS`, then signs `SHA256SUMS` with the release key into `SHA256SUMS.sig` (ADR 11).
- The job that checks the macOS archive also checks that the requirement of the app has the identifier and the certificate hash kept in `packaging/macos/certificate-leaf`.
- A job verifies `SHA256SUMS.sig` with the public key kept in the code, through the same function that `lanhop update` uses.
- Secrets: `LANHOP_SIGNING_CERTIFICATE` (the certificate and key in PKCS#12, base64), `LANHOP_SIGNING_CERTIFICATE_PASSWORD` and `LANHOP_RELEASE_KEY` (the Ed25519 private key in PEM).

## Decisions

| Decision | Choice | Why | Given up |
|---|---|---|---|
| Signature of the macOS app | A self-signed certificate of the project. [ADR 10](../../docs/adr/0010-a-stable-signature-from-a-certificate-of-the-project.md) | The same requirement from release to release, at no cost | Gatekeeper still does not know the app |
| Trust in a download | Ed25519 signature of `SHA256SUMS`, with the public key in lanhop. [ADR 11](../../docs/adr/0011-signed-releases-and-side-machines-that-follow-the-central.md) | A changed release is refused, on macOS and Linux, with no dependency | A key to keep safe |
| Who downloads | Each machine, from GitHub | The central can only name a version, never send a program | Side machines need the internet when they update |
| Order | The central first, the side machines when they connect again | No new local channel between `lanhop update` and the running `lanhop share`, and a machine that was off catches up later | A side machine is one version behind for up to a minute |
| Restart | Exit status 3, and the service starts the new version | The same path as today. No `exec` of a new program, which spec 008 found unmeasured on macOS | Only a lanhop run as a service follows the central |
| Moment of the restart on a side machine | When it does not have focus | The keyboard and mouse never freeze on the machine in use | An update can wait while a person works on that machine |
| Switch | `lanhop updates off`, a file in the configuration directory | The same way as the clipboard switch | Nothing |
| Control codes | 16 and 17. Bit 2 of Features | Codes 10 to 15 and bit 1 are files (ADR 13) | Nothing |

## Tasks

One task, one pull request. Each verify line says what must be true before the task counts as done.

- [ ] **0. Measure that macOS keeps Accessibility across builds with the same certificate.** Two builds of a small test app, with its own identifier `com.github.rodrigogaraujo.lanhop-signtest`, in a temporary folder, never the installed lanhop. Each build asks macOS whether it has Accessibility, and writes the answer. → Verify: the owner turns on the line of the first build once; the first build answers yes; the second build, put at the same path, answers yes with no new click. Recorded in ADR 10, which is accepted or rejected by the result. The test line is removed after with `tccutil reset Accessibility com.github.rodrigogaraujo.lanhop-signtest`.
- [ ] **1. Keys of the project.** The owner creates the certificate of ADR 10 and the Ed25519 key of ADR 11, adds them as secrets of the repository, and keeps a copy outside GitHub. The public key and the certificate hash go in the repository. → Verify: `codesign` signs a test app with the certificate from a temporary keychain, and its requirement shows the hash in `packaging/macos/certificate-leaf`. No private key is printed or written in a log.
- [ ] **2. Release workflow.** Sign the app with the certificate, write `UPDATE`, sign `SHA256SUMS`, and check both, as in "Release workflow" above. Add a manual run that does all of it on a branch and publishes nothing. → Verify: the manual run passes on the branch, and fails when the certificate hash in the repository is changed to another one, and when `SHA256SUMS` is changed after signing.
- [ ] **3. `internal/update`.** Find the latest version from the address that `releases/latest` sends to. Download, check, unpack and replace, as in ADR 11. → Verify: unit tests with a local HTTP server and a test key. A changed archive, a changed `SHA256SUMS`, a missing or wrong signature, a path with `..`, an absolute path, a link that points outside, an `UPDATE` above the running version and a rename that fails each leave the old program byte for byte, and remove the new folder. On the macOS runner, an app signed with a test certificate passes the check against another build signed with it, and an app signed ad hoc fails it.
- [ ] **4. `lanhop update` on one machine.** The command, `--version`, the lock, the refusals, and the restart of the service. → Verify: tests with a fake `launchctl` and a fake `systemctl`, as in `install_test.go`, and a local HTTP server. Two runs at the same time update once. A `dev` build, a program outside the installer's folders and a release that needs the installer each print why and change nothing.
- [ ] **5. Side machines follow the central.** Version, Update and Features bit 2 in `lanhop share` and `lanhop receive`, `lanhop updates`, and exit status 3. → Verify: an integration test with a central and three side machines over TLS on loopback, with a fake updater: an older one that follows gets Update once and restarts only after focus leaves it; a newer one is not moved back; one with updates off gets nothing; one that sends no Features and drops every unknown frame, as 0.1.1 does, keeps getting keys and mouse and gets nothing. A side machine run by hand, and a `dev` build, never set bit 2.
- [ ] **6. Versions in the status, and `lanhop update` waits for the desk.** → Verify: tests with a fake status file that changes over time: machines are printed as they come back, the wait stops after 90 seconds, and the last lines name the machines that did not come back and the ones that need the installer.
- [ ] **7. Documents, and the test on a real desk.** README, `docs/setup-macos.md` and the installer's last line say how to update, and that the move to the first signed release asks for Accessibility once more. → Verify: with the owner present, on one Mac and the Linux server, two test builds signed with the certificate of task 1, made only for the test and never published: they carry a test key instead of the release key, and read the address of a local folder from the environment. After the update: Accessibility, Local Network and the firewall need no click, each machine reaches `connected` or `sharing`, and SC-001 is timed. Then the release of the desk goes back with the installer line.
