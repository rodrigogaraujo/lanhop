# 4. Pinned TLS 1.3, and pairing by comparing codes

Date: 2026-11-11 · Status: accepted · Amended by: [ADR 12](0012-joining-with-a-line-that-carries-the-key-of-the-central.md)

## Context

lanhop carries every keystroke, passwords included, across a network that may have other devices listening or trying to connect. Both sides must know who they are talking to, and nobody else may read or inject input. The machines sit on the same desk and have no certificate authority.

## Decision

- Each machine creates an Ed25519 key and a self-signed certificate on first use.
- Connections use TLS 1.3 from the Go standard library, with a certificate on both sides. Each side accepts only a public key it has stored at pairing. The certificate chain and names are not checked, because the stored key is the identity.
- There is no option to run without encryption.
- To pair, each machine shows a code made from the first 80 bits of the SHA-256 of a public key: its own and the one it received. A person compares the codes on both screens and accepts on both.

## Consequences

- No cryptography of our own, and no dependency.
- A machine in the middle cannot pair: to show matching codes it would need a key whose hash collides in 80 bits with a key it does not control.
- Pairing needs a person at both machines, which fits a desk and does not fit remote setups.
- A shorter code, such as 6 digits, would need a password-authenticated key exchange, which the standard library does not have. It can be added later without changing the stored keys.
- Certificates never expire in practice. A machine is distrusted by removing it from the list, not by expiry.
