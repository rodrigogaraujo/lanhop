# 8. Copies follow focus, through the central

Date: 2026-11-13 · Status: accepted

## Context

Side machines connect only to the central. A copy made on one side machine and pasted on another has to pass through the central.

The clipboard often holds passwords and private messages. Some machines on a desk may belong to an employer. On macOS, reading what another program copied may make the system ask the person (see the [plan](../../specs/005-clipboard-sharing/plan.md)).

A copy can move at two moments: as soon as it is made, to every machine, or when the person moves to another machine.

## Decision

- **Who takes part.** A machine takes part while its own switch is on and the other end's last Features frame has bit 0 set. A machine on 0.1.0 never sends Features, so it never takes part. The central starts to take part when its switch turns on while a side machine takes part, or when the first side machine starts to take part. Everything below applies only between machines that take part, except the rule for answering, which every side machine follows.
- **Reading.** A machine reads its clipboard when focus leaves it, and only if the clipboard changed since lanhop last read it or wrote it, or since the machine started to take part: when lanhop started, when its switch was turned on, or when the other end announced bit 0 again. A copy made before any of those moves only after it is copied again. Reads of one machine run one at a time. A read that has not begun when focus leaves that machine again is skipped and counts as no change, and the read of that later leave reads what changed.
- **Answering.** A side machine answers every leave: with what it read, with a withdrawal when the change is not something lanhop carries (empty, not text, over 1 MiB, or marked private by the program that made it), or with word that its clipboard did not change. It answers even a leave it got while it did not take part, and a leave whose read ended after it stopped taking part, both with word that its clipboard did not change, and it sends its answers in the order of its leaves, so that they always pair with its leaves, whatever the switches did in between. Both ends at a desk run a version that follows this rule: an earlier side machine never answers a leave while it does not take part. A Features frame with bit 0 clear stops the central waiting for that machine; its answers still come, and are dropped.
- **The desk's last copy.** The central keeps it in memory, numbered, and remembers which copy each machine holds. A withdrawal, or a change on the central that lanhop does not carry, empties it. Nothing is handed out until the next copy, and the clipboards of the other machines stay as they are. Reads and answers the central waits for count in the order of the leaves they belong to, not in the order they reach the central: one that arrives after the central took the copy or withdrawal of a later leave, or after it gave up on the machine of a later leave or lost it before it answered, changes nothing. So a slow read of the central never brings back a copy older than the answer of a side machine. The one exception is a copy made while a read was under way, as the consequences below say.
- **Handing out.** The machine that has focus gets the desk's last copy if it does not hold it yet: when focus arrives, when it starts to take part while it has focus, and at once when a new copy reaches the central while it has focus. The central hands it out only after every side machine that focus left has answered, and after it has read its own clipboard when focus left the central. This holds for the central as well as for the side machines.
- **No answer.** A side machine that does not answer a leave within the silence limit of the connection, or whose connection is lost before it answers, is taken to have withdrawn: the desk's last copy is emptied and nothing is handed out. Its answer, if it comes later, is used as usual, and does not stand for the answer to a later leave. Answers that were on their way when the central was switched off are dropped, and so are its own reads.
- **Leaving in the middle.** When focus leaves a side machine while a copy is on its way to it, the central stops sending the copy and does not count it as held. A side machine writes a copy only while it has focus.
- **Writing.** lanhop writes a copy to the clipboard of the machine with focus, unless the person copied something on that machine after focus arrived there, or after lanhop last wrote there if that is later. That copy is newer and stays, and moves when focus leaves. When focus arrives while that machine is still reading its clipboard, a copy made there after that read began counts as made after focus arrived, because lanhop can look at the clipboard only after the read ends.
- **Off the input path.** Reads and writes of the clipboard run apart from the loop that replays input and acknowledges frames, so a slow clipboard never holds up keys or the window.
- **Content.** Only plain UTF-8 text up to 1 MiB is carried. The central never sends a withdrawal, and a side machine ignores one.

## Consequences

- A copy reaches only the machines the person moves to after making it.
- A copy that was withdrawn is never handed out, even when focus moves straight from one side machine to another. A side machine that does not answer in time counts as a withdrawal, so a slow answer loses the last copy rather than spreading a stale one.
- A side machine on 0.1.0, or with its switch off, is never waited for.
- When a password manager empties the clipboard of the machine where the password was copied, and focus then leaves that machine, the password is no longer handed out. The machines that already got it keep it.
- The clipboard is read once per change that leaves a machine, not on every change.
- Crossing does not wait for a copy. Keys and the mouse move to the next machine at once, and the copy follows after the answer of the machine that focus left. If macOS asks the person before lanhop reads the clipboard, the question appears on the machine the person just left, and the answer waits until they go back. SC-001 of the spec sets how much later. A paste pressed in the same instant may still get the previous text.
- The central holds the last copy in memory, even when the copy goes from one side machine to another.
- A copy made by a program on a machine without focus moves only when focus next leaves that machine. A copy that arrives with focus replaces it.
- When the central restarts, the desk's last copy is lost. Each clipboard keeps what it holds.
- A read can run long: while macOS asks the person, while a file listing runs to its limit, or while the program that holds a copy on Linux is slow to hand it over. lanhop sees the clipboard of that machine again only when the read ends, so it cannot tell when a copy made there meanwhile was made. When focus comes back to that machine during the read, lanhop does not write over a copy made there during the read, even one made before focus came back. When focus leaves that machine again before the next read of it begins, that read is skipped, and the copy counts at the later leave whose read finds it, even when another machine copied something after it was made.

## Alternatives

- **Send every copy to every machine at once.** Simpler, and a copy is ready everywhere, but every password copied on one machine lands on all of them, and every change of the clipboard is read.
- **Hold the keys and the mouse of the machine that gets focus until its copy is there.** A paste could never overtake the copy, but the mouse would stall after each crossing while the machine that lost focus reads its clipboard and answers.
- **Take the mark of the clipboard at each move of focus, even while a read is under way.** lanhop could then tell before which move a copy was made, and a long read would change nothing. But it would touch the clipboard from two threads at once, and the pasteboard of macOS is not known to allow that.
- **Side machines connected to each other.** Each pair would need pairing and a connection. The desk is a star around the central, and copies follow the same paths as the keyboard and mouse.
