# Feature Specification: Switching through the screen edges

**Created**: 2026-11-11 · **Status**: Draft · **Depends on**: [spec 001](../001-hotkey-switching/spec.md)

I push the cursor past the edge of one screen and it appears on the machine that sits on that side, as if all the machines were one wide desktop. A small text file says where each machine sits.

The hotkey of spec 001 keeps working. The first release ships both.

## Terms

- **Layout**: the description of where each machine sits in relation to the others.
- **Neighbour**: the machine on the other side of an edge, according to the layout.
- **Crossing**: the moment the cursor leaves one machine and enters its neighbour.

Sharer, receiver, focus and pairing mean the same as in spec 001.

## User scenarios

### Story 1 - Cross to the machine next to me (Priority: P1)

My laptop sits to the right of my desktop. I move the cursor to the right edge of the desktop screen, keep going, and the cursor shows up on the left edge of the laptop. The keyboard follows the cursor.

**Why this priority**: with three or more machines, remembering which number is which machine is the hard part. Position on the desk needs no memory.

**Independent test**: with two paired machines and a layout that places one to the right of the other, cross to the right, type, and cross back to the left.

**Acceptance scenarios**:

1. **Given** the layout places machine B to the right of machine A, **When** I push the cursor through the right edge of A, **Then** the cursor appears on the left edge of B at the matching height, and the keyboard and mouse act on B.
2. **Given** focus is on B, **When** I push the cursor through the left edge of B, **Then** the cursor appears on the right edge of A at the matching height, and focus is on A.
3. **Given** A and B have screens of different sizes, **When** I cross in the middle of the edge of one, **Then** I arrive in the middle of the edge of the other.
4. **Given** an edge has no neighbour in the layout, **When** I push the cursor against it, **Then** nothing happens and the cursor stays on that screen.
5. **Given** the neighbour is not connected, **When** I push the cursor against its edge, **Then** nothing happens and the cursor stays on that screen.
6. **Given** a machine has more than one monitor, **When** I move between its own monitors, **Then** no crossing happens. Crossing happens only at the outer edges of its whole desktop.

### Story 2 - Describe my desk in a file (Priority: P1)

I write a short text file that draws my desk: which machine is on the left, which is on the right, which is above or below.

**Why this priority**: without the layout there is no neighbour to cross to.

**Independent test**: write a layout with three machines in a row and one below, start lanhop, and check each crossing against the drawing.

**Acceptance scenarios**:

1. **Given** a layout file with the names of my paired machines, **When** lanhop starts, **Then** it uses that layout.
2. **Given** I edit the layout file while lanhop runs, **When** I save it, **Then** the new layout applies without restarting.
3. **Given** the layout names a machine that is not paired, repeats a name or cannot be read, **When** lanhop loads it, **Then** it says which line is wrong and why, and keeps the last good layout.
4. **Given** there is no layout file, **When** lanhop starts, **Then** switching by hotkey still works and lanhop says that edge switching is off until a layout exists.
5. **Given** I want to check my file, **When** I ask lanhop to show the layout, **Then** it prints the desk as it understood it, with the neighbours of each machine.

### Story 3 - Do not cross by accident (Priority: P2)

Reaching for a button or a scroll bar at the edge of the screen does not throw my cursor to another machine.

**Why this priority**: accidental crossings are the most common complaint about tools of this kind.

**Independent test**: touch the edge quickly, drag a window to the edge and move the cursor into a corner. None of them crosses.

**Acceptance scenarios**:

1. **Given** the default settings, **When** the cursor touches an edge and comes back at once, **Then** no crossing happens. Crossing needs the cursor to keep pushing for a short moment.
2. **Given** a mouse button is held, **When** the cursor pushes against an edge, **Then** no crossing happens.
3. **Given** the cursor is in a corner of the screen, **When** it pushes against the edge, **Then** no crossing happens, so the corner shortcuts of the desktop still work.
4. **Given** I want a different behaviour, **When** I change the push time or the corner size in the settings, **Then** lanhop follows them.
5. **Given** I want to stay on one machine for a while, **When** I press the lock hotkey, **Then** the edges stop crossing until I press it again.

## Edge cases

- The layout places two machines on the same spot.
- The sharer is not in the layout.
- A machine in the middle of a row is off: the cursor does not jump over it.
- The cursor crosses while a key is held.
- The screen resolution or the monitor arrangement changes while lanhop runs.
- An application has locked the cursor (a game, a remote desktop window).

## Requirements

### Functional requirements

- **FR-001**: The layout MUST be a plain text file that a person can write by hand, with the same format on every supported system.
- **FR-002**: The layout MUST place machines to the left, to the right, above and below one another.
- **FR-003**: The layout MUST refer to machines by the names given when they were paired.
- **FR-004**: Pushing the cursor through an edge that has a connected neighbour MUST move focus to that neighbour.
- **FR-005**: After a crossing, the cursor MUST appear on the opposite edge of the neighbour, at the position that matches where it left, in proportion to the two edges.
- **FR-006**: Crossing back MUST work from any machine with focus, whatever system it runs.
- **FR-007**: Edges without a neighbour, or whose neighbour is not connected, MUST behave as normal screen edges.
- **FR-008**: A crossing MUST NOT happen while a mouse button is held, in a screen corner, or before the cursor has pushed against the edge for the configured time.
- **FR-009**: The user MUST be able to lock focus on the current machine with a hotkey.
- **FR-010**: A crossing MUST follow every rule of spec 001 for a focus change: no key stays held and a lost connection returns focus to the sharer.
- **FR-011**: A change to the layout file MUST apply without restarting, and a broken file MUST NOT replace a working layout.
- **FR-012**: lanhop MUST report each problem in a layout file with its line and its cause.
- **FR-013**: On a system or desktop session where lanhop cannot find the cursor position of the sharer, it MUST say so at start and keep working with the hotkey.

### Out of scope for this feature

- Placing machines per monitor. Each machine is one block, with all its monitors.
- A graphical editor for the layout.
- Sending the layout from one machine to the others. A machine that may act as sharer needs its own copy of the file.
- Diagonal neighbours.

## Success criteria

- **SC-001**: A person writes a layout for three machines in under 2 minutes, following only the README.
- **SC-002**: Crossing takes less than 100 ms from the end of the push to the cursor showing on the neighbour.
- **SC-003**: After 50 crossings back and forth, the cursor still arrives at the matching position, with no drift.
- **SC-004**: In one hour of normal use on one machine, with the default settings, no accidental crossing happens.
- **SC-005**: Every acceptance scenario passes with a Linux sharer and with a macOS sharer.

## Assumptions

- The machines are paired and connected as spec 001 describes.
- The user knows the names of the machines, which `lanhop peers` lists.

## Open questions

1. **Linux as sharer.** Which desktop sessions do your Linux machines run, X11 or Wayland? It decides how lanhop finds the cursor on a Linux sharer (see the plan).
