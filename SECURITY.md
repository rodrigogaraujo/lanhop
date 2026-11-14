# Security Policy

lanhop carries keystrokes between machines, so security reports are taken seriously.

## Reporting a vulnerability

**Do not open a public issue.**

Use GitHub's private reporting: go to the [Security tab](https://github.com/rodrigogaraujo/lanhop/security/advisories/new) and choose "Report a vulnerability". If you cannot use it, write to rodrigoaraujo990@gmail.com.

Include what you found, how to reproduce it and what an attacker gains from it.

You should get a first answer within 7 days. Please give us a reasonable time to release a fix before you publish details.

## Supported versions

There is no stable release yet. Fixes land on the `main` branch.

## How releases are signed

From v0.1.2 on, each release carries `SHA256SUMS.sig`, an Ed25519 signature of `SHA256SUMS` by the release key of the project. `SHA256SUMS` lists the archives and `UPDATE`, which names the version of the release and the oldest version that can move to it without the installer. `lanhop update` replaces nothing unless the signature is made by a key compiled into lanhop, and the files match it ([ADR 11](docs/adr/0011-signed-releases-and-side-machines-that-follow-the-central.md)).

From v0.1.2 on, `lanhop.app` is signed with a self-signed certificate of the project, not by Apple. `lanhop update` on a Mac takes a new app only when its signature names the identifier `com.github.rodrigogaraujo.lanhop` and a certificate whose hash is compiled into lanhop ([ADR 10](docs/adr/0010-a-stable-signature-from-a-certificate-of-the-project.md)).

- Only the release workflow signs, in a GitHub environment that only `v` tags reach, and only after the owner approves each run. Pull requests and manual runs sign with a key and a certificate made for that run and thrown away.
- lanhop carries a second key and a second certificate hash, kept offline, so that a lost or leaked key can be replaced by an update.
- The installer still checks the archive only against the `SHA256SUMS` of the same release, over HTTPS, and does not check the signature yet.

A way to sign a program that lanhop takes, or to make lanhop take a release that the project did not sign, counts as a vulnerability.

## What counts

- Reading or injecting input without having been paired
- Weaknesses in the encryption or in the pairing
- Privilege escalation through the daemon, its device permissions or its configuration files
- Crashes or memory problems triggered by data from the network
