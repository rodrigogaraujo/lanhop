# Implementation Plan: File sharing

**Spec**: [spec.md](spec.md) · **Created**: 2026-11-13 · **Status**: Draft

## Goal

Stage A: a file copy moves like a text copy of spec 005, and the person pastes it in the file manager. Stage B: a file dragged through a screen edge can be dropped on the neighbour. File bytes travel on a second connection of their own ([ADR 13](../../docs/adr/0013-files-travel-on-a-second-connection.md)).

## What each system allows

Nothing in this table was measured yet. Each row says what the plan relies on, and research tasks 1 and 8 measure it before code depends on it.

| Step | macOS | Linux, X11 session | Linux, Wayland application |
|---|---|---|---|
| See a file copy | The general pasteboard holds file URLs (`public.file-url`), and the names as text, after a copy in Finder | The `CLIPBOARD` owner offers `x-special/gnome-copied-files` and `text/uri-list`, and the paths as text, after a copy in Nautilus. A cut is marked on the first line, and arrives as a copy | Through Xwayland, as text in spec 005 |
| Read the files | Reading from Desktop, Documents, Downloads, iCloud Drive or an external or network volume needs a permission of macOS for lanhop.app, asked on the machine the person just left. An iCloud file that is not downloaded is downloaded first, or cannot be read offline | Plain file access as the person | Same as X11 |
| Offer files to paste | File URLs of the arrival folder on the pasteboard. Finder copies them on paste | Own `CLIPBOARD` and offer `x-special/gnome-copied-files` and `text/uri-list` with the arrival folder. Nautilus copies them on paste | Through Xwayland |
| See that a file is being dragged | The drag pasteboard (`NSPasteboard` named `drag`) changes when a drag starts, and holds the file URLs | The `XdndSelection` owner changes when a drag starts (`XFIXES`), and offers `text/uri-list` | Likely impossible: Wayland gives an outside program no view of another program's drag |
| Drop on the neighbour | A drag session from a window of lanhop under the cursor, offering file promises, and file URLs once the files have arrived. Finder and programs that take promises accept the drop at once, others once the files have arrived. It needs an AppKit run loop on the main thread | lanhop acts as an XDND source toward the window under the cursor, in pure Go, grabs the pointer while it does, and lets the drop happen only once the files have arrived, because XDND has no promises | Likely impossible directly. GNOME may carry a drag from an X11 program into a Wayland one, so lanhop as an X11 source could reach some |
| End the drag left behind | Post Escape on the source after the crossing, then a release of the button, which the capture otherwise swallows | Same, through the virtual devices | - |

The proposal for the first release is stage A everywhere and stage B from a Mac to a Mac, because the desk is four Macs and one Linux machine on X11. Linux to and from a Mac for stage B comes after.

## How a file copy moves (stage A)

The rules of who gets which copy are those of spec 005 and ADR 8, with these additions.

1. **A copy is text or files.** When the clipboard offers both, the files win (FR-012). A machine that takes text but not files gets a withdrawal instead of the names or paths.
2. **The answer to a leave stays light**: the number of files and their total size, worked out within the time an answer has. No content is read and no digest is computed at that moment (ADR 13).
3. **Files move to the machine that has focus, while it has focus.** When a machine that takes files gets focus and does not hold the copy, the central opens the transfer. If focus leaves before every file has arrived, the transfer stops and what arrived is removed, so a machine the person only passes through keeps nothing. The next visit starts it again.
4. **Side machine to side machine** streams through the central (ADR 13), which keeps a copy only when it has focus.
5. **Arriving.** Each file is written under a temporary name in the arrival folder, checked by size and SHA-256, given the quarantine mark on macOS (FR-014), and renamed. Only when every file has arrived does lanhop put them on the clipboard. A paste before that gives what the clipboard held before (FR-002).
6. **Room.** A transfer starts only if it fits in the free space and in the room lanhop allows itself, 8 GiB for the whole arrival folder, removing the oldest arrivals first (FR-013). Arrivals are removed after a day (FR-009).

| System | Arrival folder |
|---|---|
| macOS | `~/Library/Caches/lanhop/arrived/` |
| Linux | `$XDG_CACHE_HOME/lanhop/arrived/`, or `~/.cache/lanhop/arrived/` |

**Limit**: 4 GiB and 10 000 files and folders per copy (FR-005).

## How a drag crosses (stage B)

1. **Seeing a drag.** On the machine with the keyboard and mouse, lanhop watches for a file drag (table above).
2. **Crossing.** During a file drag, an edge with a neighbour that takes dragged files crosses with the button held, after a push of 600 ms instead of the usual push time, never in a corner, and never through the edge where the Dock of macOS sits. Every other held button keeps blocking crossings (FR-008, and FR-008 of spec 002).
3. **Leaving the source.** At the crossing, lanhop posts Escape to the source, so that its drag ends without a drop, then a release of the button. The press is never replayed on the neighbour.
4. **On the neighbour.** lanhop starts a drag of its own with the files, and the replayed mouse carries it. The files start to travel at the crossing. A drop waits for them as the table says.
5. **Changing one's mind.** Dragging back across the edge, or Escape, ends the drag on the neighbour without a drop and stops the transfer.

## Decisions

| Decision | Choice | Why | Given up |
|---|---|---|---|
| Bytes of files | A second TLS connection to the same port, opened by the side machine at the central's request. [ADR 13](../../docs/adr/0013-files-travel-on-a-second-connection.md) | Full speed, no doubling of size, and keys never wait behind file bytes in the same stream | One more connection to open, and a change in the transport |
| When files move in stage A | While a machine that does not hold the copy has focus | Same rule as text, and a machine passed through keeps nothing | A paste right after arriving waits for large files |
| Where files land | An arrival folder in the cache, until pasted or dropped | Nothing appears in the person's folders without a paste or a drop | Disk space for a day, within 8 GiB |
| Text in a file copy | The files win, and the names or paths are never sent as text | Paths reveal folders and names | A machine that takes only text gets nothing |
| Which drag directions first | Mac to Mac | The desk is mostly Macs, and Wayland likely rules out part of Linux | Linux in stage B waits |
| Control codes | 10 to 15 for files. The work on `lanhop update` starts at 16 | No clash between features | Nothing |

## Tasks

Each task is one pull request with its tests. Stage A comes first, and stage B starts after the research of task 8.

**Stage A: copy and paste**

- [ ] **1. Research: files on the clipboard.** Measure on a Mac:
  - what Finder puts on the pasteboard for a copy of files and of a folder;
  - whether lanhop.app, started by its launch agent, can read files from Desktop, Documents, Downloads, iCloud Drive and an external volume, which permission macOS asks for, and whether reading file URLs raises the pasteboard alert;
  - whether Finder pastes file URLs that lanhop puts on the pasteboard;
  - whether Finder pastes file promises, which would let files travel only on paste.

  Measure on the Linux machine what Nautilus offers and accepts. Writing to the clipboard of a session in use needs the OK of the person who uses it. → Verify: ADR 14 records the formats and the permissions, with the measurements.
- [ ] **2. Data connections.** ADR 13 in `internal/transport`. → Verify: tests over TLS on loopback, including a data connection refused for an unknown or expired number, from the wrong machine, and twice. Keys on the control connection are timed while 100 MiB move on the data connection, with both connections behind one queue shaped to 100 Mbit/s, against SC-002; a loopback at full speed would hide the delay.
- [ ] **3. File copies in the state machines.** A copy is text or files, in `internal/clipboard`, with the switch for files apart from text. → Verify: the table tests of spec 005 again with file copies; a machine that takes text but not files; the limit; and a transfer stopped when focus leaves.
- [ ] **4. macOS file pasteboard.** Read and write file URLs in `internal/macos`. → Verify: the macOS job of CI, on a pasteboard of its own.
- [ ] **5. Linux file clipboard.** `x-special/gnome-copied-files` and `text/uri-list` in `internal/x11`. → Verify: tests against Xvfb.
- [ ] **6. Files in share and receive, and the switch.** Arrival folder, safe paths, checks, renames, quarantine, room, clean-up, and `lanhop files on|off`. → Verify: an integration test over TLS on loopback with fake clipboards; paths with `..`, absolute paths and links refused; a file cut short by a lost connection leaving nothing behind; SC-001 timed.
- [ ] **7. Test at the desk, stage A.** With the person: Mac to Mac, Mac to Linux, Linux to Mac (SC-003).

**Stage B: drag across the edge**

- [ ] **8. Research: dragging.** Measure on a Mac that the drag pasteboard reveals a file drag to a background program, that Escape then a button release ends the source drag without a drop, and that lanhop can start a drag session with file promises from a window of its own while the mouse is driven by posted events. Measure on the Linux machine `XdndSelection` and an XDND drop from a pure Go source, and whether GNOME on Wayland carries such a drop into a Wayland program, if a Wayland session can be tried. → Verify: ADR 15 records what works on each system and the directions of the first release.
- [ ] **9. Crossing with a file drag.** The edge watcher crosses with a button held only during a file drag, after the longer push, outside corners and the Dock edge, and the source drag is ended. → Verify: table tests of the edge watcher with and without a drag, and every scenario of story 3 of spec 002 still passing.
- [ ] **10. Drop on a Mac.** The drag session, and the AppKit run loop it needs. → Verify: CI builds it. At the desk, a file dragged from a Mac lands where it is dropped on another Mac.
- [ ] **11. Test at the desk, stage B.** Story 2 with the person, in the directions ADR 15 chose (SC-004).

## Done when

- [ ] Every acceptance scenario of stories 1 and 3 passes at the desk in the directions of SC-003.
- [ ] Story 2 passes at the desk in the directions ADR 15 chose.
- [ ] The README says what can be dragged where, and what cannot.

## Risks

- **An AppKit run loop in lanhop.** A drag on macOS needs a window and the main thread. In a Go program that means locking the main goroutine to the main thread from `init` and moving the rest of lanhop off it, a large change to `cmd/lanhop`. Task 8 finds whether it works before stage B depends on it.
- **macOS permissions for files.** Reading files from protected folders may need a permission per folder, or Full Disk Access, for lanhop.app. The question appears on the machine the person just left, as the pasteboard alert of spec 005 may. macOS ties these grants to the signature of the app (ADR 6), so an update may ask again. The installer of spec 008 may have to ask for them.
- **The cache can be emptied.** macOS may empty `~/Library/Caches` when the disk is short of room. A file copy that arrived is then gone, and has to travel again.
- **Large files right after a move.** A paste of a large file right after moving gives the previous clipboard until the file has arrived.
- **A shared queue.** On a busy Wi-Fi link, file bytes and keys share the air even in separate streams. Task 2 measures it, and the data connection is paced if needed.
- **Wayland.** Dragging from or to a Wayland application on Linux likely cannot work. lanhop says so once in its log (FR-010).
