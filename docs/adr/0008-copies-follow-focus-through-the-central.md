# 8. Copies follow focus, through the central

Date: 2026-11-13 · Status: accepted

## Context

Side machines connect only to the central. A copy made on one side machine and pasted on another has to pass through the central.

The clipboard often holds passwords and private messages. Some machines on a desk may belong to an employer. On macOS, reading what another program copied may make the system ask the person (see the [plan](../../specs/005-clipboard-sharing/plan.md)).

A copy can move at two moments: as soon as it is made, to every machine, or when the person moves to another machine.

## Decision

- A machine reads its clipboard when focus leaves it, and only if the clipboard changed since lanhop last read it or wrote it.
- A side machine sends what it read to the central. When the change is not something lanhop carries (empty, not text, or over 1 MiB), it sends a withdrawal instead.
- The central keeps the desk's last copy in memory, numbered, and remembers which copy each machine holds. A withdrawal, or a change on the central that lanhop does not carry, empties the desk's last copy. Nothing is handed out until the next copy, and the clipboards of the other machines stay as they are.
- The machine that has focus gets the desk's last copy if it does not hold it yet: when focus arrives, or at once when a new copy reaches the central while it has focus. This holds for the central as well as for the side machines.
- lanhop writes a copy to a clipboard only if that clipboard has not changed since lanhop last read it or wrote it. A copy that the person made there in the meantime is newer and stays.
- Only plain UTF-8 text up to 1 MiB is carried.

## Consequences

- A copy reaches only the machines the person moves to after making it.
- When a password manager empties the clipboard of the machine where the password was copied, and focus then leaves that machine, the password is no longer handed out. The machines that already got it keep it.
- The clipboard is read once per change that leaves a machine, not on every change.
- Crossing does not wait for a copy. The copy arrives after focus does, and SC-001 of the spec sets how much later. A paste pressed in the same instant may still get the previous text.
- The central holds the last copy in memory, even when the copy goes from one side machine to another.
- A copy made by a program on a machine without focus moves only when focus next leaves that machine.
- When the central restarts, the desk's last copy is lost. Each clipboard keeps what it holds.

## Alternatives

- **Send every copy to every machine at once.** Simpler, and a copy is ready everywhere, but every password copied on one machine lands on all of them, and every change of the clipboard is read.
- **Hold the input of the machine that gets focus until its copy is there.** A paste could never overtake the copy. But the central cannot know whether a side machine has anything to send until it has read its clipboard, and the mouse would stall after each crossing while it waits.
- **Side machines connected to each other.** Each pair would need pairing and a connection. The desk is a star around the central, and copies follow the same paths as the keyboard and mouse.
