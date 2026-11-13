# 12. Joining with a line that carries the key of the central

Date: 2026-11-13 · Status: proposed · Amends: [ADR 4](0004-pinned-tls-and-pairing-by-comparison.md)

## Context

[Spec 009](../../specs/009-adding-a-machine/spec.md) wants a new machine to join the central with one line typed on it, and nothing typed on the central after `lanhop add`.

Pairing today ([ADR 4](0004-pinned-tls-and-pairing-by-comparison.md), and FR-005 of spec 001) needs both machines in `lanhop pair` at once, and a person who compares two codes of 80 bits on both screens and accepts on both. ADR 4 notes that a shorter code would need a password-authenticated key exchange, which Go's standard library does not have.

In a join, the person carries text in one direction only: from the central to the new machine. Two things have to be true at the end:

- The new machine trusts the key of the real central, and no other.
- The central trusts the key of the machine that got the line, and no other.

## Decision

A join is a second way to pair, next to the comparison of ADR 4, which stays.

**The join code** has 120 bits, written as 24 characters of base32 in groups of four, such as `RF4K-Q7MZ-2XPA-HB3T-W6NC-5LDE`. It is read without regard to case, with or without the dashes.

- The first 80 bits are the first 80 bits of the SHA-256 of the public key of the central: the same code that ADR 4 shows.
- The last 40 bits are a random token that `lanhop add` makes for this one join.

**The join line** is `lanhop join <address> <code>`, or the installer line with the same two values in `LANHOP_JOIN`.

**The exchange**, on port 24746, the pairing port, over TLS 1.3 with the keys of both machines, as in ADR 4:

1. The new machine checks, during the TLS handshake (`VerifyConnection`, as `internal/transport` does), that the key of the central matches the first 80 bits of the code. If not, the handshake fails and the new machine sends nothing more.
2. The central sends its name. The new machine refuses, before it sends the token, when it would not be able to store the central: it already trusts another machine under that name, or it already trusts a central with an address.
3. The new machine sends the token. Its key is already known to the central, from the same TLS connection.
4. Under one lock, the central checks the token in constant time, refuses its own key, a key it already trusts and a name taken in the meantime, spends the token, stores the new machine, and only then sends Welcome. The new machine stores the central with its name and the address it used.
5. A wrong token counts as one attempt. At the third, after ten minutes, at Ctrl+C or after a join, `lanhop add` stops listening.

If the central stored the new machine and the new machine never got Welcome, `lanhop forget NAME` on the central and a new `lanhop add` start over.

**The wait cannot be held.** `lanhop add` serves each connection on its own, with a deadline of 10 seconds from accept that covers the TLS handshake and the first message, and at most eight connections at once. A connection that sends no Join message within that time is closed and does not count as an attempt.

**Why this order.** The token travels only to a machine that has proved it holds the key of the central, inside TLS. A machine in the middle never sees it, so it cannot guess it at leisure. It can only guess while connected to the real central, three times, against 2^40 values. Go checks that each side holds the key it presents, even when the chain is not checked, so the key that the central stores is the key of the machine that sent the token.

## Consequences

- No cryptography of our own, and no dependency: TLS 1.3 and SHA-256 from the standard library, as ADR 4.
- A machine in the middle cannot join in place of the new machine: it never learns the token. It cannot pose as the central either: it would need a key whose hash matches 80 bits of a key it does not control, the same bar as ADR 4.
- Whoever reads the join line before it is used, within ten minutes, can join in place of the new machine, and then gets every key, copy and file sent while focus is on that machine. Whoever can change the line on its way controls what the new machine installs, as with any line piped to `sh`. `lanhop add` prints the name and address of the machine that joined, and `lanhop forget` removes it. The line works once, so the real machine then fails to join, which shows that something is wrong.
- Any machine on the network can end a wait with three wrong tokens. It fails closed: nothing joins, and `lanhop add` prints the addresses the wrong tokens came from.
- The line is 24 characters of code plus an address, longer to type than the codes of ADR 4 are to compare. A person who copies it through a chat or a note does not type it at all.
- `lanhop add` listens only while it waits. `lanhop share` does not listen for joins.
- Spec 001 FR-005 changes to allow this second way.

## Alternatives

- **A password-authenticated key exchange with a short code** (SPAKE2, CPace). A code of 6 or 8 characters would do, but Go's standard library has neither, and writing one is cryptography of our own.
- **A short code and an HMAC of both keys.** No new primitive, but whichever side proves first gives a machine in the middle a value to test codes against offline. A code short enough to type, 40 bits for 8 characters, likely falls within the ten minutes on one graphics card (deduced, not measured).
- **The central sends its key only, and the new machine is accepted without a token.** One code less, but any machine on the network could join during the wait.
- **Comparing codes, as ADR 4, but with `lanhop share` waiting instead of `lanhop pair`.** It removes the second terminal on the central, and keeps a person at both screens.
