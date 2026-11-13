# 11. Signed releases, and side machines that follow the central

Date: 2026-11-13 · Status: proposed

## Context

[Spec 010](../../specs/010-updates/spec.md) wants one command on the central to update every machine. lanhop sees every key typed and can type on every machine, so a program that replaces it has to come from the project and nowhere else.

What exists today:

- Each release has four archives and `SHA256SUMS`. The installer checks the archive it downloads against `SHA256SUMS` of the same release. Both files come from the same place, so the check catches a broken download and not a changed release.
- The release workflow runs on any tag that starts with `v`, and any account with write access to the repository can push one.
- Side machines open their connection to the central. The hello carries protocol version 1, and the version has not changed since 0.1.0.
- Read in the code of 0.1.1: a side machine ignores every control code it does not know (`internal/receiver/receiver.go`), and so does the central (`internal/sharer/sharer.go`). 0.1.1 has no Features frame. From spec 005 on, Features is a set of bits whose unknown bits are ignored ([ADR 7](0007-clipboard-in-control-frames.md), [ADR 13](0013-files-travel-on-a-second-connection.md)).
- The service restarts lanhop when it ends with an error: `KeepAlive` with `SuccessfulExit` false in the launch agent, `Restart=on-failure` in the systemd unit.

## Decision

**Who can sign.** The signing secrets live in a GitHub environment named `release`, which needs the approval of the owner for each run. Tags that start with `v` are protected so that only the owner can push them. Pull requests and manual runs on a branch sign with a certificate and a key made inside the job and thrown away after it.

**Signed checksums.** The release workflow signs `SHA256SUMS` with an Ed25519 key of the project and publishes the signature as `SHA256SUMS.sig`, 64 bytes. lanhop carries the public key in its code. The owner keeps a copy of the private key outside GitHub. Go's standard library verifies Ed25519, so lanhop keeps no dependencies ([ADR 3](0003-go-with-no-dependencies.md)).

**What the signature covers.** `SHA256SUMS` lists the four archives and a file named `UPDATE`, with two lines:

```
version v0.1.3
from v0.1.2
```

`version` is the release that the files belong to. `from` is the oldest version that can move to this one without the installer.

**Fixed addresses.** lanhop downloads only from `https://github.com/rodrigogaraujo/lanhop/releases/download/<version>/`. Only the version comes from outside: from the address that `releases/latest` sends to, or from the central.

**Checks before replacing**, in this order. Any failure leaves the old program as it was, and removes what was downloaded.

1. `SHA256SUMS.sig` is a valid signature of `SHA256SUMS` by the key in the running program.
2. `UPDATE` matches its line in `SHA256SUMS`, its `version` is the version asked for, and its `from` is not above the running version. Otherwise lanhop stops, and prints the installer line when `from` is the reason.
3. The archive matches its line in `SHA256SUMS`.
4. The archive unpacks into a new folder beside the program, on the same disk. Only regular files and folders under `lanhop-<os>-<arch>/` are taken. Absolute paths, `..`, links of any kind, special files and setuid or setgid bits are refused.
5. On macOS, the new lanhop.app meets the requirement built from the identifier and the certificate hash compiled into lanhop ([ADR 10](0010-a-stable-signature-from-a-certificate-of-the-project.md)), checked with the Security framework.
6. The new program, run with `version`, prints the version asked for.

A copy that waits, on a side machine that has focus, is checked again from step 4 on just before it replaces the old one.

**Replacing** happens in one step on the same disk, so a crash leaves either the old program or the new one, whole:

- On Linux, `rename` of the new program over the old one.
- On macOS, `renamex_np` with `RENAME_SWAP`, which swaps the new lanhop.app and the old one in one step. A plain `rename` cannot replace a folder that holds files. The old app, now in the new folder's place, is removed after. A folder left over by a crash is removed at the next start.

**Restarting.** A side machine ends with exit status 3 after replacing, and its service, taking it as an error, starts the new program at the same path. On the central, `lanhop update` runs apart from the service, so it restarts the service with `launchctl kickstart -k` or `systemctl --user restart`. The launch agent, the systemd unit and the link in `~/.local/bin` stay as they are.

**Side machines follow the central.**

- Features bits 0 to 7 belong to the clipboard and to files (specs 005 and 011). Bits 8 to 15 belong to this ADR.
- Bit 8 says that a machine follows the central. A side machine sets it only when its service started it (`LANHOP_SERVICE=1`, set by the installer in the launch agent and in the systemd unit), its version is a release (`vMAJOR.MINOR.PATCH`), and following is switched on. A Features frame sent again because bit 8 changed does not count as announcing bit 0 again for the clipboard (ADR 8): only a change of bit 0 does.
- Control code 17, Version, goes both ways right after Features, with the version in its value: major × 65536 + minor × 256 + patch, minor and patch at most 255. A build that is not a release sends 0.
- Control code 16, Update, goes from the central to a side machine that has bit 8 set and a lower version: once after the Version of the side machine arrives, and again if bit 8 turns on later in the connection. Its value is the version of the central.
- The side machine downloads and checks that version at once. It replaces and restarts only when it does not have focus, has answered the last leave (ADR 8), and has no file transfer open (ADR 13).
- A side machine never moves to a version below its own.
- The central writes the version of each side machine in its status file, where `lanhop status` and `lanhop update` read it.

**The central goes first.** `lanhop update` on the central replaces it and restarts it. The side machines connect again, the central sees their lower version and sends Update. A side machine that was off gets it when it connects later.

## Consequences

- A release asset changed after it was published, a changed `SHA256SUMS`, an old release offered as a new one, or a download changed by a proxy that reads TLS (one machine of the desk runs Zscaler) is refused.
- Signing a program that lanhop takes needs the approval of the owner in the `release` environment, or the private key itself.
- The central can only ask for a version number, never for an address or a program. The worst a central can do to a side machine is move it to a newer signed release.
- The hello stays at version 1 for good. A later change to the protocol has to come through Features bits, or keep accepting version 1 long enough to send Update.
- Machines on 0.1.0 and 0.1.1 ignore codes 16 and 17 and never set bit 8. They need the installer line once to reach the first release that updates, which also sets `LANHOP_SERVICE=1`.
- Side machines need to reach github.com when they update.
- A release that does not start leaves the side machines that took it without lanhop until the installer line runs on each. The release workflow already installs each macOS archive and checks the app before publishing. Going back to the previous version by itself is out of scope.
- If the key is lost or leaks, a release with a new key goes out, and each machine takes it with the installer line once.

## Alternatives

- **Trust HTTPS to GitHub alone.** No key to keep, but anyone who can change a release of the project, or read TLS on the way, reaches every machine.
- **Sign the archives with the macOS certificate.** It covers only macOS, and the Linux archives would stay unsigned.
- **minisign or signify.** Well known formats, but they add a tool to the release workflow and to every machine that checks, where Go's standard library already verifies Ed25519.
- **The central sends the new program over the lanhop connection.** Side machines would not need the internet, but a central could then send any program, and the transport would carry large files before spec 011 adds a connection for them.
- **Each machine checks GitHub on its own every day.** Nothing to type at all, but every machine restarts at a moment nobody chose, and the keyboard and mouse pause then. It is out of scope.
- **`INVOCATION_ID` to tell that systemd started lanhop.** It needs no installer change, but a terminal that systemd starts can pass it on to the shells it opens (deduced, not measured), and a lanhop started by hand would then end after an update with nothing to start it again.
