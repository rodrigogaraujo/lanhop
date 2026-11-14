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

From v0.1.2 on:

- Each release carries `SHA256SUMS.sig`, an Ed25519 signature of `SHA256SUMS` by the release key of the project. `SHA256SUMS` lists the archives and `UPDATE`, which names the version of the release and the oldest version that can move to it without the installer.
- lanhop, through `lanhop update` or on a side machine that follows its central, replaces nothing unless `SHA256SUMS` carries a valid signature by a key compiled into lanhop, the archive and `UPDATE` match `SHA256SUMS`, and `UPDATE` names the version asked for. It moves to an older release only when `lanhop update --older` is typed on that machine ([ADR 11](docs/adr/0011-signed-releases-and-side-machines-that-follow-the-central.md)).
- `lanhop.app` is signed with a self-signed certificate of the project, not by Apple. lanhop takes a new app only when its signature names the identifier `com.github.rodrigogaraujo.lanhop` and a certificate whose hash is compiled into lanhop ([ADR 10](docs/adr/0010-a-stable-signature-from-a-certificate-of-the-project.md)).
- Only the release workflow signs, in a GitHub environment that only `v` tags reach. Only the owner can push such a tag, and each signing job waits for the owner's approval. Pull requests and manual runs sign with a key and a certificate made for that run and thrown away.
- lanhop also carries the public part of a second key and the hash of a second certificate, whose private keys the owner keeps offline. A leaked or lost key is replaced by a release signed with the second one. A machine trusts the leaked key until it takes that release, and each Mac asks for Accessibility once more when the certificate changes.
- The installer still checks the archive only against the `SHA256SUMS` of the same release, over HTTPS, and does not check the signature yet.

A way to sign a program that lanhop takes, or to make lanhop take a release that the project did not sign, counts as a vulnerability.

## What counts

- Reading or injecting input without having been paired
- Weaknesses in the encryption or in the pairing
- Privilege escalation through the daemon, its device permissions or its configuration files
- Crashes or memory problems triggered by data from the network
