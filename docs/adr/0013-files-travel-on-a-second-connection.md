# 13. Files travel on a second connection

Date: 2026-11-13 · Status: proposed

## Context

File sharing ([spec 011](../../specs/011-file-sharing/spec.md)) moves files of up to 4 GiB between machines. Text copies travel on the connection of the keyboard and mouse, in 8-byte control frames with an acknowledgement window of 32 KiB ([ADR 7](0007-clipboard-in-control-frames.md)). That suits texts of up to 1 MiB and does not suit files:

- Each 4 bytes of content take 8 bytes on the wire, so a 4 GiB copy would take 8 GiB.
- The window lets 32 KiB move per round trip. On a Wi-Fi link with a round trip of 5 ms that is about 6 MiB/s, so 100 MiB would take about 30 s. A larger window would speed it up and make keys wait longer behind file bytes.

Side machines open their connection to the central, and the central does not open connections to them.

## Decision

- **Announcing**: Features bit 1 says that a machine takes and gives files. Only machines that set it take part. A file copy is announced on the connection of the keyboard and mouse with a few control frames, codes 10 to 15: a transfer number, the number of files and their total size. Codes from 16 up are left to other features.
- **Second connection**: the bytes travel on a second TLS connection to the same port of the central, always opened by the side machine, whichever way the files go. It uses the pinned keys of [ADR 4](0004-pinned-tls-and-pairing-by-comparison.md), so only a paired machine can open it.
- **First frame**: instead of the hello, the second connection starts with a control frame, code 10, carrying the transfer number. The central accepts it only from the machine the transfer was announced to or from, and only for a minute after the announcement. A central on 0.1.x sees an unknown first frame and closes the connection, so no such connection is opened toward it.
- **Content**: on the second connection, the machine that has the files sends, for each file, its relative path and size, then its content, then its SHA-256. The receiving side writes under a temporary name, checks size and digest, and renames. The connection closes when the last file is through, or at the first error, and nothing cut short is kept.
- **Pace**: TCP alone sets the pace of the second connection. The connection of the keyboard and mouse is not involved, so keys never wait behind file bytes in the same stream.
- **Side machine to side machine**: through the central, which receives into its arrival folder and sends on, because side machines do not connect to each other.

## Consequences

- Files move at the speed of the network, without doubling.
- `internal/transport` learns a second kind of connection, and the listener tells the two apart by their first frame.
- The central holds files passing between side machines for as long as it takes to pass them on.
- On a busy Wi-Fi link, a large file still competes with keys for the air, in a separate stream. Task 2 of the plan times keys during a transfer of 100 MiB (SC-002).
- A machine that does not announce files keeps working as before, including machines on 0.1.x.

## Alternatives

- **Control frames with the window of ADR 7.** No change to the transport, but files double in size and move at 32 KiB per round trip.
- **A larger window for files only.** Faster, but keys would wait behind up to that many bytes in the same connection.
- **The central opening connections to side machines.** Side machines would have to listen on a port, which needs another firewall rule and another Local Network permission on macOS.
