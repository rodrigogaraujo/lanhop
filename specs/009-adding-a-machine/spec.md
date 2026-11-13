# Feature Specification: Adding a machine with one line

**Created**: 2026-11-13 · **Status**: Draft · **Depends on**: [spec 001](../001-hotkey-switching/spec.md), [spec 007](../007-layout-editor/spec.md), [spec 008](../008-permissions-checked/spec.md)

On the machine with the keyboard and mouse I run one command. It gives me one line. I type that line on the new machine, and the new machine ends up connected, placed on the desk, with a hotkey I know.

## Why

On 2026-11-13 the owner of the project set up four machines around one central, with the help of AI sessions on each machine, and said that without them it would not have been possible. The steps that cost the most, as the sessions that helped wrote them down:

- Each new machine took three trips to the central: `lanhop pair` there, `lanhop pair --as` to fix the name, and `lanhop layout` to place it.
- Both machines had to sit in `lanhop pair` at the same time. One stray connection to the port, from a `nc` used to test it, ended the wait.
- The central needed a fixed address, typed on every side machine.
- The installer asked whether the keyboard and mouse were on this machine. On a laptop the answer was yes, and the laptop became a central.
- The names lanhop proposed came from the host names, and were not the names the owner uses.
- Nothing said which number of the hotkey reached which machine, nor which machines were not connected.
- `lanhop` was not on the `PATH` on one Mac, and an installer closed halfway did not pick up where it stopped.

## Terms

- **Central**, **side machine**, **pairing**, **layout** and **hotkey** mean the same as in specs 001, 002 and 007.
- **Join line**: the line that `lanhop add` prints, to type on the new machine.
- **Join code**: the part of the join line that lets the new machine know the central, and the central know the new machine.

## User scenarios

### Story 1 - Add a machine with one line (Priority: P1)

On the central I run `lanhop add office-mac`. It prints a join line. On the new machine I type it. The new machine installs lanhop if needed, joins, and connects. On the central, `lanhop add` says that office-mac joined, which hotkey reaches it, and opens the layout editor with office-mac on the desk.

**Why this priority**: it is the whole feature. It turns three trips to the central, a fixed address and two people in front of two screens into one command and one line.

**Independent test**: with a central running as its service, run `lanhop add office-mac` and type the join line on a machine that has never had lanhop. Count what was typed, and time it from the join line to `connected`.

**Acceptance scenarios**:

1. **Given** the central runs lanhop as its service, **When** I run `lanhop add office-mac`, **Then** it prints a join line for a machine without lanhop and a shorter one for a machine that has it, and waits.
2. **Given** a machine without lanhop, **When** I type the join line, **Then** it installs lanhop as a side machine, joins the central as office-mac, and connects, without asking whether the keyboard and mouse are there.
3. **Given** the new machine joined, **When** `lanhop add` sees it, **Then** it prints its name, its address and its hotkey, and opens the layout editor with the new machine placed next to the others, ready to move.
4. **Given** `lanhop share` is running, **When** a machine joins, **Then** it can take focus at once, with no restart of the central.
5. **Given** the join line was used once, **When** anyone types it again, **Then** the central refuses it.
6. **Given** ten minutes passed, or I pressed Ctrl+C in `lanhop add`, **When** the join line is typed, **Then** the central refuses it, and the new machine says to run `lanhop add` again.

### Story 2 - See the desk at a glance (Priority: P1)

**Independent test**: with three side machines, switch one off, then run `lanhop status` on the central.

**Acceptance scenarios**:

1. **Given** the central has side machines, **When** I run `lanhop status` there, **Then** it lists each one with its hotkey, and says whether it is connected, or since when it is not.
2. **Given** a side machine is off, **When** I press its hotkey, **Then** focus stays where it is, as today, and `lanhop status` names it as not connected.

### Story 3 - An installer that finishes (Priority: P2)

**Independent test**: close the terminal in the middle of the installer, then type the same line again; open a new terminal and type `lanhop`.

**Acceptance scenarios**:

1. **Given** the installer stopped halfway, **When** I type the same line again, **Then** it skips what is done and goes on from where it stopped.
2. **Given** the join already happened when the installer stopped, **When** I type the same join line again, **Then** the installer sees that the central is already trusted and goes on, although the join code is used.
3. **Given** `~/.local/bin` is not on the `PATH`, **When** the installer ends, **Then** a new terminal finds `lanhop`, because the installer added the folder to the profile of the shell, after saying which file it changes.

### Story 4 - The central can change address (Priority: P3)

**Independent test**: give the central another address on the network, and restart a side machine.

**Acceptance scenarios**:

1. **Given** the central got another address from the router, **When** a side machine connects, **Then** it finds the central by its local name, where the network offers local names.

## Edge cases

- A stray connection to the port of `lanhop add`: a scan, a `nc`, a browser.
- Someone else types a wrong code three times while `lanhop add` waits.
- The name given to `lanhop add` is already taken, or is the name of the central.
- Two `lanhop add` at the same time on the central.
- The new machine has lanhop 0.1.1, which has no `lanhop join`.
- The new machine is already paired with this central.
- The new machine was a central before.
- The central has several addresses: Wi-Fi, a cable, a VPN.
- A VPN on the new machine blocks the local network (FortiClient did, on 2026-11-13).
- A side machine that needs Accessibility and Local Network on macOS: the installer asks for them as spec 008 says, after the join.
- More than eight side machines: there are not enough hotkey numbers.

## Requirements

### Functional requirements

- **FR-001**: `lanhop add NAME` on the central MUST print a join line and wait for one machine to join, for at most ten minutes or until Ctrl+C.
- **FR-002**: The join line MUST be enough for a machine without lanhop to install it as a side machine, join and connect, with no other question than the permissions of its system.
- **FR-003**: The new machine MUST know the central by the join code before it sends anything that lets it join, so that a machine in the middle can neither join in its place nor learn the code.
- **FR-004**: A join code MUST work once, and only within the time `lanhop add` waits. Three wrong codes MUST end the wait.
- **FR-005**: A connection that does not complete a join MUST NOT end the wait.
- **FR-006**: The name of the new machine MUST be the one given on the central, and the new machine MUST store the central under the central's own name.
- **FR-007**: A machine that joined MUST be able to take focus at once, without restarting `lanhop share`.
- **FR-008**: `lanhop add` MUST print the hotkey of the new machine and open the layout editor with it placed, or print the address of the editor where no browser opens.
- **FR-009**: `lanhop status` on the central MUST list each side machine with its hotkey and whether it is connected, or since when it is not.
- **FR-010**: Typing the installer line again MUST go on from where it stopped.
- **FR-011**: The installer MUST make `lanhop` reachable from a new terminal.
- **FR-012**: `lanhop pair` MUST keep working as it does, for machines that pair by comparing codes.

### Out of scope for this feature

- Finding the central with no address at all, by broadcast or Bonjour. It is spec 004.
- Adding a machine from the machine itself, with no step on the central.
- Changing hotkey numbers. They stay in the order of `lanhop peers`.
- Windows. It is spec 003.

## Success criteria

- **SC-001**: A Mac that never had lanhop goes from the join line to `connected` with one line typed and the permissions of macOS, in under 3 minutes not counting the clicks of the permissions.
- **SC-002**: Adding a machine takes one trip to the central, one command there, and one line on the new machine.
- **SC-003**: No setup step of the list in "Why" is needed any more for a new machine.

## Assumptions

- The central runs lanhop 0.1.2 or newer as its service, and the new machine can reach it on the local network.
- A person can get the join line from the central to the new machine: by typing it, or through a chat or a note.

## Open questions

None at the moment.
