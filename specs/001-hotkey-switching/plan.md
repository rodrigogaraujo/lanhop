# Implementation Plan: the shared core and Linux

**Spec**: [spec.md](spec.md) · **Created**: 2026-11-11 · **Status**: Accepted

This is part 1 of 2. It builds everything the two systems share, plus the Linux side. [plan-macos.md](plan-macos.md) is part 2 and adds macOS. The first release needs both.

## Goal

Two or more paired machines share the keyboard and mouse of one of them, over an encrypted connection, switching focus with a hotkey.

## How it works

```
        SHARER                                         RECEIVER
physical keyboard, mouse
        │ capture (takes the input away from the local desktop while a receiver has focus)
        ▼
  hotkey detector ──► focus ──► TLS 1.3, both sides pinned ──► replay (releases held keys on leave or loss)
                                  8-byte frames
```

Capture and replay are the only parts that differ between systems. The sharer and the receiver talk to them through two small interfaces, so each system plugs in its own:

| | Capture | Replay |
|---|---|---|
| Linux (this plan) | `evdev`, exclusive grab | `uinput` virtual device |
| macOS ([plan-macos.md](plan-macos.md)) | `CGEventTap` | `CGEventPost` |

- The **receiver dials the sharer**. Only the sharer needs a known address and an open port (TCP 24745 by default).
- A Linux sharer always reads its keyboards to detect the hotkey. It grabs them, so the local desktop stops seeing them, only while a receiver has focus.
- A Linux receiver creates its virtual device when it starts, not when it gets focus, because desktops take a moment to accept a new device.

## Commands

| Command | Where | What it does |
|---|---|---|
| `lanhop pair` | sharer | Waits for one machine to pair, shows the codes, asks to accept |
| `lanhop pair <address>` | receiver | Connects to the sharer, shows the codes, asks to accept |
| `lanhop share` | sharer | Captures and forwards input |
| `lanhop receive` | receiver | Connects to its sharer and replays input |
| `lanhop peers` / `lanhop forget <name>` | both | Lists and removes trusted machines |
| `lanhop devices` | both | Lists the keyboards and mice found (exists today) |

## Decisions

| Decision | Choice | Why | Given up |
|---|---|---|---|
| Encryption | TLS 1.3 from the Go standard library, a self-signed Ed25519 certificate per machine, each side pins the other's public key | No cryptography of our own and no dependency | Certificates from an authority, which a desk does not have |
| Pairing | Each machine shows its own code and the code it received. A person compares them on both screens and accepts on both. The code is the first 80 bits of the SHA-256 of the public key, as `ABCD-EFGH-IJKL-MNOP` | Same model as SSH host keys. Safe against a machine in the middle without a password protocol | A 6-digit code, which would need a PAKE that the standard library lacks |
| Capture on Linux | Read without grabbing, grab only while a receiver has focus | A bug in lanhop cannot break local typing. If the process dies, the kernel drops the grab | The hotkey is also seen by the local application. Proxying all local input would hide it and is left for later |
| Switch moment | When the hotkey is released, not when it is pressed | No key is down at the moment of the grab, so nothing gets stuck | A few milliseconds |
| Control messages | Frames with type `0xFF00`, outside the kernel's range: `hello` (protocol version), `ping`, `enter`, `leave` | One frame format for everything | Variable-length messages |
| Liveness | Ping every second. 3 seconds of silence means the connection is lost | Meets the 3-second limit of the spec | A little idle traffic |
| Key repeat | Repeat events are not sent | A Linux desktop generates its own repeats, and a macOS receiver generates them from a timer | Nothing |
| Storage | `~/.config/lanhop/identity.key` (mode 0600) and `peers.json`, written by the commands | JSON is in the standard library | A format that is nicer to edit by hand |

The encryption and pairing decision will be recorded as ADR 0004 when its code lands.

## Tasks

Each task is one pull request with its tests. Tasks 1 to 3 and task 6 do not depend on each other.

- [ ] **1. Classify devices.** Query capabilities with `EVIOCGBIT` in `internal/evdev`, tell keyboards and relative mice from the rest, and skip lanhop's own virtual device. → Verify: test with virtual devices of each kind. `lanhop devices` prints the kind.
- [x] **2. Control frames and key state.** Add the control codes to `internal/event` and a tracker of held keys in `internal/keystate` that returns the releases needed to clear them. → Verify: unit tests.
- [x] **3. Identity and trust store.** `internal/identity` creates and loads the key and certificate and formats the code. `internal/trust` stores the peers. → Verify: unit tests, including file mode 0600 and a corrupted file.
- [ ] **4. Transport.** `internal/transport` listens and dials with TLS 1.3 and pinned keys, exchanges `hello` and keeps the connection alive with `ping`. → Verify: a paired peer connects. An unpaired peer is refused in both directions. A different protocol version is refused. A silent peer is dropped within 3 seconds.
- [ ] **5. Pairing.** `internal/pairing` and the `pair`, `peers` and `forget` commands. → Verify: pairing succeeds only when both sides accept. With a machine in the middle, the codes shown do not match.
- [x] **6. Hotkey detector.** `internal/hotkey`, a state machine with no I/O that turns key events into "focus machine N" when the hotkey is released. → Verify: table tests, including a hotkey pressed during a drag.
- [ ] **7. Sharer.** `internal/sharer` and `lanhop share`: the focus logic, written against a capture interface, and the Linux capture that reads the devices, watches `/dev/input` for new ones and grabs while a receiver has focus. → Verify: unit tests with a fake capture, and an integration test with a fake keyboard made with `uinput`.
- [ ] **8. Receiver.** `internal/receiver` and `lanhop receive`: dial with retry, replay through a replay interface, and release held keys on `leave` or on loss. → Verify: integration test that kills the connection with a key held.
- [ ] **9. End to end in CI.** One test runs sharer and receiver over TLS on localhost, with a fake keyboard and mouse, and checks the events that reach the receiver's virtual device. A benchmark measures the added delay. → Verify: success criteria SC-002 to SC-006 are asserted in CI.
- [ ] **10. First run.** Clear messages for missing permissions and a quick start in the README with the manual setup. → Verify: a person pairs two real machines following only the README (SC-001).

## Done when

- [ ] Every acceptance scenario of the spec passes between two real Linux machines, one on X11 and one on Wayland.
- [ ] CI runs the end-to-end test on every pull request.
- [ ] The README roadmap and the spec status are updated.

## Risks

- **Laptop as sharer.** A touchpad is not a relative mouse, so it is not shared in this version. The sharer needs a mouse.
- **Hotkey collisions.** `Ctrl+Alt+<number>` may be bound in some desktop or application. It is configurable.
- **Permissions.** Until spec 006 ships a udev rule, the user sets group permissions by hand or runs with `sudo`.
- **Pointer movement is relative in this part.** [Spec 002](../002-edge-switching/plan.md) adds absolute positions, so the replay interface of task 8 must leave room for them.
