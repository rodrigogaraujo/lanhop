# Feature Specification: Clipboard sharing

**Created**: 2026-11-13 · **Status**: Draft · **Depends on**: [spec 001](../001-hotkey-switching/spec.md)

I copy text on one machine, move to another machine with the keyboard and mouse, and paste it there. It works in every direction: from the central to a side machine, from a side machine to the central, and from one side machine to another.

This spec covers plain text. Copying a file and pasting it on another machine, and dragging a file across a screen edge, come later.

## Why

With lanhop, one keyboard and mouse drive every machine on the desk, but text still cannot cross from one machine to another. Today a person sends it to themselves by chat or by email to move a link, a command or an error message. The owner of the project asked for it in these words: "if I click copy on one, I have to be able to take it to the other", and "it would save a lot".

## Terms

- **Central**: the machine with the keyboard and mouse. It shares them.
- **Side machine**: a machine controlled by the central. It receives.
- **Copy**: text that a person puts on the clipboard of a machine, with the copy command of any application.
- **The desk's last copy**: the most recent copy that the central knows about, from any machine.

Sharer, receiver, focus and pairing mean the same as in spec 001. Side machines connect only to the central, never to each other.

## User scenarios

### Story 1 - Copy here, paste there (Priority: P1)

I copy a line on the central, move to a side machine and paste it. Then I copy something on the side machine, come back to the central and paste it.

**Why this priority**: it is the whole feature for a desk with two machines.

**Independent test**: with a central and a side machine connected, copy on one, move to the other by hotkey or through the edge, and paste. Do it in both directions.

**Acceptance scenarios**:

1. **Given** focus is on the central, **When** I copy text and move to a side machine, **Then** pasting on the side machine gives that text.
2. **Given** focus is on a side machine, **When** I copy text and move to the central, **Then** pasting on the central gives that text.
3. **Given** I copied on a machine, **When** I move to another machine and paste right away, **Then** the text is already there. I do not have to wait for it.
4. **Given** I copy text with formatting, such as part of a web page, **When** I paste on another machine, **Then** I get the same text without the formatting.
5. **Given** text in any language, with emoji, tabs or several lines, **When** I paste it on another machine, **Then** it is the same text, character for character.

### Story 2 - Copy on one side machine, paste on another (Priority: P1)

I copy on the laptop on my left and paste on the laptop on my right. Both are side machines.

**Why this priority**: a desk with three or more machines has several side machines, and moving text between them is as common as moving it to the central.

**Independent test**: with a central and two side machines, copy on the first side machine, move to the second, through the central or straight to it by hotkey, and paste.

**Acceptance scenarios**:

1. **Given** focus is on side machine A, **When** I copy text and move straight to side machine B, **Then** pasting on B gives that text.
2. **Given** I copied on A and then visited the central, **When** I move to B and paste, **Then** I get the text from A, unless I copied something newer on the central.
3. **Given** I copied on A, **When** I move to B, **Then** the text reaches B through the central. A and B never connect to each other.

### Story 3 - A copy goes only where I go (Priority: P1)

What I copy reaches only the machines I move to after copying. A machine I do not visit does not get it.

**Why this priority**: people copy passwords and private messages. A copy should not be spread to every machine on the desk without reason.

**Independent test**: with a central and two side machines, copy on the central, move to side machine A, and check that side machine B still has its own clipboard.

**Acceptance scenarios**:

1. **Given** I copy on the central, **When** I move to side machine A and never to B, **Then** the clipboard of B does not change.
2. **Given** a side machine that I have not visited since my last copy, **When** I move to it, **Then** it gets that copy at that moment.
3. **Given** I copy on a machine and stay on it, **When** I do not move, **Then** no other machine gets the copy.
4. **Given** I copy on side machine A and move straight to side machine B, **When** the copy passes through the central, **Then** the central keeps it in memory, and writes it to its own clipboard only when focus comes back to the central.

### Story 4 - A desk with an older lanhop keeps working (Priority: P1)

I update some machines to the version with clipboard sharing and leave others on lanhop 0.1.0. Keyboard and mouse keep working on all of them.

**Why this priority**: a person updates one machine at a time. An update must not cut off the machines that are not updated yet.

**Independent test**: connect a central with clipboard sharing to a side machine on 0.1.0, and the other way around. Use the keyboard and mouse on both, and copy on both.

**Acceptance scenarios**:

1. **Given** a central with clipboard sharing and a side machine on 0.1.0, **When** they connect, **Then** keyboard and mouse work as before, and the side machine neither gets nor gives copies.
2. **Given** a central on 0.1.0 and a side machine with clipboard sharing, **When** they connect, **Then** keyboard and mouse work as before, and no copy moves.
3. **Given** a desk where some side machines have clipboard sharing and some do not, **When** I copy on one that has it, **Then** the others that have it get the copy as in story 2, and the ones that do not are not affected.

### Story 5 - Switch it off on a machine (Priority: P2)

On a machine where I do not want copies to arrive or to leave, such as a work laptop, I switch clipboard sharing off.

**Why this priority**: the clipboard holds private things, and some machines have rules about where data may go.

**Independent test**: switch it off on a side machine, then copy on it and on the central, and move between them.

**Acceptance scenarios**:

1. **Given** clipboard sharing is off on a side machine, **When** I copy on it and move away, **Then** no other machine gets that copy.
2. **Given** clipboard sharing is off on a side machine, **When** I copy elsewhere and move to it, **Then** its clipboard does not change.
3. **Given** clipboard sharing is off on the central, **When** I copy on any machine, **Then** no copy moves anywhere, because every copy passes through the central.
4. **Given** lanhop is running, **When** I switch clipboard sharing off or on, **Then** the change applies from the next time I move between machines, and I restart nothing.
5. **Given** a new install, **When** I have not switched anything, **Then** clipboard sharing is on.

### Story 6 - What is not text stays where it is (Priority: P2)

When I copy an image, a file, or a very long text, lanhop does not send it, and the other machines keep what they had on their clipboard.

**Why this priority**: the first version carries text only. What it cannot carry must fail in a way that is easy to understand.

**Independent test**: copy an image, a file in the file manager, and a text larger than the limit, and move to another machine each time.

**Acceptance scenarios**:

1. **Given** I copy an image or a file, **When** I move to another machine and paste, **Then** I get what that machine's clipboard held before, and lanhop's log says that the copy was not text.
2. **Given** I copy a text larger than 1 MiB, **When** I move to another machine, **Then** the copy is not sent, and lanhop's log says that it was over the limit.
3. **Given** a large text is on its way to another machine, **When** I type or move the mouse, **Then** the keyboard and mouse respond as usual.

## Edge cases

- I copy and move away at once, before the application I copied from has handed the text over.
- I move through two or three machines faster than a large copy can travel.
- I copy the same text twice in a row, or copy on a machine the same text that lanhop has just put there.
- A program changes the clipboard of a machine that does not have focus.
- The connection to a machine is lost while a copy is on its way to it.
- The central restarts. The desk's last copy is gone, and each machine keeps what its clipboard holds.
- A side machine has no graphical session, such as a Linux machine still at its login screen.
- A Linux machine runs a Wayland session or an X11 session.
- On a Mac, macOS asks whether lanhop may read what other applications copied.
- A password manager empties the clipboard of the machine where I copied after a while. The copies that lanhop made on other machines stay.

## Requirements

### Functional requirements

- **FR-001**: Text copied on a machine MUST be ready to paste on the next machine that gets focus, in every direction between the central and the side machines.
- **FR-002**: A copy made on a side machine MUST reach other side machines through the central.
- **FR-003**: The clipboard of a machine MUST change only when that machine gets focus after a copy was made elsewhere. The central MUST keep the copies that pass through it in memory until focus comes back to it.
- **FR-004**: lanhop MUST carry plain text only, encoded as UTF-8, up to 1 MiB. Formatted text MUST arrive as its plain text.
- **FR-005**: When a copy is not text, or is larger than the limit, lanhop MUST NOT send it, MUST leave the clipboards of the other machines as they were, and MUST say why in its log.
- **FR-006**: Copies MUST travel only over the encrypted connection between paired machines of spec 001. lanhop MUST NOT write them to disk.
- **FR-007**: Keyboard and mouse MUST keep responding while a copy is on its way.
- **FR-008**: A machine on lanhop 0.1.0 MUST keep working with keyboard and mouse when it connects to a machine with clipboard sharing, in either role.
- **FR-009**: A person MUST be able to switch clipboard sharing off and on for each machine, without restarting lanhop. It MUST be on after a new install.
- **FR-010**: lanhop MUST NOT send a copy back to the machine it came from, and MUST NOT treat the text it writes to a clipboard as a new copy.
- **FR-011**: On a machine where lanhop cannot reach the clipboard, it MUST say so in its log once, and the keyboard and mouse MUST keep working.
- **FR-012**: Clipboard sharing MUST work with macOS and Linux in every combination of central and side machine. On Linux it MUST work in X11 sessions and in Wayland sessions of GNOME.

### Out of scope for this feature

- Copying a file on one machine and pasting it on another. It is the next step if the text version works well.
- Dragging a file from one screen to another.
- Images and formatted text.
- The Linux primary selection: selecting text and pasting it with the middle button.
- Emptying the copies on other machines when the clipboard of the machine where I copied is emptied.
- A clipboard history.
- Windows. It is spec 003.

## Success criteria

- **SC-001**: A text of up to 64 KiB copied on one machine can be pasted on the next machine less than 300 ms after focus arrives there, on a wired local network.
- **SC-002**: While a 1 MiB copy travels, no key or mouse movement arrives more than 50 ms later than it would without the copy.
- **SC-003**: A side machine on 0.1.0 connected to a central with clipboard sharing passes every acceptance scenario of spec 001.
- **SC-004**: Every acceptance scenario of stories 1, 2 and 3 passes from a Mac to a Mac, from a Mac to Linux and from Linux to a Mac.

## Assumptions

- The machines are paired and connected as spec 001 describes, and lanhop runs in the session of the person who uses the desktop on each machine, as the installer of [spec 008](../008-permissions-checked/spec.md) sets it up.
- Each machine has a clipboard that the person uses with the usual copy and paste commands.
