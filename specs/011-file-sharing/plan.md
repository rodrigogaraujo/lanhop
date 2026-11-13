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
| See that a file is being dragged | The drag pasteboard (`NSPasteboard` named `drag`) changes when a drag starts, and holds the file URLs | XDND is the drag and drop protocol of X11. The `XdndSelection` owner changes when a drag starts (`XFIXES`), and offers `text/uri-list`. Only a list where every address starts with `file:` counts as a file drag | Deduced impossible: Wayland gives an outside program no view of another program's drag |
| Drop on the neighbour | A drag session from a window of lanhop under the cursor, offering file promises, and file URLs once the files have arrived. Finder and programs that take promises accept the drop at once, others once the files have arrived. It needs an AppKit run loop on the main thread | lanhop acts as an XDND source toward the window under the cursor, in pure Go, from an unmapped window of its own: `XdndEnter`, `XdndPosition` and `XdndStatus` as the cursor moves, then `XdndDrop` and `XdndFinished`, with the copy action only. It grabs the pointer while it does, lets the drop happen only once the files have arrived, because XDND has no promises, and gives up on a target that does not answer | Deduced impossible. A drag between an X11 program and a Wayland one through Xwayland is left out of the first release |
| End the drag left behind | Post Escape on the source after the crossing, then a release of the button, which the capture otherwise swallows | Same, through the virtual devices | - |

The first release has stage A and stage B in every direction between macOS and Linux in an X11 session: Mac to Mac, Mac to Linux, Linux to a Mac and Linux to Linux. The Linux machine of the desk runs an X11 session of GNOME 46, measured for [ADR 9](../../docs/adr/0009-the-linux-clipboard-through-the-x11-selection.md), so XDND should work there; task 8 measures it. If task 8 finds that a direction cannot work, lanhop stops and asks the owner before building the rest.

**Linux in a Wayland session.** Deduced, not measured: a program outside the compositor can neither see a drag of a Wayland application nor start one into it. On such a machine stage A should still work through Xwayland, as text does, which is deduced too (ADR 9). A machine says with Features bit 2 that it takes dragged files, and a machine in a Wayland session does not set it. A file drag pushed toward a machine without bit 2 does not cross, and the central says in its log and in `lanhop status`, once per run and per machine, that copying and pasting the file works instead. Bit 2 belongs to a whole machine, so a drag that starts in an X11 application of a Wayland session stays out of the first release too, even if it could work through Xwayland.

## How a file copy moves (stage A)

The rules of who gets which copy are those of spec 005 and ADR 8, with these additions.

1. **A copy is text or files.** When the clipboard offers both, the files win (FR-012). A machine that takes text but not files gets a withdrawal instead of the names or paths.
2. **The answer to a leave stays light**: the number of files and their total size, worked out within the time an answer has. No content is read and no digest is computed at that moment (ADR 13).
3. **Files move to the machine that has focus, while it has focus.** When a machine that takes files gets focus and does not hold the copy, the central opens the transfer. If focus leaves before every file has arrived, the transfer stops and what arrived is removed, so a machine the person only passes through keeps nothing. The next visit starts it again. A transfer that serves a drop already accepted on that machine (stage B) carries on after focus leaves.
4. **Side machine to side machine** streams through the central (ADR 13), which keeps a copy only when it has focus.
5. **Arriving.** Each file is written under a temporary name in the arrival folder, checked by size and SHA-256, given the quarantine mark on macOS (FR-014), and renamed. Only when every file has arrived does lanhop put them on the clipboard. A paste before that gives what the clipboard held before (FR-002).
6. **Room.** A transfer starts only if it fits in the free space and in the room lanhop allows itself, 8 GiB for the whole arrival folder, removing the oldest arrivals first (FR-013). Arrivals are removed after a day (FR-009).

| System | Arrival folder |
|---|---|
| macOS | `~/Library/Caches/lanhop/arrived/` |
| Linux | `$XDG_CACHE_HOME/lanhop/arrived/`, or `~/.cache/lanhop/arrived/` |

**Limit**: 4 GiB and 10 000 files and folders per copy (FR-005).

## How a drag crosses (stage B)

1. **Seeing a drag.** On the machine with the keyboard and mouse, lanhop watches for a file drag (table above). The sharer already knows which buttons are held from the input it captures, on both systems, and a drag ends when the button is released.
2. **Crossing.** During a file drag, an edge with a neighbour that takes dragged files crosses with the button held, after a push of 600 ms or of the configured push time plus 300 ms, whichever is longer, never in a corner, and never through the edge where a dock sits: the Dock of macOS, or the dock of Ubuntu. Every other held button keeps blocking crossings (FR-008, and FR-008 of spec 002).
3. **Leaving the source.** At the crossing, lanhop posts Escape to the source, so that its drag ends without a drop, then a release of the button: through posted events on a Mac, through the virtual devices on Linux. The press is never replayed on the neighbour.
4. **On the neighbour.** lanhop starts a drag of its own with the files, and the replayed mouse carries it. The files start to travel at the crossing. A drop waits for them as the table says.
5. **Changing one's mind.** Dragging back across the edge, or Escape, ends the drag on the neighbour without a drop and stops the transfer.

## Decisions

| Decision | Choice | Why | Given up |
|---|---|---|---|
| Bytes of files | A second TLS connection to the same port, opened by the side machine at the central's request. [ADR 13](../../docs/adr/0013-files-travel-on-a-second-connection.md) | Full speed, no doubling of size, and keys never wait behind file bytes in the same stream | One more connection to open, and a change in the transport |
| When files move in stage A | While a machine that does not hold the copy has focus | Same rule as text, and a machine passed through keeps nothing | A paste right after arriving waits for large files |
| Where files land | An arrival folder in the cache, until pasted or dropped | Nothing appears in the person's folders without a paste or a drop | Disk space for a day, within 8 GiB |
| Text in a file copy | The files win, and the names or paths are never sent as text | Paths reveal folders and names | A machine that takes only text gets nothing |
| Drag directions | Every direction between macOS and Linux in an X11 session | The owner asked for all of them | A Linux machine in a Wayland session falls back to copy and paste, said in the log and in `lanhop status` |
| Control codes | 10 to 15 for files. The work on `lanhop update` starts at 16 | No clash between features | Nothing |

## Tasks

Each task is one pull request with its tests. Stage A comes first, and stage B starts after the research of task 8. Tasks 10, 11 and 12 can then go in any order, and task 13 needs all three.

**Stage A: copy and paste**

- [x] **0. A file copy is not text.** The text readers of spec 005 in `internal/macos` and `internal/x11` report a copy of files as not text, so its names or paths never travel as text (FR-012). This lands before spec 005 is wired, whatever happens to the rest of this plan. → Verify: the macOS job of CI and Xvfb, with a copy that offers both files and text.
- [ ] **1. Research: files on the clipboard.** Measure on a Mac:
  - what Finder puts on the pasteboard for a copy of files and of a folder;
  - whether lanhop.app, started by its launch agent, can read files from Desktop, Documents, Downloads, iCloud Drive and an external volume, which permission macOS asks for, and whether reading file URLs raises the pasteboard alert;
  - whether Finder pastes file URLs that lanhop puts on the pasteboard;
  - whether Finder pastes file promises, which would let files travel only on paste.

  Measure on the Linux machine what Nautilus offers and accepts. Writing to the clipboard of a session in use needs the OK of the person who uses it. → Verify: ADR 14 records the formats and the permissions, with the measurements.
- [x] **2. Data connections.** ADR 13 in `internal/transport`. → Verify: tests over TLS on loopback, including a data connection refused for an unknown or expired number, from the wrong machine, and twice. Keys on the control connection are timed while 100 MiB move on the data connection, with both connections behind one queue shaped to 100 Mbit/s, against SC-002; a loopback at full speed would hide the delay.
- [ ] **2b. Pace that follows the delay.** The data connection lowers its rate when the delay of the control connection grows, or by acknowledgements on the data connection, instead of keeping to the fixed 80 Mbit/s of task 2, which leaves keys 245 ms late behind a queue drained at 50 Mbit/s. → Verify: keys timed while 100 MiB move behind one queue shaped to 50 Mbit/s, and to 100 Mbit/s, against SC-002; at the desk on Wi-Fi in task 7.
- [ ] **3. File copies in the state machines.** A copy is text or files, in `internal/clipboard`, with the switch for files apart from text. → Verify: the table tests of spec 005 again with file copies; a machine that takes text but not files; the limit; and a transfer stopped when focus leaves.
- [ ] **4. macOS file pasteboard.** Read and write file URLs in `internal/macos`. → Verify: the macOS job of CI, on a pasteboard of its own.
- [ ] **5. Linux file clipboard.** `x-special/gnome-copied-files` and `text/uri-list` in `internal/x11`. → Verify: tests against Xvfb.
- [ ] **6. Files in share and receive, and the switch.** Arrival folder, safe paths, checks, renames, quarantine, room, clean-up, and `lanhop files on|off`. → Verify: an integration test over TLS on loopback with fake clipboards; paths with `..`, absolute paths and links refused; a file cut short by a lost connection leaving nothing behind; SC-001 timed.
- [ ] **7. Test at the desk, stage A.** With the person: Mac to Mac, Mac to Linux, Linux to Mac (SC-003), and keys timed on Wi-Fi while a large file moves (SC-002, task 2b).

**Stage B: drag across the edge**

- [ ] **8. Research: dragging.** Measure on a Mac that the drag pasteboard reveals a file drag to a background program, that Escape then a button release ends the source drag without a drop, and that lanhop can start a drag session with file promises from a window of its own while the mouse is driven by posted events. Measure on the Linux machine `XdndSelection`, an XDND drop from a pure Go source, Escape then a button release through the virtual devices ending a drag of Nautilus without a drop, and whether the virtual devices still move the cursor while lanhop holds a pointer grab, and whether GNOME on Wayland carries such a drop into a Wayland program, if a Wayland session can be tried. → Verify: ADR 15 records what works on each system, with the measurements. If a direction cannot work, the work stops there and the owner decides before tasks 9 to 13 go on.
- [ ] **9. Crossing with a file drag.** The edge watcher crosses with a button held only during a file drag, after the longer push, outside corners and the Dock edge, and the source drag is ended. Each machine sets Features bit 2 only where lanhop can start a drag (macOS, or Linux in an X11 session) and only while file sharing is on, and clears it when file sharing is switched off. When a dragged file meets a neighbour without bit 2, the central says so once per run and per machine in its log and in `lanhop status`. → Verify: table tests of the edge watcher with and without a drag, and every scenario of story 3 of spec 002 still passing.
- [ ] **10. Drop on a Mac.** The drag session, and the AppKit run loop it needs. → Verify: CI builds it. At the desk, a file dragged from a Mac lands where it is dropped on another Mac, and one dragged from Linux lands on a Mac.
- [ ] **11. Drop on Linux.** lanhop as an XDND source in `internal/x11`, from an unmapped window of its own, toward the window under the cursor that has `XdndAware`, with the pointer grabbed, the copy action only, the drop held until the files have arrived, and a time limit on a target that does not answer. The same lanhop process is the source on the machine that receives a drag and watches drags on the machine that sends one, so Linux to Linux needs nothing more. → Verify: tests against Xvfb with a small XDND target, including a drop before the files have arrived. At the desk, a file dragged from a Mac lands where it is dropped on the Linux machine.
- [ ] **12. Seeing a drag on Linux.** `XdndSelection` watched through `XFIXES`, so that a drag that starts on the Linux machine can cross, counted as a file drag only when its `text/uri-list` holds only `file:` addresses, and ended on the source with Escape and a button release through the virtual devices. → Verify: tests against Xvfb with a small XDND source, which sees the drag end without a drop. At the desk, a file dragged from the Linux machine lands on a Mac A small XDND source in Xvfb sees the drag end without a drop.
- [ ] **13. Test at the desk, stage B.** Story 2 with the person: Mac to Mac, Mac to Linux and Linux to a Mac. Linux to Linux runs in containers without privileges: two Xvfb displays, two paired lanhop processes on a container network, and small XDND test programs as source and target. That shows the protocol, not a real file manager, so the README also asks anyone with two Linux machines to try it, without holding the release for it. The container test passes when the dropped file arrives whole with the same content, and the source drag ends without a drop (SC-004).

## Done when

- [ ] Every acceptance scenario of stories 1 and 3 passes at the desk in the directions of SC-003.
- [ ] Story 2 passes at the desk from a Mac to a Mac, from a Mac to Linux and from Linux to a Mac, and the Linux to Linux test passes in containers.
- [ ] The README says what can be dragged where and what cannot, and asks people with two Linux machines to try Linux to Linux.

## Risks

- **An AppKit run loop in lanhop.** A drag on macOS needs a window and the main thread. In a Go program that means locking the main goroutine to the main thread from `init` and moving the rest of lanhop off it, a large change to `cmd/lanhop`. Task 8 finds whether it works before stage B depends on it.
- **macOS permissions for files.** Reading files from protected folders may need a permission per folder, or Full Disk Access, for lanhop.app. The question appears on the machine the person just left, as the pasteboard alert of spec 005 may. macOS ties these grants to the signature of the app (ADR 6), so an update may ask again. The installer of spec 008 may have to ask for them.
- **The cache can be emptied.** macOS may empty `~/Library/Caches` when the disk is short of room. A file copy that arrived is then gone, and has to travel again.
- **Large files right after a move.** A paste of a large file right after moving gives the previous clipboard until the file has arrived.
- **A shared queue.** On a busy Wi-Fi link, file bytes and keys share the air even in separate streams. Task 2 measured it behind one queue: the fixed pace of 80 Mbit/s keeps keys within SC-002 on a link of 100 Mbit/s, and leaves them 245 ms late on a link of 50 Mbit/s. Task 2b makes the pace follow the delay, and task 7 measures it on Wi-Fi.
- **Wayland.** Dragging from or to a Wayland application on Linux is deduced to be impossible. The central says so once per run and per machine in its log and in `lanhop status`, and points to copy and paste of the file (story 2 scenario 4, FR-010).
- **Linux to Linux at the desk.** There is one Linux machine, so that direction is tested in containers, between lanhop and test programs, not with a real file manager, and not with the virtual devices and the pointer grab of a real session, which only task 8 measures on the one machine.
- **XDND on Linux.** A pointer grab may change how the virtual devices move the cursor, some programs ignore XDND, and a target may never answer. Task 8 measures the first, and task 11 gives up on a target after a time limit.
