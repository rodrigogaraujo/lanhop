# Implementation Plan: macOS

**Spec**: [spec.md](spec.md) · **Created**: 2026-11-11 · **Status**: Accepted

This is part 2 of 2. [plan.md](plan.md) builds the shared core and the Linux side. This part plugs macOS into the same capture and replay interfaces.

## Goal

A Mac can be the sharer or a receiver, with Linux machines or with other Macs.

## How it works

- **Replay** creates events with CoreGraphics and posts them with `CGEventPost`.
- **Capture** installs an active `CGEventTap`. While a receiver has focus, the tap swallows the events so the Mac does not see them, and the cursor is detached from the mouse so it stays still.
- Both need the **Accessibility** permission, which the user grants once in System Settings.
- CoreGraphics is reached through cgo, so the macOS binary is built on a Mac.

## What macOS does differently

| Topic | On macOS | What lanhop does |
|---|---|---|
| Key codes | Its own virtual key codes | Translates to and from Linux key codes with a table, by physical key |
| Modifiers | Arrive as "flags changed" events, and every key event carries the flags | Turns flag changes into key events when capturing, and sets the flags when replaying |
| Mouse movement | Events carry an absolute position | Keeps its own cursor position, adds each movement and clamps it to the displays |
| Drag | Movement with a button held is a different event type | Sends "dragged" events while a button is held |
| Double click | The event must say it is the second click | Counts clicks by time and distance and sets the click count |
| Key repeat | Posted keys do not repeat by themselves | Repeats held keys from a timer, using the system's delay and rate |
| Tap timeouts | macOS disables a tap that is slow to answer | Re-enables the tap when macOS reports it |

## Tasks

Each task is one pull request with its tests. Task 1 does not depend on plan.md and can start at any time.

- [x] **1. Key translation.** `internal/keymap`, pure Go: Linux key code to and from macOS virtual key code, with the option to swap Alt and Super. → Verify: table tests that run on any system, including a round trip of every mapped key.
- [ ] **2. macOS replay.** `internal/macos` posts keys with flags, movement, drag, buttons with click count and scroll, and repeats held keys. → Verify: on a Mac with the permission granted, a test posts events and reads them back through a listen-only tap.
- [ ] **3. macOS capture.** An active tap that reports keys, flag changes, movement, buttons and scroll, swallows them while a receiver has focus and re-enables itself after a timeout. → Verify: same loopback test in the other direction, plus the hotkey detected from captured events.
- [ ] **4. Permission check.** Detect a missing Accessibility permission at start and name the System Settings pane. `lanhop devices` reports the permission state on macOS. → Verify: run without the permission and read the message.
- [ ] **5. Cross-system end to end.** A Mac sharer with a Linux receiver running in a container that has `uinput`, checked by reading the receiver's virtual device. CI builds and tests the macOS binary on a macOS runner. → Verify: the test passes on a Mac. CI is green on both systems.
- [ ] **6. Real machines.** The acceptance scenarios, run by hand in the four combinations of the spec. → Verify: SC-007.

## Done when

- [ ] SC-007 passes: Linux to Linux, Linux to macOS, macOS to Linux and macOS to macOS.
- [ ] The README has the macOS quick start, with the permission step.

## Risks

- **Tests need a person.** GitHub's macOS runners may not allow event taps, so tasks 2 and 3 are verified on a real Mac and CI covers the build and the pure Go parts.
- **Secure input.** While a password field is in front on a Mac sharer, macOS hides the keyboard from lanhop and the hotkey does not work. This is a system rule and is documented.
- **Login window.** A Mac receiver cannot be controlled at its login window or lock screen in this version.
- **Layouts.** The table maps physical keys of ANSI and ISO keyboards. Keys that exist only on one system (for example Fn) are not sent.
