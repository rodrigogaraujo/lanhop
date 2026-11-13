# Implementation Plan: Clipboard sharing

**Spec**: [spec.md](spec.md) · **Created**: 2026-11-13 · **Status**: Draft

## Goal

Text copied on one machine is on the clipboard of the next machine that gets focus. Copies travel over the connection that already carries the keyboard and mouse, in frames that lanhop 0.1.0 ignores, and pass through the central.

## How a copy moves

```
side machine A               central                         side machine B
copy "hello"
   │
focus leaves A ◄──── leave ──┤
                             ├──── enter ──────────────────► focus on B: keys and mouse go to B
read the clipboard           │
(only if it changed)         │
   ├──── "hello" ───────────►│ the desk's last copy = "hello"
                             ├──── "hello" ────────────────► write the clipboard
                             │                                paste gives "hello"
```

- **Only machines that take part.** A machine takes part while its own switch is on and the other end's last Features has bit 0 set. A side machine on 0.1.0, or switched off, is never read, never waited for and never sent a copy frame. A side machine under a central on 0.1.0, or switched off, reads nothing.
- **A copy is read when focus leaves the machine where it was made**, and only if the clipboard of that machine changed since lanhop last read it or wrote it, or since the machine started to take part: at start, when its switch was turned on, or when the other end announced bit 0 again. The central reads its own clipboard when focus leaves it.
- **A side machine answers every leave**: with the copy it read, with a withdrawal when the change is empty, not text or over 1 MiB, or with "unchanged". A Features frame with bit 0 clear also counts as its answer.
- **The central keeps the desk's last copy in memory**, with a number that grows with each copy, and remembers which number each machine holds. A withdrawal, or a change on the central that lanhop does not carry, empties it, and the other clipboards stay as they are.
- **The machine that has focus gets the desk's last copy if it does not hold it yet**: when focus arrives, when it starts to take part while it has focus, and at once when a new copy reaches the central while it has focus. The central hands out only once every side machine that focus left has answered and the central has read its own clipboard when focus left it. That is true for the central as well as for the side machines.
- **No answer is a withdrawal.** A side machine that does not answer within the silence limit, or whose connection is lost first, empties the desk's last copy. Its answer still counts if it comes later.
- **A copy is written unless the person copied something on that machine after focus arrived there**, or after lanhop last wrote there if that is later. That copy is newer, and moves when focus leaves.
- **Clipboards are read and written apart from the input path**, so a slow clipboard never holds up keys or acknowledgements.
- **A copy that is cut short is not held.** When focus leaves a side machine while a copy is on its way to it, the central stops sending and does not count the copy as held. A side machine writes a copy only while it has focus.
- **A machine never gets back its own copy**, because the central records that the machine where the copy was made already holds it. A copy equal to the desk's last copy is not a new copy.

The crossing does not wait for a copy. Keys and the mouse move to B at once, and the copy follows the answer of A: the leave reaches A, A reads its clipboard, and the copy makes two trips over the network. SC-001 sets the limit, 300 ms for 64 KiB, and task 6 times it. A paste pressed in the same instant may still get the previous text.

## Messages on the wire

Five new control frames, in the 8-byte format of [ADR 2](../../docs/adr/0002-linux-input-events-as-wire-format.md). See [ADR 7](../../docs/adr/0007-clipboard-in-control-frames.md).

| Frame | Type | Code | Value |
|---|---|---|---|
| Features | `0xFF00` | 5 | Bit 0 set: this machine takes and gives copies |
| Clipboard | `0xFF00` | 6 | Length of the text in bytes, up to 1048576. 0 is a withdrawal |
| Clipboard data | `0xFF00` | 7 | The next 4 bytes of the text, big-endian. The padding of the last frame is ignored |
| Clipboard ack | `0xFF00` | 8 | How many Clipboard and Clipboard data frames this side has read on the connection |
| Clipboard unchanged | `0xFF00` | 9 | Answer to a leave: the clipboard has not changed since lanhop last read or wrote it |

- Each machine sends Features right after it connects, and again when its switch changes. The central sends copies only to side machines that said they take them, and takes copies only from them.
- A copy is a Clipboard frame followed by its data frames. Other frames may come between them: input, pings, enter and leave. The receiving side collects data frames until it has the length that was announced.
- A new Clipboard frame drops a copy that has not finished. A data frame outside a copy is dropped.
- Each side counts the Clipboard and Clipboard data frames it sends and reads, once for the whole connection. A withdrawal counts as one frame. The reading side counts every one of them, whatever its switch, its focus or its clipboard, refused ones too, and acknowledges every 512 of them and whenever no copy is left half read.
- The sender never has more than 4096 copy frames, 32 KiB on the wire, that are not acknowledged, and sends slices of 512 frames as the acknowledgements come back. Without that window, Linux took a whole 1 MiB copy into the send buffer of the connection at once, and a key sent after it waited for all of it: 147 ms at 100 Mbit/s and 818 ms at 20 Mbit/s, measured over TLS behind a reader slowed down on purpose. With it, the same key waited 1.6 ms and 11.2 ms.
- A sender that waits for room longer than the silence limit, or gets an acknowledgement of frames it never sent, closes the connection. Both counts start again on the next one.
- The protocol version stays 1. lanhop 0.1.0 ignores control frames it does not know.

## Reading and writing the clipboard

Each system provides three operations: a mark that changes with every copy and can be taken without reading the content, read the text, and write the text. lanhop keeps two marks per machine: the one after its last read or write, or from when the machine started to take part, which tells whether there is something to read when focus leaves, and the later of the one when focus arrived and the one after its last write, which tells whether the person copied something since.

### macOS

`NSPasteboard`, the general pasteboard, through cgo:

- **Mark**: `changeCount`.
- **Read**: the string of type `NSPasteboardTypeString`. Formatted text also offers that type, with the plain text.
- **Write**: `clearContents`, then `setString:forType:`.

Newer versions of macOS may show an alert when a program reads what another program copied without a paste from the person. `changeCount` does not read the content. Whether macOS 15 or 26 show such an alert to lanhop running as a launch agent is not measured yet. Task 3 measures it. If macOS asks, the installer of [spec 008](../008-permissions-checked/spec.md) has to ask for it too.

### Linux, open point

There is no clipboard in the kernel. The clipboard belongs to the desktop session, and the session is X11 or Wayland. The spec asks for both, without a Go dependency.

| Way | Works on | Cost |
|---|---|---|
| The X11 selection `CLIPBOARD`, spoken over the X socket in pure Go | X11 sessions, and Wayland sessions that run Xwayland, which GNOME and KDE Plasma do. The compositor keeps the Wayland clipboard and the X11 one in step | About 600 lines of X11 protocol: owning a selection, answering requests from other programs, and the `INCR` transfer for large texts |
| Run `wl-copy` and `wl-paste`, or `xclip` and `xsel`, when they are installed | Where they are installed | Little code. They are probably not part of a default Ubuntu desktop, which task 4 checks, and then the person has to install them. Without the data-control protocol, which GNOME does not offer, `wl-paste` has to give itself keyboard focus to read |
| The Wayland data-control protocol, in pure Go | KDE Plasma and wlroots compositors | A second protocol to speak, and no GNOME, which is the default desktop of Ubuntu |

The planned way is the X11 selection, because it covers X11 and GNOME on Wayland with one implementation and nothing to install. It is deduced from how GNOME runs Xwayland and has not been measured. Task 4 measures it on Ubuntu 24.04 with GNOME on Wayland and on an X11 session, and records the choice in ADR 9.

Where no way works, for example a session with no Xwayland or a machine at its login screen, clipboard sharing is off on that machine. lanhop says why in its log once (FR-011), the central does not send copies to it, and the keyboard and mouse work as before.

## Switching it off

`lanhop clipboard off` writes a file named `clipboard`, with the word `off`, in the configuration directory, next to the key. `lanhop clipboard on` removes it. `lanhop clipboard` with nothing after it prints `on` or `off`.

`lanhop share` and `lanhop receive` read that file when they connect and at each focus change, so the switch applies without a restart. A machine that switches sends Features again, before it answers the leave. A machine takes a fresh mark without reading each time it starts to take part, when its own switch turns on or when the other end announces bit 0 again, so a copy made while sharing was off on either end never leaves. A side machine that is off writes nothing that arrives and reads nothing when focus leaves it, and still counts and acknowledges every copy frame that reaches it, so the window of the central keeps working.

## Decisions

| Decision | Choice | Why | Given up |
|---|---|---|---|
| Copies on the wire | Control frames of 8 bytes, announced by Features. [ADR 7](../../docs/adr/0007-clipboard-in-control-frames.md) | lanhop 0.1.0 ignores them on both ends, so a desk with mixed versions keeps working | Half the bytes are framing: a 1 MiB copy takes 2 MiB on the wire |
| Keys behind a copy | A window of 32 KiB of copy frames that are not acknowledged. [ADR 7](../../docs/adr/0007-clipboard-in-control-frames.md) | A key waits at most for 32 KiB, the same on every system | A copy moves at most 32 KiB per round trip |
| Route and timing | Through the central, read when focus leaves, handed out after the answer of the machine that lost focus, written on the machine that has focus. [ADR 8](../../docs/adr/0008-copies-follow-focus-through-the-central.md) | Side machines only connect to the central. A copy reaches only the machines the person visits, and the clipboard is read as little as possible | A copy that a program makes on a machine without focus moves only when focus next leaves that machine |
| Limit | 1 MiB of UTF-8 text | Covers long logs and source files. At 2 MiB on the wire it takes about 0.2 s on a 100 Mbit/s network | Larger texts are not sent |
| Switch | A file in the configuration directory, set by `lanhop clipboard` | Works with the service as installed, with no restart and no flag to edit | Nothing |
| macOS | `NSPasteboard` through cgo, with `changeCount` | It is the clipboard of macOS, and `changeCount` tells a new copy without reading it | Code that only the macOS job of the CI can build |
| Linux | Open, see above. Decided in task 4, ADR 9 | | |

## Tasks

Each task is one pull request with its tests. Task 6 starts after pull request #31 of spec 008 is merged, because both change `lanhop share` and `lanhop receive`.

- [ ] **1. Messages.** This spec, this plan, ADRs 7 and 8. `internal/event` gets the five control codes. `internal/clipboard` turns a text into slices of frames and frames back into a text or a withdrawal, acknowledges what it reads, and keeps the window of the sender. → Verify: round trips for texts of 1 to 9 bytes and at the limit, with characters of several bytes. Empty texts, texts over the limit and invalid UTF-8 are refused. Frames mixed with input and pings still give the text. A new copy or a withdrawal drops an unfinished one, and stray data frames are dropped. Every copy frame is acknowledged, refused ones too. A sender that waits for acknowledgements delivers a 1 MiB copy with never more than the window on the way, and the window still works after an abandoned copy and a withdrawal. The receiver of 0.1.0 replays nothing when it gets the frames of a copy.
- [ ] **2. Who gets which copy.** A state machine with no I/O in `internal/clipboard`: focus changes, reads, copies, answers and copies cut short in, announcements, copies to send, withdrawals, answers and writes out, for the central and for a side machine. → Verify: table tests for each acceptance scenario of stories 1, 2, 3, 5 and 6, for the rule that no copy goes back where it came from, for withdrawals from a machine that focus moved away from, for the wait for answers and its end, and for a copy that the person made after focus arrived (FR-013).
- [ ] **3. macOS clipboard.** `NSPasteboard` in `internal/macos`. → Verify: the macOS job of the CI writes a text with emoji and several lines and reads it back, and sees `changeCount` change. On a Mac, with the person at the desk, lanhop.app started by its launch agent reads a copy made in another app, on macOS 15 and on macOS 26, and the plan records whether macOS asked. A read and a write of 64 KiB are timed, because the read is on the path of every copy (SC-001).
- [ ] **4. Research: the clipboard on Linux.** A throwaway prototype of the X11 selection. Measure it on Ubuntu 24.04 with GNOME on Wayland, copying in a Wayland application and pasting in another, in both directions, and on an X11 session in a container with Xvfb. Check which of `wl-copy`, `wl-paste`, `xclip` and `xsel` a default Ubuntu desktop has, and how the service of lanhop finds the session's display. Writing to the clipboard of a session in use needs the OK of the person who uses that session. → Verify: ADR 9 records the choice with the measurements.
- [ ] **5. Linux clipboard.** The way chosen in task 4, in its own package under `internal/`. → Verify: tests against Xvfb in a container without privileges, including a text larger than one X11 request. A read and a write of 64 KiB are timed (SC-001).
- [ ] **6. Copies in `lanhop share` and `lanhop receive`, and `lanhop clipboard`.** Wire tasks 1 to 5 into the central and the side machine, send copies off the input path, and add the switch. Send acknowledgements from the side that reads, and wait for them on the side that sends. → Verify: an integration test with a central and two side machines over TLS on loopback, with fake clipboards. A side machine that never sends Features and drops every frame, as 0.1.0 does, keeps getting input and gets no copy. With the reading side slowed to 20 Mbit/s: a 1 MiB copy is sent while keys flow and the delay of each key is timed against SC-002, and a 64 KiB copy is timed from the enter to the write against SC-001. A loopback at full speed would hide both. A side machine that switches off in the middle of a copy, and focus that leaves in the middle of a copy, leave the next copy working.
- [ ] **7. README and a test at the desk.** Say how copying works and how to switch it off. → Verify: with the person at the desk, every acceptance scenario of stories 1, 2 and 3 from a Mac to a Mac and between a Mac and the Linux machine.

## Done when

- [ ] Every acceptance scenario of the spec passes at the desk, with macOS and Linux as central and as side machine.
- [ ] The README says what is shared, the limit, and how to switch it off.

## Risks

- **Pasteboard privacy on macOS.** If macOS asks before lanhop reads a copy, and the answer belongs to one build of the app as described in [ADR 6](../../docs/adr/0006-an-app-bundle-with-its-own-identifier-on-macos.md), each update may ask again.
- **GNOME on Wayland.** The plan relies on GNOME keeping the clipboard of Wayland applications and the one of Xwayland in step. Task 4 exists to check it before tasks 5 and 6 depend on it.
- **The display of the Linux service.** A systemd user unit can start before the desktop session and then has no `DISPLAY`. Task 4 finds how lanhop learns where the session is.
- **Bytes on the wire.** A copy takes twice its size, and moves at most 32 KiB per round trip. It matters only for copies near the limit on a slow Wi-Fi link, where a 1 MiB copy may take about a second. Keys wait at most for the window.
- **A paste right after crossing.** The copy follows the answer of the machine that lost focus, so a paste pressed in the same instant can get the previous text. SC-001 bounds how long that lasts.
