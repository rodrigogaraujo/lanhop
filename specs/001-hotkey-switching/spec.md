# Feature Specification: Linux and macOS with hotkey switching

**Created**: 2026-11-11 · **Status**: Draft

The first version of lanhop that a person can use every day: one keyboard and one mouse, plugged into one machine, control the other machines on the desk. Linux and macOS machines work together in any combination: either system can be the sharer or a receiver.

## Terms

- **Sharer**: the machine with the physical keyboard and mouse.
- **Receiver**: any other machine. It replays what the sharer sends.
- **Focus**: the machine that currently receives the input. Exactly one machine has focus at a time.
- **Pairing**: the one-time step in which two machines agree to trust each other.

## User scenarios

### Story 1 - Control a second machine (Priority: P1)

I press a hotkey and my keyboard and mouse start controlling the other machine. I press another hotkey and they control my own machine again.

**Why this priority**: this is the product. Everything else supports it.

**Independent test**: with two paired machines, switch focus, type a sentence and click a button on the receiver, then switch back and type on the sharer.

**Acceptance scenarios**:

1. **Given** two paired machines with lanhop running, **When** I press the hotkey for the receiver, **Then** typing and mouse movement act on the receiver and no longer act on the sharer.
2. **Given** focus is on the receiver, **When** I press the hotkey for the sharer, **Then** typing and mouse movement act on the sharer again.
3. **Given** focus is on the receiver, **When** I type with modifiers, hold a key, scroll and drag with a button held, **Then** each of them works as it would with a keyboard and mouse plugged into the receiver.
4. **Given** the sharer and the receiver use different keyboard layouts, **When** I type on the receiver, **Then** the receiver applies its own layout.
5. **Given** a Linux sharer and a macOS receiver, or a macOS sharer and a Linux receiver, **When** I switch focus and use the keyboard and mouse, **Then** scenarios 1 to 4 hold, including double click, drag and key repeat.
6. **Given** one machine runs Linux and the other macOS, **When** I press a modifier key, **Then** it acts as its counterpart on the other system: Control as Control, Alt as Option and Super as Command.

### Story 2 - Pair two machines safely (Priority: P1)

Before a machine can control another, I confirm on both of them that they are the machines I mean.

**Why this priority**: the tool carries every keystroke, passwords included. Shipping without pairing would be shipping a keylogger.

**Independent test**: pair two machines by comparing the codes shown on both screens. Then try to connect from a third, unpaired machine and see it rejected.

**Acceptance scenarios**:

1. **Given** two machines that have never met, **When** I start pairing on both, **Then** each shows its own code and the code it sees for the other, and pairing completes only after I accept on both.
2. **Given** a paired sharer, **When** an unpaired machine connects to it, **Then** the connection is refused and no input is sent.
3. **Given** a paired receiver, **When** a machine that is not its sharer tries to send it input, **Then** the receiver refuses it.
4. **Given** two paired machines, **When** someone captures the network traffic between them, **Then** the keystrokes cannot be read.
5. **Given** a paired machine, **When** I remove it from the list of trusted machines, **Then** it can no longer connect.

### Story 3 - Never get locked out (Priority: P2)

Whatever fails, I can still use the machine in front of me.

**Why this priority**: a tool that takes over the keyboard must fail safe. One lockout is enough for a person to uninstall it.

**Independent test**: with focus on the receiver, unplug the network cable. Focus returns to the sharer within a few seconds and no key keeps repeating on either machine.

**Acceptance scenarios**:

1. **Given** focus is on a receiver, **When** the connection to it is lost, **Then** focus returns to the sharer within 3 seconds.
2. **Given** a key is held while the connection is lost, **When** the receiver notices the loss, **Then** it releases every held key and button.
3. **Given** focus is on a receiver, **When** lanhop on the sharer stops for any reason, **Then** the keyboard and mouse control the sharer again immediately.
4. **Given** a receiver lost its connection, **When** the sharer is reachable again, **Then** the receiver reconnects without my help.
5. **Given** I switch focus while holding keys, **When** the switch happens, **Then** no key stays held on the machine that lost focus.

### Story 4 - Several machines and several devices (Priority: P2)

I have more than two machines, and sometimes I plug in another keyboard or mouse.

**Why this priority**: the target user has four or five machines. Two machines prove the idea, several make it useful.

**Independent test**: pair three receivers, give focus to each by its number, then plug a second mouse into the sharer and use it on a receiver.

**Acceptance scenarios**:

1. **Given** up to eight paired receivers, **When** I press the hotkey with a machine's number, **Then** that machine gets focus.
2. **Given** I press the hotkey for a receiver that is not connected, **When** nothing can be sent to it, **Then** focus stays where it was.
3. **Given** lanhop is running, **When** I plug a keyboard or mouse into the sharer, **Then** it is shared like the others without restarting anything.

### Story 5 - Understand what is wrong (Priority: P3)

When the setup is incomplete, lanhop tells me what to fix.

**Why this priority**: reading input needs permissions that most users have never set. A clear message decides whether a newcomer gets through the first run.

**Independent test**: run lanhop on Linux as a user without access to the input devices, and on macOS without the Accessibility permission, and read the message.

**Acceptance scenarios**:

1. **Given** my Linux user cannot read the input devices or create a virtual device, **When** I start lanhop, **Then** it names the missing permission and the command that grants it.
2. **Given** lanhop has no Accessibility permission on macOS, **When** I start it, **Then** it names the permission and the System Settings pane where I grant it.
3. **Given** I want to see what lanhop would share, **When** I list the devices, **Then** I see each keyboard and mouse it found.

## Edge cases

- The sharer has no keyboard or no mouse at start, and one is plugged in later.
- Two receivers are paired with the same name.
- The hotkey is pressed while a mouse button is held (in the middle of a drag).
- The receiver is suspended or its lid is closed while it has focus.
- The same machine is started as sharer twice.
- A machine changes its IP address between sessions.
- A macOS sharer has a password field in front, which makes macOS hide the keyboard from every other program.
- A macOS receiver is at its login window or lock screen.

## Requirements

### Functional requirements

- **FR-001**: The sharer MUST send keyboard keys, mouse buttons, relative mouse movement and vertical and horizontal scrolling to the machine with focus.
- **FR-002**: While a receiver has focus, input MUST NOT act on the sharer.
- **FR-003**: Focus MUST change with a hotkey that selects a machine by number, where number 1 is always the sharer. The hotkey MUST be configurable.
- **FR-004**: Every connection MUST be encrypted and both sides MUST be authenticated. There MUST NOT be an option to turn this off.
- **FR-005**: Two machines MUST be paired before any input travels between them. Pairing MUST require a person to accept on both machines after comparing the codes shown.
- **FR-006**: A machine MUST refuse connections from any machine it is not paired with.
- **FR-007**: A user MUST be able to list the trusted machines and remove one.
- **FR-008**: When the connection to the machine with focus is lost, focus MUST return to the sharer.
- **FR-009**: A machine that loses focus or its connection MUST end with no key or button held.
- **FR-010**: A receiver MUST reconnect by itself after losing its connection.
- **FR-011**: The sharer MUST pick up keyboards and mice plugged in while it runs.
- **FR-012**: On Linux, lanhop MUST work the same on X11, on Wayland and on a text console.
- **FR-013**: lanhop MUST run without root when the user has granted it access to input (device permissions on Linux, Accessibility on macOS), and MUST say which permission is missing when it does not.
- **FR-014**: The private key of a machine MUST be readable only by its owner.
- **FR-015**: A Linux machine and a macOS machine MUST be able to take either role, sharer or receiver, with each other.
- **FR-016**: Keys MUST be translated between the two systems by their meaning, and the user MUST be able to swap Alt and Super so that the keys next to the space bar match the keyboard in use.
- **FR-017**: Held keys MUST repeat on a macOS receiver as they do with a keyboard plugged into it.

### Out of scope for this feature

- Switching by pushing the cursor through a screen edge (spec 002)
- Finding machines automatically (spec 003). The receiver is given the sharer's address.
- Clipboard sharing (spec 004)
- Running as a system service, packages, udev rules and launch agents (spec 005). Setup is documented by hand.
- Touchpads, touchscreens, tablets and game controllers. Only keyboards and relative mice are shared.
- Keyboard LEDs (Caps Lock and Num Lock state) on the sharer.
- Controlling the macOS login window and lock screen.
- Capturing the keyboard on a macOS sharer while a password field has secure input on.
- Connections across the internet. Machines are on the same local network.

## Success criteria

- **SC-001**: A new user pairs two machines and types on the second one in under 5 minutes, following only the README.
- **SC-002**: On a wired local network, lanhop adds less than 5 ms between a key press on the sharer and the same event on the receiver, for 99% of events.
- **SC-003**: Typing 1,000 keys and moving the mouse for 60 seconds on a receiver loses and duplicates no event.
- **SC-004**: After the network cable is pulled, the user controls the sharer again in 3 seconds or less, with no key held on either machine.
- **SC-005**: An unpaired machine cannot make a receiver replay a single event.
- **SC-006**: The Linux binary passes its end-to-end tests on a text console, with no desktop session.
- **SC-007**: Every acceptance scenario passes in the four combinations: Linux to Linux, Linux to macOS, macOS to Linux and macOS to macOS.

## Assumptions

- Linux machines have a kernel that offers `evdev` and `uinput` (version 4.5 or newer).
- macOS machines run macOS 13 or newer, and the user can grant the Accessibility permission.
- The machines sit on the same desk, so a person can read both screens while pairing.
- The local network is not trusted: other devices on it may listen or try to connect.

## Open questions

1. **Default hotkey.** The proposal is `Ctrl+Alt+<number>`. Does it collide with anything you use daily?
2. **Hotkey leak.** In this version, on a Linux sharer the hotkey is also seen by the application in front (see the plan). Is that acceptable for a first release?
3. **Which machine has the keyboard.** It decides whether macOS capture or macOS replay is built first.
