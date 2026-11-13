# 7. Copies travel in control frames, and the protocol version stays 1

Date: 2026-11-13 · Status: accepted

## Context

Clipboard sharing ([spec 005](../../specs/005-clipboard-sharing/spec.md)) sends text between machines. Until now the connection carried only fixed frames of 8 bytes ([ADR 2](0002-linux-input-events-as-wire-format.md)): input events, and control frames of type `0xFF00` for hello, ping, enter and leave.

A person updates one machine at a time, so a machine with clipboard sharing will meet machines on lanhop 0.1.0. Read in the code of 0.1.0:

- Both ends send the protocol version in the hello and close the connection when the versions differ.
- A receiver ignores a control frame whose code it does not know (`handle` in `internal/receiver`, covered by `TestUnknownControlEventsAreIgnored`).
- A sharer reads what a receiver sends only to notice that the connection is lost, and drops every frame (`watch` in `internal/sharer`).

A copy also shares the connection with keys and mouse movement. The operating system accepts megabytes into the send buffer of a connection at once. A key sent after a copy waits until the whole copy has left. Measured on Linux with a 1 MiB copy over TLS on loopback, with the reading side slowed down on purpose: a key sent right after the copy started arrived 147 ms late at 100 Mbit/s and 818 ms late at 20 Mbit/s. Splitting the copy into slices did not help, because every slice was already in the send buffer.

## Decision

Copies travel as control frames in the same 8-byte format:

- **Features**, code 5: bit 0 of the value says that the machine takes and gives copies. Each machine sends it after it connects, and again when its switch changes.
- **Clipboard**, code 6: the value is the length of the text in bytes, up to 1 MiB. A length of 0 is a withdrawal: the clipboard of the sending machine changed to something lanhop does not carry.
- **Clipboard data**, code 7: the value holds the next 4 bytes of the text, big-endian. The last frame is padded, and the receiving side ignores the padding.
- **Clipboard ack**, code 8: the value is how many Clipboard and Clipboard data frames the receiving side has read on this connection so far. It is sent every 512 such frames, and whenever no copy is left half read.

A machine sends copies only to a machine that has said, with Features, that it takes them. Other frames may come between the frames of a copy. A new Clipboard frame drops a copy that has not finished, and a data frame outside a copy is dropped.

The sender never has more than 4096 copy frames, 32 KiB on the wire, that the other side has not acknowledged. A key that follows a copy waits at most for those 32 KiB: about 13 ms at 20 Mbit/s.

The protocol version stays 1.

## Consequences

- A machine on 0.1.0 keeps working with a newer one, in either role. It ignores Features and never says that it takes copies, so no copy and no acknowledgement is sent to it.
- No length prefix or new frame format: the reader still reads 8 bytes at a time.
- Half of the bytes on the wire are framing. A copy of 1 MiB takes 2 MiB.
- A copy moves at most 32 KiB per round trip. On a wired network, with a round trip under 1 ms, the speed of the link is the limit. On a Wi-Fi link with a round trip of 10 ms, a copy of 1 MiB takes about 0.7 s.
- A change that 0.1.0 cannot ignore still needs a new protocol version, and then the two versions cannot connect.

## Alternatives

- **Frames of any length, switched on by a Features bit.** The hello stays at version 1, so 0.1.0 still connects, and a copy no longer doubles in size. Both readers then change format in the middle of a connection, after Features arrives. Every reader has to track that switch, and a frame sent just before it is read the wrong way. Doubling a copy costs little at these sizes, so the fixed frames win on simplicity.
- **A second connection for copies.** The same listener could accept it, and macOS would not ask again, because the Local Network permission belongs to the app. Keys would never wait behind copy bytes in the same connection. It needs a second TLS connection per machine, a way to tie it to the first, and reconnection for both. The acknowledgement window keeps keys waiting at most for 32 KiB, which is enough.
- **Limit the send buffer** with `TCP_NOTSENT_LOWAT`. A throwaway test on Linux brought the delay of a key to 11 ms at 100 Mbit/s and 49 ms at 20 Mbit/s with a limit of 16 KiB. It was not measured on macOS, and how it behaves depends on each system. The acknowledgements work the same everywhere.
