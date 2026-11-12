# lanhop

Share one keyboard and mouse across the computers on your desk, over the local network.

> **Status: early development.** Nothing here is usable day to day yet. See [Roadmap](#roadmap) for what works.

## Why another one

[Deskflow](https://github.com/deskflow/deskflow) and [lan-mouse](https://github.com/feschber/lan-mouse) already solve this, and they are good. If you need something that works today, use one of them.

lanhop explores a narrower design:

- **Kernel-level input on Linux.** It reads from `evdev` and writes to `uinput`, so the same code works on X11, on any Wayland compositor and on a text console. It does not depend on what the desktop environment exposes.
- **One static binary, no runtime dependencies.** The Linux build uses only the Go standard library.
- **Encrypted and authenticated by default.** A tool that carries keystrokes over the network must never have a plaintext mode.
- **Small enough to read.** The whole wire format is one 8-byte frame.

## How it works

The machine with the physical keyboard and mouse captures input events and sends them to the machine that currently has focus, which replays them through a virtual device.

Events travel as Linux input events (`type`, `code`, `value`). Linux machines use them as they are. Other systems translate at the edge.

| | Capture | Replay |
|---|---|---|
| Linux | `evdev`, with an exclusive grab while another machine has focus | `uinput` virtual device |
| macOS | `CGEventTap` (planned) | `CGEventPost` (planned) |

Design decisions are recorded in [docs/adr](docs/adr). Features are specified in [specs](specs) before they are built.

## Roadmap

- [x] Wire format for input events
- [x] Linux: virtual device (`uinput`) and device reader (`evdev`)
- [ ] Encrypted transport with device pairing
- [ ] Linux to Linux, end to end, switching with a hotkey
- [ ] macOS replay and capture
- [ ] Switching by pushing the cursor through a screen edge
- [ ] Automatic discovery on the local network
- [ ] Clipboard sharing
- [ ] Packages: systemd unit, udev rule, Homebrew formula

## Building

Requires Go 1.23 or newer.

```sh
go build ./cmd/lanhop
go test ./...
```

The Linux input tests need access to `/dev/uinput`, so they are skipped unless you run them with enough privileges:

```sh
sudo go test ./internal/evdev/
```

## Contributing

Bug reports, ideas and pull requests are welcome. Read [CONTRIBUTING.md](CONTRIBUTING.md) first.

Found a security problem? Do not open a public issue. See [SECURITY.md](SECURITY.md).

## License

[MIT](LICENSE)
