# 11. Signed releases, and side machines that follow the central

Date: 2026-11-13 · Status: proposed

## Context

[Spec 010](../../specs/010-updates/spec.md) wants one command on the central to update every machine. lanhop sees every key typed and can type on every machine, so a program that replaces it has to come from the project and nowhere else.

What exists today:

- Each release has four archives and `SHA256SUMS`. The installer checks the archive it downloads against `SHA256SUMS` of the same release. Both files come from the same place, so the check catches a broken download and not a changed release.
- The release workflow runs on any tag that starts with `v`, and any account with write access to the repository can push one.
- Side machines open their connection to the central. The hello carries protocol version 1, and the version has not changed since 0.1.0.
- Read in the code of 0.1.1: a side machine ignores every control code it does not know (`internal/receiver/receiver.go`), and the central reads and drops all that a side machine sends (`internal/sharer/sharer.go`). 0.1.1 defines code 5, Features, but never sends or reads it. From spec 005 on, Features is a set of bits whose unknown bits are ignored ([ADR 7](0007-clipboard-in-control-frames.md), [ADR 13](0013-files-travel-on-a-second-connection.md)).
- The service restarts lanhop when it ends with an error: `KeepAlive` with `SuccessfulExit` false in the launch agent, `Restart=on-failure` with `RestartSec=10` in the systemd unit.

## Decision

**Who can sign.**

- The signing secrets live in a GitHub environment named `release`. Its deployment policy allows only tags that match `v*`, and each run needs the approval of the owner.
- A tag ruleset on `v*` lets only the owner create, move or delete such a tag. A ruleset on `main` asks for a pull request and forbids force pushes.
- Pull requests and manual runs on a branch sign with a certificate and a key made inside the job and thrown away after it.
- The release job fails when the program was built with build tags, which `go version -m` shows.
- The jobs that hold signing secrets use actions pinned to a commit.

**Signed checksums.** The release workflow signs `SHA256SUMS` with an Ed25519 key of the project and publishes the signature as `SHA256SUMS.sig`, 64 bytes. lanhop carries two public keys in its code: the current one, and the next one, kept offline for a change of key. A signature by either is valid. The release that moves to the next key drops the old one and adds a new next one. The owner keeps a copy of both private keys outside GitHub. Go's standard library verifies Ed25519, so lanhop keeps no dependencies ([ADR 3](0003-go-with-no-dependencies.md)).

**What the signature covers.** `SHA256SUMS` lists the four archives and a file named `UPDATE`, with two lines:

```
version v0.1.3
from v0.1.2
```

`version` is the release that the files belong to. `from` is the oldest version that can move to this one without the installer. A release build also carries its own `from` in its code.

**Versions.** A release version matches `vMAJOR.MINOR.PATCH`, with minor and patch at most 255. lanhop checks a version against that form before it puts it in an address.

**Where it downloads.** lanhop asks only `https://github.com/rodrigogaraujo/lanhop/releases/download/<version>/`, and follows the HTTPS redirects that GitHub answers with, to the host that holds the files. Trust comes from the signature, not from the host. Only the version comes from outside: from the address that `releases/latest` sends to, or from the central. Each file has a size limit: 4 KiB for `SHA256SUMS` and `UPDATE`, 64 bytes for the signature, 100 MiB for an archive, and 200 MiB for what it unpacks to.

**Checks before replacing**, in this order. Any failure leaves the old program as it was, and removes what was downloaded.

1. `SHA256SUMS.sig` is a valid signature of `SHA256SUMS` by a key in the running program.
2. `UPDATE` matches its line in `SHA256SUMS`, and its `version` is the version asked for.
3. That version is above the running one. Only `lanhop update --version <v> --older`, typed on that machine, accepts a lower one.
4. `from` is not above the running version. Otherwise lanhop stops and prints the installer line.
5. The archive matches its line in `SHA256SUMS`.
6. The archive unpacks into a new folder made with `os.MkdirTemp` (mode 0700) beside the program, on the same disk. Only regular files and folders under `lanhop-<os>-<arch>/` are taken. Absolute paths, `..`, links of any kind, special files and setuid or setgid bits are refused.
7. On macOS, the new lanhop.app meets the requirement built from the identifier and the certificate hashes compiled into lanhop ([ADR 10](0010-a-stable-signature-from-a-certificate-of-the-project.md)), checked with the Security framework.
8. The new program, run with `version`, prints the version asked for.

A side machine that has to wait before replacing keeps the downloaded files, and runs every check again from step 1, into a new folder, just before it replaces.

**Replacing** happens in one step on the same disk, so a crash leaves either the old program or the new one, whole:

- On Linux, `rename` of the new program over the old one.
- On macOS, `renamex_np` with `RENAME_SWAP`, which swaps the new lanhop.app and the old one in one step. A plain `rename` cannot replace a folder that holds files. A disk that does not offer `RENAME_SWAP` is refused, with the installer line. The old app, now in the new folder's place, is removed after.
- A folder left over by a crash is removed at the next start, under the lock of updates.

**Finding the program.** lanhop takes the path from the definition of its service, `ProgramArguments` of the launch agent or `ExecStart` of the systemd unit, and resolves links. Without a service, from `os.Executable`, with links resolved. On macOS the program must sit inside a lanhop.app.

**Restarting.** A side machine that updates for the central ends with exit status 3 after replacing, and its service, taking it as an error, starts the new program at the same path. Status 3 is its own, apart from the status 1 of a failure or of the restart for Accessibility (spec 008), so a log or a test can tell an update from those. `lanhop update`, typed on any machine, runs apart from the service, so it restarts the service at once with `launchctl kickstart -k` or `systemctl --user restart`: the person chose the moment. The launch agent, the systemd unit and the link in `~/.local/bin` stay as they are.

**Side machines follow the central.**

- Features bits 0 to 7 belong to the clipboard and to files (specs 005 and 011). Bits 8 to 15 belong to this ADR. Every Features frame carries the whole set of bits of the machine that sends it, so that one feature never clears the bit of another.
- Bit 8 says that a machine follows the central. A side machine sets it only when its service started it (`LANHOP_SERVICE=1`, set by the installer in the launch agent and in the systemd unit), its version is a release, and following is switched on. A Features frame sent again because bit 8 changed does not count as announcing bit 0 again for the clipboard (ADR 8): only a change of bit 0 does.
- Control code 17, Version, goes from a side machine to the central right after Features, with the version in its value: major × 65536 + minor × 256 + patch. A build that is not a release sends 0.
- Control code 16, Update, goes from the central to a side machine that has bit 8 set, a version lower than the central's, and a version not below the central's `from`: once after the Version of the side machine arrives, and again if bit 8 turns on later in the connection. Its value is the version of the central.
- The side machine downloads and checks that version at once. It replaces and restarts only when it does not have focus, has answered the last leave (ADR 8), and has no file transfer open (ADR 13). A file copy that waits on it for a later paste is lost at the restart, as at any restart of lanhop.
- A side machine never moves to a version below its own because of the central, and ignores an Update while bit 8 is off. `lanhop update --older` switches following off on that machine, and says so, so that the central does not move it back up.
- The central writes the version of each side machine in its status file, where `lanhop status` and `lanhop update` read it.

**The central goes first.** `lanhop update` on the central replaces it and restarts it. The side machines connect again, the central sees their lower version and sends Update. A side machine that was off gets it when it connects later.

## Consequences

- A release asset changed after it was published, a changed `SHA256SUMS` or `UPDATE`, or a download changed by a proxy that reads TLS (one machine of the desk runs Zscaler) is refused. Such a proxy can still withhold updates, but cannot move a machine to an older release.
- Signing a program that lanhop takes needs a `v` tag that only the owner can push and the owner's approval in the `release` environment, or a private key itself.
- The central can only ask for a version number, never for an address or a program. A central can already type anything on a side machine, so this adds little to what a central can do.
- The hello stays at version 1 for good. A later change to the protocol has to come through Features bits, or keep accepting version 1 long enough to send Update.
- Machines on 0.1.0 and 0.1.1 ignore codes 16 and 17 and never set bit 8. They need the installer line once to reach v0.1.2, the first release that updates, which also sets `LANHOP_SERVICE=1`.
- The installer line that lanhop prints names `install.sh` at the tag of the release, not at `main`. The installer itself still trusts HTTPS and checks the archive against the unsigned `SHA256SUMS`, as today, so everything that falls back to it, 0.1.x, a release that needs the installer, lost keys, is protected by HTTPS and the rulesets only. Checking the signature in the installer is later work.
- Side machines need to reach github.com and the host it redirects to when they update.
- A release that does not start leaves the side machines that took it without lanhop until the installer line runs on each. Before publishing, the release workflow installs the arm64 macOS archive and runs it, and checks the signature of the amd64 one. Going back to the previous version by itself is out of scope.
- A change of key uses the next key, already in every machine, and needs no installer. If both keys are lost or leak, a release with new keys goes out, and each machine takes it with the installer line once.
- `packaging/update-from` is kept by hand. The release workflow runs the `install.sh` of that tag and of the release into temporary roots, with the same inputs, and fails when the launch agent, the systemd unit or the udev rule they write differ.

## Alternatives

- **Trust HTTPS to GitHub alone.** No key to keep, but anyone who can change a release of the project, or read TLS on the way, reaches every machine.
- **Sign the archives with the macOS certificate.** It covers only macOS, and the Linux archives would stay unsigned.
- **minisign or signify.** Well known formats, but they add a tool to the release workflow and to every machine that checks, where Go's standard library already verifies Ed25519.
- **The central sends the new program over the lanhop connection.** Side machines would not need the internet, but a central could then send any program, and the transport would carry large files before spec 011 adds a connection for them.
- **Each machine checks GitHub on its own every day.** Nothing to type at all, but every machine restarts at a moment nobody chose, and the keyboard and mouse pause then. It is out of scope.
- **`INVOCATION_ID` to tell that systemd started lanhop.** It needs no installer change, but a terminal that systemd starts can pass it on to the shells it opens (deduced, not measured), and a lanhop started by hand would then end after an update with nothing to start it again.
