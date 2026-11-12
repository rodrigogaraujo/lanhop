# Feature Specification: A drag-and-drop editor for the layout

**Created**: 2026-11-12 · **Status**: Draft · **Depends on**: [spec 002](../002-edge-switching/spec.md)

I say where each computer sits by clicking and dragging, because my desk changes. lanhop writes the layout file for me.

Spec 002 left a graphical editor out. This spec adds it. The layout file stays the only place where the layout lives, and writing it by hand keeps working.

## Terms

- **Desk**: the grid of places on the page. A place holds one machine or is empty.
- **Tray**: the part of the page where the machines that have no place on the desk wait.
- **Box**: a machine on the page, with its name.

Layout, neighbour, sharer and pairing mean the same as in specs 001 and 002.

## User scenarios

### Story 1 - Arrange the desk by dragging (Priority: P1)

I run one command and a page opens in my browser. It shows my machines as boxes. I drag each one to where it sits and press Save.

**Why this priority**: it is the feature. A person who moved a laptop to the other side of the desk fixes the layout in a few seconds, with no file format to remember.

**Independent test**: with two paired machines and no layout file, run the editor, drag both machines to the desk, save, and cross the edge between them.

**Acceptance scenarios**:

1. **Given** paired machines, **When** I run `lanhop layout --edit`, **Then** lanhop prints an address, opens it in the default browser, and the page shows one box for this machine, marked as this machine, and one for each machine `lanhop peers` lists.
2. **Given** a layout file, **When** the page opens, **Then** the machines of the file sit in their places on the desk and the others wait in the tray.
3. **Given** the page, **When** I drag a box to a free place, to another place or back to the tray, **Then** it stays there, and the desk always shows one empty row and one empty column around the machines, so there is room to grow.
4. **Given** boxes on the desk, **When** one moves, **Then** the page says in words the neighbours of each placed machine, or why the arrangement cannot be saved.
5. **Given** an arrangement with this machine on the desk, **When** I press Save, **Then** lanhop writes the layout file, and a running `lanhop share` uses the new layout at once. The page says that it does.
6. **Given** the layout file has comments, **When** the page opens, **Then** it says, before the first save, that saving does not keep them.
7. **Given** the page, **When** I press Done, or Ctrl+C in the terminal, **Then** lanhop stops and nothing of it keeps running.

### Story 2 - Arrange the desk with the keyboard alone (Priority: P2)

I do not use a mouse, or I cannot drag. I still arrange the desk.

**Why this priority**: dragging must not be the only way to do the one thing the page is for.

**Independent test**: with the keyboard alone, move a machine from the tray to the desk, to another place and back, and save.

**Acceptance scenarios**:

1. **Given** the page, **When** I move through it with Tab, **Then** every box, every free place, the tray and the buttons can be reached, and I can see which one I am on.
2. **Given** I am on a box, **When** I press Enter or Space, **Then** the box is picked up and shows it. Pressing again lets it go.
3. **Given** a box is picked up, **When** I press Enter or Space on a free place or on the free place of the tray, **Then** the box moves there and I stay on it.

### Story 3 - Nobody else changes my layout (Priority: P1)

The editor is a web page, so I must be sure that no web site I visit, no other program and no other user of this machine can change my layout through it.

**Why this priority**: the layout decides where my keyboard goes. A tool that carries keystrokes cannot open a door to change that.

**Independent test**: while the editor runs, ask for its page without the token, with another Host, and from another machine. Each is refused.

**Acceptance scenarios**:

1. **Given** the editor runs, **When** another machine on the network tries to reach it, **Then** it cannot: lanhop listens only on the address of the machine itself.
2. **Given** the editor runs, **When** a request arrives without the secret that is in the address lanhop opened, **Then** lanhop refuses it and does nothing.
3. **Given** the editor runs, **When** a request arrives under another host name, **Then** lanhop refuses it.
4. **Given** a request that carries the secret, **When** it names a machine that is neither this one nor a trusted one, places a machine twice or leaves this machine out, **Then** lanhop refuses it and the layout file stays as it was.
5. **Given** any save, **When** lanhop writes, **Then** it writes only the layout file, in one step, so `lanhop share` never reads half a file.

## Edge cases

- There is no layout file yet: the desk starts empty and every machine waits in the tray.
- The layout file has problems: the command prints them, as `lanhop layout` does, and does not open the editor. The person fixes those lines by hand or removes the file.
- The layout file does not name this machine: the page shows it and says that this machine must be placed before saving.
- The machine has no browser, or lanhop cannot open one: the command says so and tells the person to open the address by hand or to write the file by hand.
- A machine is forgotten with `lanhop forget` while the page is open: a save that still places it is refused.
- A row written by hand is longer than the desk grows by dragging: the page shows it whole.
- Done is pressed with changes that were not saved: they are lost. Only Save writes.

## Requirements

### Functional requirements

- **FR-001**: `lanhop layout --edit` MUST serve the editor on this machine, print its address, open it in the default browser and run until Done is pressed on the page or lanhop is interrupted.
- **FR-002**: The page MUST show this machine, marked as this machine, and every trusted machine: on the desk when the layout file places it, in the tray otherwise.
- **FR-003**: A box MUST move, by dragging and by the keyboard alone, to any free place of the desk, to another place and back to the tray.
- **FR-004**: The desk MUST keep one empty row and one empty column around the placed machines, up to nine places a side.
- **FR-005**: The page MUST say in words the neighbours of each placed machine, or the problem that stops a save, and keep that up to date as boxes move.
- **FR-006**: Save MUST write the layout file so that lanhop reads back the same arrangement: rows from top to bottom, machines from left to right, a dot for an empty place, and no empty rows or columns around the machines.
- **FR-007**: lanhop MUST NOT write an arrangement it would refuse to read, nor one that leaves this machine out.
- **FR-008**: The page MUST say that saving does not keep the comments of the file, before the first save, when the file has comments.
- **FR-009**: The page MUST say that a running `lanhop share` uses a saved layout at once.
- **FR-010**: The editor MUST listen only on the loopback address, on a port the system chooses, and MUST refuse every request that lacks the secret of its address or whose Host is not the address it listens on.
- **FR-011**: The editor MUST write only the layout file, replacing it in one step.
- **FR-012**: The page MUST work with no internet connection: it loads nothing from outside lanhop.
- **FR-013**: When no browser opens, the command MUST say so and say how to write the file by hand.
- **FR-014**: The page MUST have only what this spec lists, in plain English text, and stay usable in a narrow window.

### Out of scope for this feature

- Editing the layout of another machine. The editor changes the file of the machine it runs on.
- Pairing, forgetting or renaming machines on the page.
- Keeping the comments of a file written by hand.
- Swapping two machines in one move, undo, and a warning about changes that were not saved.
- Touch screens.

## Success criteria

- **SC-001**: A person arranges three machines and saves in under 30 seconds, without reading the README.
- **SC-002**: A person does the same with the keyboard alone.
- **SC-003**: Every arrangement the page can make is read back by lanhop as the same arrangement.
- **SC-004**: With the editor running, a request without the secret changes nothing, whatever it asks.

## Assumptions

- The machines are paired, as spec 001 describes.
- The person arranges the desk on the machine with the keyboard, which is the one that reads the layout.
