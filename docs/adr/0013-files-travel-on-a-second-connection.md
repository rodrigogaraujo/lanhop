# 13. Files travel on a second connection

Date: 2026-11-13 · Status: proposed

ADRs 10 to 12 are reserved for specs 009 and 010.

## Context

File sharing ([spec 011](../../specs/011-file-sharing/spec.md)) moves files of up to 4 GiB between machines. Text copies travel on the connection of the keyboard and mouse, in 8-byte control frames with an acknowledgement window of 32 KiB ([ADR 7](0007-clipboard-in-control-frames.md)). That suits texts of up to 1 MiB and does not suit files:

- Each 4 bytes of content take 8 bytes on the wire, so a 4 GiB copy would take 8 GiB.
- The window lets 32 KiB of frames, 16 KiB of content, move per round trip. On a Wi-Fi link with a round trip of 5 ms that is about 3 MiB/s of content, so 100 MiB would take about 30 s. A larger window would speed it up and make keys wait longer behind file bytes in the same stream.

Side machines open their connection to the central, and the central does not open connections to them.

## Decision

**Features.** The value of Features is a set of bits, and a reader ignores the bits it does not know. Bit 0 is text (ADR 7). Bit 1 says that a machine takes and gives files. Only machines that set bit 1 on both ends take part. Bit 2 says that a machine can receive a dragged file: it is set only where lanhop can start a drag, on macOS or on Linux in an X11 session, and only while file sharing is on. Bits 3 to 7 are left for clipboard and file sharing, and bits 8 to 15 belong to spec 010.

**Control codes 10 to 15**, all of type `0xFF00`:

| Code | Name | Where | Value |
|---|---|---|---|
| 10 | Data hello | First frame of a data connection, instead of the hello, and the central's answer when it accepts it | The transfer number |
| 11 | File copy | Control connection, the answer of a side machine to a leave, instead of a text | The number of files and folders |
| 12 | File copy size | Right after File copy | The total size, in KiB, rounded up |
| 13 | Open data | Control connection, from the central to a side machine | A new transfer number, and the way: bit 31 set means the side machine sends, clear means it receives |
| 14 | Data stop | Control connection, either way | The transfer number to stop |
| 15 | Data ack | Data connection, from the side that reads back to the side that writes | Bits 0 to 30: the number of blocks of 32 KiB read so far. Bit 31 set: every file has arrived whole, and no acknowledgement follows |

**The answer to a leave stays light.** A side machine that holds a file copy answers with File copy and File copy size, worked out within the time an answer has (ADR 8). It reads no content and computes no digest at that moment. A list that cannot be counted in that time is not sent, and the log says that it was too slow to list, apart from a list over the limit.

**Second connection.** File bytes travel on a second TLS connection to the same port of the central, with the pinned keys of [ADR 4](0004-pinned-tls-and-pairing-by-comparison.md), and always opened by the side machine.

- When the central needs files from a side machine, or has files for one, it sends Open data with a new random transfer number. The side machine opens the data connection and starts it with Data hello and that number. The central answers with the same frame when it accepts the connection, so the side machine knows before any file byte moves. The answer does not promise that the central still waits: if it stopped waiting at that moment, the connection ends right after the answer, and the side machine takes that end as a stop of the transfer.
- The central accepts a data connection only from the machine it sent that number to, within 10 seconds of sending it, and only once. Anything else closes the connection.
- A central on 0.1.x sees an unknown first frame and closes the connection. No machine opens one toward it, because it never sets bit 1.

**Content.** The machine that has the files sends, for each file or folder, its relative path, its kind and its size, then the content, then a SHA-256 of the content worked out while sending it. It compares the size and modification time of each file before and after reading it, and stops the transfer if the file changed. The receiving side says on the same connection when every file has arrived whole, so that the machine that sent knows the copy is held there. That answer is a Data ack with bit 31 set. Before it, the reading side sends a Data ack for each 32 KiB it reads, which tells the writing side how fast the bytes get through. Each data connection carries files one way only, and its way back carries only these Data acks.

**Receiving safely.** The receiving side refuses absolute paths, `..`, links and special files; creates every file without following links, under a temporary name in the arrival folder; refuses a total size over the limit or over the free space; checks size and digest; and renames only when a file is whole. When the transfer stops, at a Data stop, at a lost connection on either the data or the control connection, or at any error, what arrived of that transfer is removed. Focus leaving the receiving machine stops a transfer too, except one that serves a drop already accepted there. A stopped transfer starts again from the beginning when it is needed again.

**Pace.** TCP sets the pace of the data connection, and each machine keeps what it writes on all its data connections together to 80 Mbit/s of TLS content, about 84 Mbit/s on the wire, in bursts of at most 128 KiB. Keys never wait behind file bytes in the same stream. They can still wait behind them in a queue shared by both streams, such as a Wi-Fi link or a router. Measured on Linux over TLS for task 2, with both connections behind one first-in first-out queue of 1000 packets drained at 100 Mbit/s: without the limit, 100 MiB kept the queue full and keys arrived 123 ms late; with it, keys arrived at most 8 ms late, and the 100 MiB took 10.5 s instead of 8.9 s. The limit helps only on a link faster than itself: behind the same queue drained at 50 Mbit/s, keys arrived 245 ms late. Task 2b of the plan makes the pace follow the delay.

**Side machine to side machine.** Through the central, which streams from one data connection to the other without waiting for whole files, and keeps a copy only when it has focus itself and will paste it.

## Consequences

- Files move without doubling, at the speed of the network up to 80 Mbit/s per machine. A copy of 4 GiB takes about 7 minutes even on a faster network.
- `internal/transport` learns a second kind of connection, and the listener tells the two apart by their first frame.
- A peer can only write inside the arrival folder of the other, and only as much as the limits allow.
- The work on `lanhop update` uses codes from 16 up on the existing connection, and does not touch the listener.

## Alternatives

- **Control frames with the window of ADR 7.** No change to the transport, but files double in size and move at 16 KiB of content per round trip.
- **A larger window for files only.** Faster, but keys would wait behind up to that many bytes in the same connection.
- **The central opening connections to side machines.** Side machines would have to listen on a port, which needs another firewall rule and another Local Network permission on macOS.
