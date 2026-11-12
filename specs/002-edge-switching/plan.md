# Implementation Plan: Switching through the screen edges

**Spec**: [spec.md](spec.md) · **Created**: 2026-11-11 · **Status**: Draft

## Goal

The cursor crosses from one machine to its neighbour through the screen edge, following a layout written in a text file.

## The layout file

One file, the same format on every system. It draws the desk: each line is a row of machines, from left to right. A dot holds an empty place.

```
# my desk
laptop   desktop   macbook
.        server    .
```

Here `desktop` has `laptop` on its left, `macbook` on its right and `server` below.

| System | Path |
|---|---|
| Linux | `~/.config/lanhop/layout` |
| macOS | `~/Library/Application Support/lanhop/layout` |
| Windows (when supported) | `%AppData%\lanhop\layout` |

The names are the ones given at pairing. The machine that reads the file finds itself by its own name.

## How a crossing works

```
on the sharer's own screen                 on a receiver's screen
cursor position comes from the system      the sharer owns the cursor: it keeps the position
        │                                  and tells the receiver where to put it
        ▼                                          │
   edge watcher ◄──────────────────────────────────┘
        │  pushed long enough, no button held, not in a corner
        ▼
   layout ──► neighbour ──► focus change (spec 001) ──► cursor placed on the opposite edge
```

- **On a receiver, the sharer owns the cursor.** It sends absolute positions, from 0 to 65535 on each axis, and the receiver maps them to its own desktop. The sharer always knows where the cursor is, so it sees the edges of any receiver without asking, and there is no drift.
- **On the sharer's own screen**, the position comes from the system. This is direct on macOS. Linux is the open point below.

## Decisions

| Decision | Choice | Why | Given up |
|---|---|---|---|
| Layout format | A text grid, one row per line | It looks like the desk, and it needs no parser library | Free positions and per-monitor placement |
| Who knows the layout | Only the machine acting as sharer uses it | Receivers need no layout to work | A machine that may share needs its own copy |
| Cursor on a receiver | Absolute positions sent by the sharer | The sharer sees every edge, on every system, with no drift | The receiver's own pointer acceleration. The sharer applies one speed setting |
| Replay of absolute positions | Linux: a second virtual device with absolute axes. macOS: the position of the posted event | Both map a range to the whole desktop, so the sharer needs no pixel sizes | Applications that lock the cursor and read only relative movement |
| Crossing rule | Push for 150 ms, never with a button held, never within 32 pixels of a corner. All configurable | Stops accidental crossings | Instant crossing by default |
| Wire format | Absolute movement uses the kernel's `EV_ABS` event type in the same 8-byte frame | No new frame format | Nothing |

### Open point: the cursor position on a Linux sharer

The kernel does not know where the cursor is (see ADR 1). There are three ways to find it. Task 1 tries them and picks one.

| Way | Works on | Cost |
|---|---|---|
| Ask the X server | X11 only | Small. A few requests on the X socket, in pure Go |
| Input capture portal | GNOME 45+, KDE 6.1+ on Wayland | Large. D-Bus and the `libei` protocol, and it varies by desktop |
| lanhop moves the local cursor itself, through an absolute virtual device | X11, every Wayland compositor and the console | Medium. All local mouse input passes through lanhop, which applies its own speed |

Until one of them works on a session, FR-013 applies: lanhop says that edge switching is off on that machine as a sharer, and the hotkey still works. A Linux machine as a receiver is not affected.

## Tasks

Each task is one pull request with its tests. They start after tasks 7 and 8 of the [core plan](../001-hotkey-switching/plan.md). Tasks 2 and 3 have no such dependency.

- [ ] **1. Research: cursor position on a Linux sharer.** Build a throwaway prototype of each of the three ways and measure them on X11 and on two Wayland compositors. → Verify: an ADR records the choice with the measurements.
- [ ] **2. Layout parser.** `internal/layout` reads the grid and answers "who is on this side of this machine". Errors carry the line and the cause. → Verify: table tests for valid grids, unknown names, repeated names, empty file and a machine missing from the grid.
- [ ] **3. Edge watcher.** `internal/edge`, a state machine with no I/O: positions and button states in, crossings out, with the push time and the corner rule. → Verify: table tests for each scenario of story 3.
- [ ] **4. Absolute pointer on receivers.** Add absolute movement to the replay interface: a second `uinput` device on Linux, event position on macOS. → Verify: the Linux loopback test reads the absolute events back. On a Mac, the cursor lands on the requested point.
- [ ] **5. Sharer owns the remote cursor.** While a receiver has focus, the sharer turns movement into absolute positions and feeds the edge watcher. → Verify: integration test crosses to a receiver and back 50 times and checks the arrival position (SC-003).
- [ ] **6. Cursor of the sharer's own screen.** The position source for macOS, and the one chosen in task 1 for Linux. → Verify: on each system, pushing the edge triggers a crossing and a quick touch does not.
- [ ] **7. Layout in use.** Load the file at start, reload it when it changes, keep the last good one, and add `lanhop layout` to print it with the neighbours. → Verify: edit the file while running and cross to the new neighbour. Break the file and see the error with its line.
- [ ] **8. Lock hotkey and settings.** The lock hotkey, and the push time, corner size and cursor speed in the settings. → Verify: unit tests for the settings, and the lock tested by hand.

## Done when

- [ ] Every acceptance scenario of the spec passes with a macOS sharer and with a Linux sharer on at least one desktop session.
- [ ] The README explains the layout file with an example.

## Risks

- **Linux as sharer** is the uncertain part, and task 1 exists to settle it before the other tasks depend on it.
- **Cursor speed on receivers.** The receiver's own acceleration no longer applies, so the feel may differ from a mouse plugged into it. One speed setting has to be enough.
- **Cursor lock.** Games and remote desktop windows that lock the cursor on a receiver will not get relative movement in this version.
