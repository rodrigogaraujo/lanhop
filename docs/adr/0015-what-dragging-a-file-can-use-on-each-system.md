# 15. What dragging a file can use on each system

Date: 2026-11-14 · Status: proposed

ADR 14 is reserved for research task 1 of spec 011 (files on the clipboard).

## Context

Stage B of [spec 011](../../specs/011-file-sharing/spec.md) lets a file dragged on one machine cross the edge with the button held and drop on the neighbour. Its plan rests on a table of what each system allows, none of it measured. Research task 8 measures it before tasks 9 to 13 depend on it, and the plan stops the work for the owner's decision if a direction cannot work.

The measurements were made at the desk on 2026-11-14 with small probe programs (draft PR #74, not for merge). The probes never touched the installed lanhop:

- the Mac probe was its own app (`com.github.rodrigogaraujo.lanhop-dragprobe`), signed ad hoc, with its own Accessibility entry, removed afterwards;
- the Linux probe ran in the person's X11 session with his approval;
- every drag used a throwaway file over a scratch folder.

The machines:

- a MacBook on macOS 26.5.2 with Finder;
- a Linux machine running GNOME on Xorg with Nautilus (GTK 4).

## Measured

| Question | Mac | Linux (X11) |
|---|---|---|
| Does a program in the background see that a file drag started, and which files? | Yes. The drag pasteboard changes, with the left button held. It offers `public.file-url` and `NSFilenamesPboardType` with the plain path of the file. | Yes. `XdndSelection` changes owner through XFIXES. Its `text/uri-list` holds the `file:` address, and the owner also offers the portal types. |
| Can lanhop drop a file there from a source of its own? | Yes. A drag session from a window of the probe, with a file promise, is accepted by Finder with the copy operation. This works while posted events drive the mouse, as lanhop's replay does on a Mac that receives focus. | Yes. A pure Go XDND source, version 5, offering only `text/uri-list` with `XdndActionCopy`, is accepted by Nautilus. Nautilus first asks for the portal types; once they are refused, it takes `text/uri-list`. |
| Can lanhop end the drag left behind on the source without a drop? | Yes. A posted Escape and a posted left mouse up end Finder's drag: the image slides back while the finger still holds the button, and nothing moves. | **No.** Escape and a left button release sent through a uinput device of lanhop do not end Nautilus's drag. A uinput press and then release do not end it either. X keeps reporting button 1 held, because the physical mouse holds it, and the Escape from the uinput keyboard does not cancel the GTK drag. When the person releases, Nautilus drops where the cursor is. |
| Does the cursor still move from lanhop's virtual devices while lanhop holds a pointer grab? | - | Yes. A relative move through uinput moves the cursor by exactly that much under the grab, and the grabbing program gets the motion. |

No pasteboard alert of macOS appeared in any step.

## Decision

These directions keep the plan of spec 011:

- **From a Mac**, to a Mac or to Linux: the drag is seen through the drag pasteboard, and ended on the source with a posted Escape and mouse up.
- **To a Mac**: lanhop drops with a drag session and file promises from a window of its own, as task 10 plans.
- **To Linux in an X11 session**: lanhop drops as a pure Go XDND source with the copy action, as task 11 plans. It grabs the pointer, which does not stop its virtual devices from moving the cursor.

**From Linux** is open. Seeing the drag works, but the plan's way of ending it on the source does not, so Linux to a Mac and Linux to Linux cannot be built as planned. Two techniques are measured next, in a short session at the desk:

1. **Inject the release into the physical mouse.** The kernel lets a process write events to the event node of a device, and they reach X as if that device sent them. So X would see button 1 released by the slave that holds it.
2. **A drop target of lanhop at the edge.** An XdndAware window of lanhop on the edge receives the drop with the copy action. Nautilus then finishes the drag, and nothing moves.

Together they would end the source drag at the crossing without a drop into a real folder. The owner decides between measuring them and leaving dragging from Linux for a later release; this ADR is completed with that choice and those measurements.

## Consequences

- Tasks 10 and 11 and the Mac half of task 9 can start now.
- The Linux half of task 9 (ending the source drag) and task 12 wait for the second session.
- On a Mac, posted events that end the source drag need the Accessibility permission lanhop already has for its capture.
- On Linux, lanhop's XDND source must refuse the portal types and offer `text/uri-list`, which Nautilus takes.
