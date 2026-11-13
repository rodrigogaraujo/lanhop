# 12. Joining with a line that carries the key of the central

Date: 2026-11-13 · Status: proposed

## Context

[Spec 009](../../specs/009-adding-a-machine/spec.md) wants a new machine to join the central with one line typed on it, and nothing typed on the central after `lanhop add`.

Pairing today ([ADR 4](0004-pinned-tls-and-pairing-by-comparison.md)) needs both machines in `lanhop pair` at once, and a person who compares two codes of 80 bits on both screens and types yes on both. ADR 4 notes that a shorter code would need a password-authenticated key exchange, which Go's standard library does not have.

In a join, the person carries text in one direction only: from the central to the new machine. Two things have to be true at the end:

- The new machine trusts the key of the real central, and no other.
- The central trusts the key of the machine that got the line, and no other.

## Decision

**The join code** has 120 bits, written as 24 characters of base32 in groups of four, such as `RF4K-Q7MZ-2XPA-HB3T-9W6N-C5LD`:

- The first 80 bits are the first 80 bits of the SHA-256 of the public key of the central: the same code as ADR 4 shows.
- The last 40 bits are a random token that `lanhop add` makes for this one join.

**The join line** is `lanhop join <address> <code>`, or the installer line with the same two values in `LANHOP_JOIN`. The address is the one the central reaches the local network with.

**The exchange**, on port 24746, the pairing port, over TLS 1.3 with the keys of both machines, as in ADR 4:

1. The new machine connects and checks that the key of the central matches the first 80 bits of the code. If not, it closes the connection and sends nothing.
2. Only then does it send the token, its own key being already known to the central through TLS.
3. The central checks the token in constant time. If it matches, it sends back the name it gives to the new machine, its own name, and its local name on the network (`name.local`), then stores the new machine. The new machine stores the central with those names and the address it used.
4. The token is then spent. A wrong token counts as one attempt. At the third, or after ten minutes, or at Ctrl+C, `lanhop add` stops listening.

**A connection that is not a join** (no TLS, a TLS that does not complete, or a first message that is not a join message within 10 seconds) is closed and does not count as an attempt, and `lanhop add` goes on waiting.

**Why this order.** The token travels only to a machine that has proved it holds the key of the central, inside TLS. A machine in the middle never sees it, so it cannot guess it at leisure. It can only guess while connected to the real central, three times, against 2^40 values.

## Consequences

- No cryptography of our own, and no dependency: TLS 1.3 and SHA-256 from the standard library, as ADR 4.
- A machine in the middle cannot join in place of the new machine: it never learns the token. It cannot pose as the central either: it would need a key whose hash matches 80 bits of a key it does not control, the same bar as ADR 4.
- Whoever reads the join line before it is used, within ten minutes, can join in place of the new machine. `lanhop add` prints the name and address of the machine that joined, and `lanhop forget` removes it. The line works once, so the real machine then fails to join, which shows that something is wrong.
- The line is 24 characters of code plus an address, longer to type than the codes of ADR 4 are to compare. A person who copies it through a chat or a note does not type it at all.
- `lanhop pair` stays, for a person who prefers to compare codes, and for machines on 0.1.x.
- `lanhop add` listens only while it waits, on the pairing port. `lanhop share` does not listen for joins.

## Alternatives

- **A password-authenticated key exchange with a short code** (SPAKE2, CPace). A code of 6 or 8 characters would do, but Go's standard library has neither, and writing one is cryptography of our own.
- **A short code and an HMAC of both keys.** No new primitive, but whichever side proves first gives a machine in the middle a value to test codes against offline. A code short enough to type, 40 bits for 8 characters, likely falls within the ten minutes on one graphics card (deduced, not measured).
- **The central sends its key only, and the new machine is accepted without a token.** One code less, but any machine on the network could join during the wait.
- **Comparing codes, as ADR 4, but with `lanhop share` waiting instead of `lanhop pair`.** It removes the second terminal on the central, and keeps a person at both screens.
