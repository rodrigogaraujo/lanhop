# 8. Copies follow focus, through the central

Date: 2026-11-13 · Status: accepted

## Context

Side machines connect only to the central. A copy made on one side machine and pasted on another has to pass through the central.

The clipboard often holds passwords and private messages. Some machines on a desk may belong to an employer. On macOS, reading what another program copied may make the system ask the person (see the [plan](../../specs/005-clipboard-sharing/plan.md)).

A copy can move at two moments: as soon as it is made, to every machine, or when the person moves to another machine.

## Decision

- **Reading.** A machine reads its clipboard when focus leaves it, and only if the clipboard changed since lanhop last read it or wrote it, or since lanhop started. A copy made before lanhop started moves only after it is copied again.
- **Answering.** A side machine answers every leave: with what it read, with a withdrawal when the change is not something lanhop carries (empty, not text, or over 1 MiB), or with word that its clipboard did not change.
- **The desk's last copy.** The central keeps it in memory, numbered, and remembers which copy each machine holds. A withdrawal, or a change on the central that lanhop does not carry, empties it. Nothing is handed out until the next copy, and the clipboards of the other machines stay as they are.
- **Handing out.** The machine that has focus gets the desk's last copy if it does not hold it yet. The central hands it out only after every machine that focus has left since the last handout has answered, and after it has read its own clipboard when focus left the central. A side machine that does not answer within the silence limit of the connection is not waited for. This holds for the central as well as for the side machines.
- **Leaving in the middle.** When focus leaves a side machine while a copy is on its way to it, the central stops sending the copy and does not count it as held. A side machine writes a copy only while it has focus.
- **Writing.** lanhop writes a copy to the clipboard of the machine with focus, unless the person copied something on that machine after focus arrived there. That copy is newer and stays, and moves when focus leaves.
- **Content.** Only plain UTF-8 text up to 1 MiB is carried. The central never sends a withdrawal, and a side machine ignores one.

## Consequences

- A copy reaches only the machines the person moves to after making it.
- A copy that was withdrawn is never handed out, even when focus moves straight from one side machine to another.
- When a password manager empties the clipboard of the machine where the password was copied, and focus then leaves that machine, the password is no longer handed out. The machines that already got it keep it.
- The clipboard is read once per change that leaves a machine, not on every change.
- Crossing does not wait for a copy. Keys and the mouse move to the next machine at once, and the copy follows after the answer of the machine that focus left. SC-001 of the spec sets how much later. A paste pressed in the same instant may still get the previous text.
- The central holds the last copy in memory, even when the copy goes from one side machine to another.
- A copy made by a program on a machine without focus moves only when focus next leaves that machine. A copy that arrives with focus replaces it.
- When the central restarts, the desk's last copy is lost. Each clipboard keeps what it holds.

## Alternatives

- **Send every copy to every machine at once.** Simpler, and a copy is ready everywhere, but every password copied on one machine lands on all of them, and every change of the clipboard is read.
- **Hold the keys and the mouse of the machine that gets focus until its copy is there.** A paste could never overtake the copy, but the mouse would stall after each crossing while the machine that lost focus reads its clipboard and answers.
- **Side machines connected to each other.** Each pair would need pairing and a connection. The desk is a star around the central, and copies follow the same paths as the keyboard and mouse.
