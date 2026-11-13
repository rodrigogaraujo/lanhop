# 7. Copies travel in control frames, and the protocol version stays 1

Date: 2026-11-13 · Status: accepted

## Context

Clipboard sharing ([spec 005](../../specs/005-clipboard-sharing/spec.md)) sends text between machines. Until now the connection carried only fixed frames of 8 bytes ([ADR 2](0002-linux-input-events-as-wire-format.md)): input events, and control frames of type `0xFF00` for hello, ping, enter and leave.

A person updates one machine at a time, so a machine with clipboard sharing will meet machines on lanhop 0.1.0. In 0.1.0:

- Both ends send the protocol version in the hello and close the connection when the versions differ.
- A receiver ignores a control frame whose code it does not know (`handle` in `internal/receiver`, covered by `TestUnknownControlEventsAreIgnored`).
- A sharer reads what a receiver sends only to notice that the connection is lost, and drops every frame (`watch` in `internal/sharer`).

## Decision

Copies travel as control frames in the same 8-byte format:

- **Features**, code 5: bit 0 of the value says that the machine takes and gives copies. Each machine sends it after it connects, and again when its switch changes.
- **Clipboard**, code 6: the value is the length of the text in bytes, from 1 to 1 MiB.
- **Clipboard data**, code 7: the value holds the next 4 bytes of the text, big-endian. The last frame is padded with zeros.

A machine sends copies only to a machine that has said, with Features, that it takes them. Other frames may come between the frames of a copy. A new Clipboard frame drops a copy that has not finished, and a data frame outside a copy is dropped. The sender sends a copy in slices, so that input waits at most one slice.

The protocol version stays 1.

## Consequences

- A machine on 0.1.0 keeps working with a newer one, in either role. It ignores Features and never says that it takes copies, so no copy is sent to it.
- No length prefix or new frame format: the reader still reads 8 bytes at a time.
- Half of the bytes on the wire are framing. A copy of 1 MiB takes 2 MiB.
- A change that 0.1.0 cannot ignore still needs a new protocol version, and then the two versions cannot connect.

## Alternatives

- **Protocol version 2 with frames of any length.** Cheaper on the wire, but a 0.1.0 machine closes the connection when the versions differ, so every machine would have to be updated at the same time.
- **A second connection for copies.** It needs another port, another rule in each firewall and, on macOS, another trip through the Local Network permission, for no gain on a local network.
