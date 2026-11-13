# Feature Specification: File sharing

**Created**: 2026-11-13 · **Status**: Draft · **Depends on**: [spec 005](../005-clipboard-sharing/spec.md), [spec 002](../002-edge-switching/spec.md)

I copy a file on one machine and paste it in a folder of another. Later, I drag a file from one screen and drop it on the screen of the machine next to it.

This spec has two stages. Stage A is copy and paste of files, and it builds on the clipboard sharing of spec 005. Stage B is dragging a file across a screen edge, and it changes how the edges of spec 002 behave while a file is dragged.

## Why

Text already moves between machines with copy and paste (spec 005). Files still travel by chat, email or a USB stick. The owner of the project asked for it in these words: "dragging a document would be a dream", and wants it in the same release as text.

## Terms

- **Central**, **side machine**, **copy**, **focus** and **crossing** mean the same as in specs 001, 002 and 005.
- **File copy**: one or more files or folders that a person copies in the file manager (Finder, Nautilus, the file dialog of an application).
- **Drag**: a file held with the mouse button down, moving toward a place to drop it.
- **Arrival folder**: where lanhop keeps files that came from another machine until the person pastes or drops them.

## User scenarios

### Story 1 - Copy a file here, paste it there (Priority: P1, stage A)

I select a file in the file manager of one machine and copy it. I move to another machine and paste in a folder. The file appears there.

**Why this priority**: it covers most of what dragging would, with the gestures people already use, and it needs no change to the edges.

**Independent test**: copy a document on one machine, move to another by hotkey or edge, paste in a folder, and open the document.

**Acceptance scenarios**:

1. **Given** I copied a file on one machine, **When** I move to another machine and paste in a folder of its file manager, **Then** the file appears there with the same name and content.
2. **Given** I copied several files, or a folder, **When** I paste on the other machine, **Then** all of them arrive, the folder with what it holds.
3. **Given** a large file is on its way, **When** I type or move the mouse, **Then** the keyboard and mouse respond as usual.
4. **Given** a file is on its way, **When** I paste before it has arrived, **Then** the paste gives what the clipboard held before, and the file can be pasted once it has arrived. A paste never creates a file that is cut short.
5. **Given** I copy a file and then copy some text, **When** I move to another machine, **Then** pasting gives the text, the last thing I copied.

### Story 2 - Drag a file across the edge (Priority: P2, stage B)

I start dragging a file on one screen, push it through the edge toward the machine next to it, and drop it in a folder or a window there.

**Why this priority**: it is the gesture the owner asked for, but it needs more of each system than copy and paste does, and on some desktops it cannot work (see the plan).

**Independent test**: drag a file from the desktop of one machine across the edge, and drop it on the desktop of the next one.

**Acceptance scenarios**:

1. **Given** I drag a file on one machine, **When** I push it through an edge that has a neighbour, **Then** the cursor crosses with the file still held, and dropping it in a folder of the neighbour, or in a window that takes dropped files, puts the file there once it has arrived.
2. **Given** I hold the mouse button to move a window or select text, **When** I push against an edge, **Then** nothing crosses, as spec 002 says.
3. **Given** I drag a file across and change my mind, **When** I drag it back across the edge or press Escape, **Then** nothing is dropped and nothing is left behind.
4. **Given** the neighbour cannot take a dragged file, **When** I push a dragged file against that edge, **Then** nothing crosses, and lanhop's log says why.
5. **Given** I drag a file to the Trash or to a sidebar near an edge, **When** the cursor touches that edge on the way, **Then** nothing crosses. A file drag crosses only after a longer push than the cursor alone, never in a corner, and never through the edge where a dock sits.

### Story 3 - Keep control of what arrives (Priority: P2)

Files that arrive take room on the disk of the machine they go to, and some machines should not take files from others.

**Independent test**: switch file sharing off on one machine, copy a file toward it, and look at its arrival folder; then copy a file larger than the limit.

**Acceptance scenarios**:

1. **Given** file sharing is off on a machine, **When** I copy or drag a file toward it, **Then** nothing arrives, and text copy and paste of spec 005 keeps working if it is on.
2. **Given** a file copy larger than the limit, **When** I move to another machine, **Then** it is not sent, and lanhop's log says it was over the limit.
3. **Given** files arrived and were never pasted, **When** a day has passed, **Then** lanhop removes them from the arrival folder.
4. **Given** I pass through a machine on my way to another, **When** files had started to arrive there, **Then** the transfer stops when focus leaves, and what had arrived is removed.
5. **Given** a file copy that does not fit in the free space of the destination, or in the room lanhop allows itself there, **When** I move to it, **Then** nothing is sent, and lanhop's log says why.

## Edge cases

- The file is changed or deleted on the machine where it was copied before it has gone.
- Two files with the same name are pasted in the same folder.
- A file name that one system allows and the other does not, such as a colon or a name that differs only in case.
- A file with no permission to read, a symbolic link, or a special file such as a pipe.
- The disk of the destination fills up while a file arrives.
- The connection is lost in the middle of a file.
- On macOS, the file sits in a folder that macOS protects (Desktop, Documents, Downloads, iCloud Drive, an external or network volume), which a program needs permission to read. The question of macOS appears on the machine the person just left.
- An iCloud file that is not downloaded yet.
- A file cut in the file manager instead of copied: it arrives as a copy, and the original stays where it was.
- A file copy also offers its names or paths as text.
- A machine on lanhop 0.1.0 or 0.1.1, which knows nothing of files.

## Requirements

### Functional requirements

- **FR-001**: A file copy made on a machine MUST be ready to paste in the file manager of the next machine that gets focus, following the rules of spec 005 for who gets which copy.
- **FR-002**: Files MUST arrive whole and with the same content, or not arrive at all. A paste MUST NOT produce a file cut short.
- **FR-003**: Keyboard and mouse MUST keep responding while files travel (as FR-007 of spec 005).
- **FR-004**: Files MUST travel only over the encrypted connection between paired machines, and MUST be written only in the arrival folder until the person pastes or drops them.
- **FR-005**: A file copy larger than the limit, or holding a file that cannot be read, MUST NOT be sent, and lanhop MUST say why in its log.
- **FR-006**: A person MUST be able to switch file sharing off per machine, apart from text.
- **FR-007**: Machines on older versions MUST keep working with keyboard, mouse and text, and MUST NOT be sent files.
- **FR-008**: A dragged file MUST cross an edge only while the system reports a drag of files. Holding a button for anything else MUST keep blocking crossings (FR-008 of spec 002).
- **FR-009**: Files left in the arrival folder MUST be removed after a day.
- **FR-010**: On each system, lanhop MUST say in its log, once, when it cannot share files there and why.
- **FR-011**: A file path that arrives MUST stay inside the arrival folder: lanhop MUST refuse absolute paths, `..` and links, and MUST NOT follow links when it creates files.
- **FR-012**: When a copy offers both files and text, the files MUST win. A machine that takes text but not files MUST NOT get the names or paths of the files as text.
- **FR-013**: lanhop MUST keep the arrival folder under a total size it allows itself, removing the oldest files first, and MUST NOT start a transfer that does not fit.
- **FR-014**: On macOS, files that arrive MUST carry the quarantine mark that macOS gives to downloaded files.

### Out of scope for this feature

- Dragging between machines where the plan finds it cannot work. From or to a Wayland application on Linux is the likely case, deduced and to be measured in task 8.
- Syncing folders, or keeping a copy up to date when the original changes.
- Images and other content copied as data rather than as a file.
- Windows. It is spec 003.

## Success criteria

- **SC-001**: A file of 10 MiB copied on one machine can be pasted on the next one within 2 seconds of focus arriving, on a wired network.
- **SC-002**: While 100 MiB travel over a link of 100 Mbit/s, no key or mouse movement arrives more than 50 ms later than it would without them.
- **SC-003**: Stage A works from a Mac to a Mac, from a Mac to Linux and from Linux to a Mac.
- **SC-004**: Stage B works at least from a Mac to a Mac. The plan says which other directions work.

## Assumptions

- The machines are paired and connected, and text copy and paste of spec 005 works between them.
- The person uses Finder on macOS and Nautilus or another file manager that follows the GNOME conventions on Linux.

## Open questions

1. **Scope of the first release.** Which directions of dragging must the next release have? The plan proposes Mac to Mac first, because the desk is mostly Macs and the Linux machine runs X11.
