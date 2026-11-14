# 15. What dragging a file can use on each system

Date: 2026-11-14 · Status: proposed

ADR 14 is reserved for research task 1 of spec 011 (files on the clipboard).

## Context

Stage B of [spec 011](../../specs/011-file-sharing/spec.md) lets a file dragged on one machine cross the edge with the button held and drop on the neighbour. Its plan rests on a table of what each system allows, none of it measured. Research task 8 measures it before tasks 9 to 13 depend on it, and the plan stops the work for the owner's decision if a direction cannot work.

The measurements were made at the desk on 2026-11-14 with small probe programs (draft PR #74, not for merge). The probes were separate from the installed lanhop:

- the Mac probe was its own app (`com.github.rodrigogaraujo.lanhop-dragprobe`), signed ad hoc, with its own Accessibility entry, removed afterwards;
- the Linux probe ran in the person's X11 session with the person's approval;
- every drag used a throwaway file over a scratch folder.

The machines:

- a MacBook on macOS 26.5.2 with Finder;
- a Linux machine running GNOME on Xorg with Nautilus.

## Measured

| Question | Mac | Linux (X11) |
|---|---|---|
| Does a program in the background see that a file drag started, and which files? | Yes. The drag pasteboard changes, with the left button held. Its item holds `public.file-url` with the plain path of the file, and `NSFilenamesPboardType` is in its list of types. | Yes. `XdndSelection` changes owner through XFIXES. Its `text/uri-list` holds the `file:` address, and the owner also offers the portal types. |
| Can lanhop drop a file there from a source of its own? | Yes. A drag session from a window of the probe, with a file promise, is accepted by Finder with the copy operation. This works while posted events drive the mouse, as lanhop's replay does on a Mac that receives focus. | Yes, measured with the physical mouse driving the drag under the probe's pointer grab. A pure Go XDND source, version 5, offering only `text/uri-list` with `XdndActionCopy`, is accepted by Nautilus. Nautilus first asks for the portal types; once they are refused, it takes `text/uri-list`. The same drop driven by lanhop's uinput replay is not yet measured; task 11 checks it. |
| Can lanhop end the drag left behind on the source without a drop? | Yes. A posted Escape and a posted left mouse up end Finder's drag: the image slides back while the finger still holds the button, and nothing moves. The [amendment](#amendment-2026-11-14-ending-a-source-drag-on-a-mac) corrects this. | **No.** Escape and a left button release sent through a uinput device of lanhop do not end Nautilus's drag. A uinput press and then release do not end it either. X keeps reporting button 1 held, because the physical mouse holds it; the kernel also drops a release from a device that never pressed that button. The Escape from the uinput keyboard does not cancel the drag. When the person releases, Nautilus drops where the cursor is. |
| Does the cursor still move from lanhop's virtual devices while lanhop holds a pointer grab? | - | Yes. A relative move through uinput moves the cursor by exactly that much under the grab, and the grabbing program gets the motion. |

No pasteboard alert of macOS appeared in any step. A GNOME session on Wayland was not tried; a drag there is still deduced impossible, as the plan says.

## Decision

These directions keep the plan of spec 011:

- **From a Mac**, to a Mac or to Linux: the drag is seen through the drag pasteboard, and ended on the source with a posted Escape and mouse up, through the event taps that the [amendment](#amendment-2026-11-14-ending-a-source-drag-on-a-mac) names.
- **To a Mac**: lanhop drops with a drag session and file promises from a window of its own, as task 10 plans.
- **To Linux in an X11 session**: lanhop drops as a pure Go XDND source with the copy action, as task 11 plans. It grabs the pointer, which does not stop its virtual devices from moving the cursor.

**From Linux** is open. Seeing the drag works, but the plan's way of ending it on the source does not, so Linux to a Mac and Linux to Linux cannot be built as planned. As the plan asks, the work on that direction stopped for the owner, who chose on 2026-11-14 to measure the following techniques in a short second session at the desk:

1. **Inject the release into the physical mouse.** The kernel lets a process write events to the event node of a device, and they reach X as if that device sent them. So X would see button 1 released by the slave that holds it. lanhop needs write access to that node, which the `input` group that lanhop's setup already uses gives.
2. **A drop target of lanhop at the edge.** An XdndAware window of lanhop on the edge receives the drop with the copy action. Nautilus then finishes the drag, and nothing moves. This alone does not end the drag: the drop happens only when X sees the release.

A third way, letting the real release reach X before lanhop takes the mouse, does not fit a drag that crosses with the button still held, so it was left out.

**Second session (2026-11-14), measured.** On the Linux machine of the desk, which receives (a side machine), the mouse is lanhop's own virtual device, replayed from the central: the same device that pressed button 1 can release it, and X takes that release. With technique 2, a file dragged in Nautilus with the mouse of the central and released over lanhop's window on the edge was dropped there: `XdndEnter` on the window, `XdndStatus` accept with `XdndActionCopy`, `XdndDrop`, the `text/uri-list` of the file read from `XdndSelection`, `XdndFinished` accepted with copy, and the file still in its place 3 s later. Technique 1, for a Linux machine whose physical mouse drives the drag (the Linux machine as the central), was not measured: the mouse of the desk's Linux machine is paired with the other machine, and the owner chose not to switch it.

**From Linux, decided by the owner.**

- A Linux machine that receives ends the source drag at the crossing by releasing button 1 through lanhop's own virtual device over an XdndAware window of lanhop at the edge, which takes the drop with copy. Nothing moves on the source, and the window also hands lanhop the list of the dragged files.
- A Linux machine that is the central, whose physical mouse drives the drag, does not let a file drag cross in the first release. The central says so once per run in its log and in `lanhop status`, as for a machine in a Wayland session, and copying and pasting the file works instead.

## Consequences

- Every choice the owner had kept the directions from a Mac, to a Mac and to Linux, so tasks 10 and 11 are built in their own pull requests, and the Mac half of task 9 can be, while the second session is prepared.
- The Linux half of task 9 and task 12 use the edge window and the release of lanhop's own virtual device, on a Linux machine that receives only.
- On Linux, the edge window must accept only the copy action, so a source never moves or deletes its file, even when the person holds a modifier.
- On a Mac, posted events that end the source drag need the Accessibility permission lanhop already has for its capture.
- On Linux, lanhop's XDND source must refuse the portal types and offer `text/uri-list`, which Nautilus takes.

## Amendment (2026-11-14): ending a source drag on a Mac

Before stage B relies on it, a third session at the desk measured again how a posted Escape and mouse up end a Finder drag. It used the `cancel-observe` probe of the research branch `research/drag-probes`, at head `753220c` (draft PR #74).

- The probe posts Escape, then a left mouse up about 20 ms later. Both carry the same mark as the events lanhop posts (`0x6c68`).
- A listen-only tap at the session level, inserted first like lanhop's capture, logs every left mouse event.
- Every 100 ms, the probe reads the left button in the hardware state (HID system state) and in the combined session state, which also counts posted events.
- Every counted run used a MacBook on macOS 26.5.2, its own trackpad and Finder. The person dragged a throwaway file, held it still over a scratch folder, and kept the button held. No event posted by lanhop was in the path.

**Measured.**

- **Round 1, the up through the HID event tap.** Escape through the HID event tap, then the up through the HID event tap. The file was dropped into the folder, and so moved, since both were on the same volume. The hardware state read the button up at once. The tap then saw only mouse moved events, and no left mouse dragged, until a new press and release 4.5 s later.
- **Round 2, the up through the session event tap, run twice.** Escape through the HID event tap, then the up through the session event tap. Both times Finder cancelled the drag: the icon slid back, and the file stayed outside the folder. The hardware state read the button held until the person let go, in 10 of 10 samples and in 19 of 19. The combined session state read it up in 9 of 10 and in 19 of 19. The real left mouse up, without the mark, reached the tap 970 ms and 1837 ms after the posted up.
- **Not counted.** An earlier attempt at round 1 drove the drag with the mouse of another machine, through the installed lanhop, which posted those events on the MacBook. lanhop marks the events it posts with the same value as the probe, so the probe could not tell them apart.

Round 1 ran once and round 2 twice, with a trackpad only.

**Decision.**

- The first session's result, that a posted Escape and a posted mouse up end Finder's drag without a drop, was most likely only because nothing under the cursor took the drop at that moment. With the up through the HID event tap, round 1 dropped the file.
- lanhop ends a source drag on a Mac with Escape through the HID event tap, then, 20 ms later, the left mouse up through the session event tap (`macos.EndSourceDrag`, PR #95).
- As a guard, lanhop first shows a window of its own under the cursor, which takes no drop (`macos.ShieldCursor`, PR #89). So even an up that drops lands on a window that refuses it.
- The hardware state keeps the button held until the person lets go, and the real release still reaches a tap at the session level. So lanhop's capture can take that release as the one that drops the file on the neighbour.
