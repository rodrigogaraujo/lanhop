# Implementation Plan: File sharing

**Spec**: [spec.md](spec.md) · **Created**: 2026-11-13 · **Status**: Draft

## Goal

Stage A: a file copy moves like a text copy of spec 005, and the person pastes it in the file manager. Stage B: a file dragged through a screen edge can be dropped on the neighbour. File bytes travel on a second connection of their own, so they never hold up the keyboard and mouse.

## What each system allows

Nothing in this table was measured yet. Each row says what the plan relies on, and the research tasks measure it before code depends on it.

| Step | macOS | Linux, X11 session | Linux, Wayland application |
|---|---|---|---|
| See a file copy | The general pasteboard holds file URLs (`public.file-url`) after a copy in Finder | The `CLIPBOARD` owner offers `x-special/gnome-copied-files` and `text/uri-list` after a copy in Nautilus | Through Xwayland, as text in spec 005 |
| Read the files | Reading from Desktop, Documents, Downloads or iCloud Drive needs a permission of macOS for the program that reads | Plain file access as the person | Same as X11 |
| Offer files to paste | Put file URLs of the arrival folder on the pasteboard; Finder copies them on paste | Own `CLIPBOARD` and offer `x-special/gnome-copied-files` and `text/uri-list` with the arrival folder; Nautilus copies them on paste | Through Xwayland |
| See that a file is being dragged | The drag pasteboard (`NSPasteboard` named `drag`) changes when a drag starts, and holds the file URLs | The `XdndSelection` owner changes when a drag starts (`XFIXES`), and offers `text/uri-list` | **Not possible**: Wayland gives no program a view of another program's drag |
| Drop on the neighbour | lanhop starts a drag session from a window of its own under the cursor, with the files as file promises, and the replayed mouse carries it to the drop target. This needs an AppKit run loop on the main thread of lanhop, which it does not have today | lanhop acts as an XDND source toward the window under the cursor, in pure Go over the X socket | **Not possible**: a program outside the compositor cannot start a drag into a Wayland application |
| Cancel the drag left behind | Post Escape on the source after the crossing, before the button is released | Same, through the virtual keyboard | - |

The proposal for the first release is stage A everywhere and stage B from a Mac to a Mac, because the desk is four Macs and one Linux machine on X11. Linux to and from a Mac for stage B comes after.

## How a file copy moves (stage A)

The rules of who gets which copy are those of spec 005 and ADR 8. A copy is now text or a list of files.

1. The machine where the files were copied reads the list when focus leaves it, with the names, sizes and kinds, and answers the leave with that list instead of a text. Nothing of the files' content moves yet.
2. When a machine that takes files gets focus and does not hold that copy, the files travel to it on a data connection ([ADR 13](../../docs/adr/0013-files-travel-on-a-second-connection.md)). A side machine to side machine copy passes through the central, which keeps the files in its own arrival folder only until they have been passed on.
3. The receiving machine writes each file under a temporary name in its arrival folder, checks the size and a SHA-256 sent with the list, and renames it. Only when every file has arrived does lanhop put the files on the clipboard. A paste before that gives what the clipboard held before (FR-002).
4. Files in the arrival folder are removed after a day (FR-009).

| System | Arrival folder |
|---|---|
| macOS | `~/Library/Caches/lanhop/arrived/` |
| Linux | `$XDG_CACHE_HOME/lanhop/arrived/`, or `~/.cache/lanhop/arrived/` |

**Limit**: 4 GiB per file copy, and at most 10 000 files. A larger copy is not sent (FR-005).

## How a drag crosses (stage B)

1. On the machine with the keyboard and mouse, lanhop watches for a file drag (table above). While one is under way, an edge with a neighbour that takes dragged files crosses even with the button held. Every other held button keeps blocking crossings (FR-008).
2. At the crossing, lanhop posts Escape to the source so that its drag ends without a drop, and starts sending the files to the neighbour.
3. On the neighbour, lanhop starts a drag of its own with the files and keeps the button held there. The person drops by releasing the button. On macOS the files are file promises: the drop target asks lanhop to write them where they were dropped, and waits while they arrive.
4. Dragging back across the edge, or Escape, ends the drag on the neighbour without a drop.

## Decisions

| Decision | Choice | Why | Given up |
|---|---|---|---|
| Bytes of files | A second TLS connection to the same port, per transfer. [ADR 13](../../docs/adr/0013-files-travel-on-a-second-connection.md) | Full speed, no doubling of size, and keys never wait behind file bytes in the same connection | One more connection to open, and a change in the transport |
| When files move in stage A | When a machine that does not hold the copy gets focus | Same rule as text, so files reach only the machines the person visits | A paste right after arriving waits for large files |
| Where files land | An arrival folder in the cache, until pasted or dropped | Nothing appears in the person's folders without a paste or a drop | Disk space for a day |
| Which drag directions first | Mac to Mac | The desk is mostly Macs, and Wayland rules out part of Linux | Linux in stage B waits |
| Control codes | 10 to 15 for files, agreed with the work on `lanhop update`, which starts at 16 | No clash between features | Nothing |

## Tasks

Each task is one pull request with its tests. Stage A comes first, and stage B starts after the research of task 8.

**Stage A: copy and paste**

- [ ] **1. Research: files on the clipboard.** Measure on a Mac what Finder puts on the pasteboard for a copy of files and of a folder, whether lanhop.app started by its launch agent can read files from Desktop, Documents, Downloads and iCloud Drive and which permission macOS asks for, and whether Finder pastes file URLs that lanhop puts on the pasteboard. Measure on the Linux machine what Nautilus offers and accepts. Writing to the clipboard of a session in use needs the OK of the person who uses it. → Verify: ADR 14 records the formats and the permissions, with the measurements.
- [ ] **2. Data connections.** ADR 13 in `internal/transport`: a second TLS connection to the same port, tied to the first by a transfer number, used only between machines that announce the files bit of Features. → Verify: tests over TLS on loopback, including a connection with an unknown transfer number refused, and keys on the first connection timed while 100 MiB move on the second (SC-002).
- [ ] **3. File copies in the state machines.** A copy is text or a list of files, in `internal/clipboard`, with the switch for files apart from text. → Verify: the table tests of spec 005 again with file lists, a machine that takes text but not files, and the limit.
- [ ] **4. macOS file pasteboard.** Read and write file URLs in `internal/macos`. → Verify: the macOS job of CI, on a pasteboard of its own.
- [ ] **5. Linux file clipboard.** `x-special/gnome-copied-files` and `text/uri-list` in `internal/x11`. → Verify: tests against Xvfb.
- [ ] **6. Files in share and receive, and the switch.** Arrival folder, checks, renames, clean-up after a day, and `lanhop files on|off`. → Verify: integration test over TLS on loopback with fake clipboards, a file cut short by a lost connection leaving nothing behind, and SC-001 timed.
- [ ] **7. Test at the desk, stage A.** With the person: Mac to Mac, Mac to Linux, Linux to Mac (SC-003).

**Stage B: drag across the edge**

- [ ] **8. Research: dragging.** Measure on a Mac that the drag pasteboard reveals a file drag to a background program, that posting Escape ends the source drag without a drop, and that a program like lanhop can start a drag session from a window of its own with file promises while the mouse is driven by posted events. Measure on the Linux machine `XdndSelection` and an XDND drop from a pure Go source. → Verify: ADR 15 records what works on each system and the directions of the first release.
- [ ] **9. Crossing with a file drag.** The edge watcher of the sharer crosses with a button held only during a file drag, and Escape is posted on the source. → Verify: table tests of the edge watcher with and without a drag, and every scenario of story 3 of spec 002 still passing.
- [ ] **10. Drop on a Mac.** The drag session with file promises, and the AppKit run loop it needs. → Verify: CI builds it. At the desk, a file dragged from a Mac lands where it is dropped on another Mac.
- [ ] **11. Test at the desk, stage B.** Story 2 with the person, in the directions ADR 15 chose (SC-004).

## Done when

- [ ] Every acceptance scenario of story 1 passes at the desk in the directions of SC-003.
- [ ] Story 2 passes at the desk in the directions ADR 15 chose.
- [ ] The README says what can be dragged where, and what cannot.

## Risks

- **An AppKit run loop in lanhop.** Starting a drag on macOS needs a window and the main thread. lanhop is a Go program with no application object today. Task 8 exists to find whether this works before stage B depends on it.
- **macOS permissions for files.** Reading files from protected folders may need a permission per folder, or Full Disk Access, for lanhop.app. The installer of spec 008 would have to ask for it.
- **Large files right after a move.** A paste of a large file right after moving gives the previous clipboard until the file has arrived. The person has to paste again.
- **Wayland.** Dragging from or to a Wayland application on Linux cannot work. lanhop says so once in its log (FR-010).
