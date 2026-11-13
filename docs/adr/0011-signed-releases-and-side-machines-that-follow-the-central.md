# 11. Signed releases, and side machines that follow the central

Date: 2026-11-13 · Status: proposed

## Context

[Spec 010](../../specs/010-updates/spec.md) wants one command on the central to update every machine. lanhop sees every key typed and can type on every machine, so a program that replaces it has to come from the project and nowhere else.

What exists today:

- Each release has four archives and `SHA256SUMS`. The installer checks the archive it downloads against `SHA256SUMS` of the same release. Both files come from the same place, so the check catches a broken download and not a changed release.
- Side machines open their connection to the central. The hello carries protocol version 1, and the version has not changed since 0.1.0.
- Read in the code of 0.1.1: a side machine ignores control codes it does not know, and the Features frame is a set of bits whose unknown bits are ignored ([ADR 7](0007-clipboard-in-control-frames.md), [ADR 13](0013-files-travel-on-a-second-connection.md)).
- The service restarts lanhop when it ends with an error: `KeepAlive` with `SuccessfulExit` false in the launch agent, `Restart=on-failure` in the systemd unit.

## Decision

**Signed checksums.** The release workflow signs `SHA256SUMS` with an Ed25519 key of the project and publishes the signature as `SHA256SUMS.sig`, 64 bytes. lanhop carries the public key in its code. The private key is a GitHub Actions secret that only the release workflow reads, only on a tag, and the owner keeps a copy outside GitHub. Go's standard library verifies Ed25519, so lanhop keeps no dependencies ([ADR 3](0003-go-with-no-dependencies.md)).

**Fixed addresses.** lanhop downloads only from `https://github.com/rodrigogaraujo/lanhop/releases/download/v<version>/`. Only the version comes from outside: from the address that `releases/latest` sends to, or from the central.

**Checks before replacing**, in this order. Any failure leaves the old program as it was.

1. `SHA256SUMS.sig` is a valid signature of `SHA256SUMS` by the key in the running program.
2. The archive matches its line in `SHA256SUMS`.
3. The archive unpacks into a new folder beside the program, on the same disk, with no absolute path, no `..` and no link that points outside.
4. On macOS, the new lanhop.app meets the code requirement of the running one ([ADR 10](0010-a-stable-signature-from-a-certificate-of-the-project.md)).
5. The file `UPDATE` of the release, listed in `SHA256SUMS`, says `from <version>`: the oldest version that can move to this one without the installer. A machine older than that stops and prints the installer line.

**Replacing** is a rename on the same disk: the new program, or lanhop.app, takes the place of the old one in one step, and the old one is removed after. Then lanhop ends with exit status 3, which the service takes as an error and answers with a restart. The new program starts at the same path, so the launch agent, the systemd unit and the link in `~/.local/bin` stay as they are.

**Side machines follow the central.**

- Bit 2 of Features says that a machine follows the central. A side machine sets it only when it runs as its service, has a release version and has following switched on.
- Control code 17, Version, goes both ways right after Features, with the release version in its value: major × 65536 + minor × 256 + patch. A `dev` build sends 0.
- Control code 16, Update, goes from the central to a side machine that set bit 2 and sent a lower version. Its value is the version of the central. The side machine downloads and checks that version, waits until it does not have focus, replaces and restarts.
- A side machine never moves to a version below its own.
- The central writes the version of each side machine in its status file, where `lanhop status` and `lanhop update` read it.

**The central goes first.** `lanhop update` on the central replaces it and restarts it. The side machines connect again, the central sees their lower version and sends Update. A side machine that was off gets it when it connects later.

## Consequences

- A changed archive, a changed `SHA256SUMS` or a release without a valid signature is refused. Publishing a release that lanhop takes needs both the key and write access to the repository.
- The central can only ask for a version number, never for an address or a program. The worst a central can do to a side machine is move it to a newer signed release.
- The hello stays at version 1 for good. A later change to the protocol has to come through Features bits, or keep accepting version 1 long enough to send Update.
- Machines on 0.1.0 and 0.1.1 never set bit 2 and ignore codes 16 and 17. They need the installer line once to reach the first release that updates.
- Side machines need to reach github.com when they update.
- If the key is lost or leaks, a release with a new key goes out, and each machine takes it with the installer line once.

## Alternatives

- **Trust HTTPS to GitHub alone.** No key to keep, but anyone who can change a release of the project, or the account behind it, reaches every machine.
- **Sign the archives with the macOS certificate.** It covers only macOS, and the Linux archives would stay unsigned.
- **minisign or signify.** Well known formats, but they add a tool to the release workflow and to every machine that checks, where Go's standard library already verifies Ed25519.
- **The central sends the new program over the lanhop connection.** Side machines would not need the internet, but a central could then send any program, and the transport would carry large files before spec 011 adds a connection for them.
- **Each machine checks GitHub on its own every day.** Nothing to type at all, but every machine restarts at a moment nobody chose, and the keyboard and mouse pause then. It is out of scope.
