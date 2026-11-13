# 8. Copies follow focus, through the central

Date: 2026-11-13 · Status: accepted

## Context

Side machines connect only to the central. A copy made on one side machine and pasted on another has to pass through the central.

The clipboard often holds passwords and private messages. Some machines on a desk may belong to an employer. On macOS, reading what another program copied may make the system ask the person (see the [plan](../../specs/005-clipboard-sharing/plan.md)).

A copy can move at two moments: as soon as it is made, to every machine, or when the person moves to another machine.

## Decision

- A machine reads its clipboard when focus leaves it, and only if the clipboard changed since lanhop last read or wrote it. A side machine sends the copy to the central.
- The central keeps the desk's last copy in memory, numbered, and remembers which copy each machine holds.
- When focus arrives at a machine that does not hold the desk's last copy, the central sends it there. A copy that reaches the central while a side machine has focus goes to that machine at once. The central writes its own clipboard when focus comes back to it.
- Only plain UTF-8 text up to 1 MiB is carried. Anything else is not sent, and the other clipboards stay as they were.

## Consequences

- A copy reaches only the machines the person moves to after making it.
- The clipboard is read once per copy that leaves a machine, not on every change.
- Crossing does not wait for a copy. The copy follows focus by a few milliseconds.
- The central holds the last copy in memory, even when the copy goes from one side machine to another.
- A copy made by a program on a machine without focus moves only when focus next leaves that machine.
- When the central restarts, the desk's last copy is lost. Each clipboard keeps what it holds.

## Alternatives

- **Send every copy to every machine at once.** Simpler, and a copy is ready everywhere, but every password copied on one machine lands on all of them, and every change of the clipboard is read.
- **Side machines connected to each other.** Each pair would need pairing and a connection. The desk is a star around the central, and copies follow the same paths as the keyboard and mouse.
