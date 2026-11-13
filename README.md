# lanhop

Share one keyboard and mouse across the computers on your desk, over the local network.

> **Status: v0.1.1.** On macOS, lanhop is now an app, `lanhop.app`, with an identifier of its own, so that macOS can apply the Local Network permission to it. The installer now checks lanhop as it runs at login: on a Mac it opens the pane of each permission that is missing, Accessibility and Local Network, and it ends when the machine is connected or says what is still missing. The `lanhop.app`, `lanhop status` and the installer of this version have been checked only by automated tests, and no person has run them on a real machine yet. The automated tests do not run the macOS build for x86-64. Tried on real machines so far: a Mac shared its keyboard and trackpad with a Linux machine, by hotkey and through the screen edge. On 2026-11-13, an x86-64 machine running macOS ran the 0.1.0 build as the machine with the keyboard, and three Macs and a Linux machine each connected to it. The three Macs ran the 0.1.0 program inside a `lanhop.app` made by hand, started at login. Not tried by a person yet: typing into a Mac controlled from another machine, a Linux machine sharing its keyboard and mouse, and switching across three or more machines. On `main`, not in a release yet: [adding a machine with one line](#adding-a-machine) and [`lanhop update`](#updating). They have passed only automated tests, and `lanhop update` refuses every download until the release keys of the project exist. Expect rough edges, and please [report](https://github.com/rodrigogaraujo/lanhop/issues) what you find. See [Roadmap](#roadmap) for what is built.

## Why another one

[Deskflow](https://github.com/deskflow/deskflow) and [lan-mouse](https://github.com/feschber/lan-mouse) already solve this, and they are good. If you need something that works today, use one of them.

lanhop explores a narrower design:

- **Kernel-level input on Linux.** It reads from `evdev` and writes to `uinput`, so the same code works on X11, on any Wayland compositor and on a text console. It does not depend on what the desktop environment exposes.
- **One binary, no runtime dependencies.** The Linux build is static and uses only the Go standard library.
- **Encrypted and authenticated by default.** A tool that carries keystrokes over the network must never have a plaintext mode.
- **Small enough to read.** The whole wire format is one 8-byte frame.

## How it works

The machine with the physical keyboard and mouse captures input events and sends them to the machine that currently has focus, which replays them through a virtual device.

Events travel as Linux input events (`type`, `code`, `value`). Linux machines use them as they are. Other systems translate at the edge.

| | Capture | Replay |
|---|---|---|
| Linux | `evdev`, with an exclusive grab while another machine has focus | `uinput` virtual device |
| macOS | `CGEventTap` | `CGEventPost` |

Design decisions are recorded in [docs/adr](docs/adr). Features are specified in [specs](specs) before they are built.

## Install

On each machine, Linux or macOS, as your own user:

```sh
curl -fsSL https://raw.githubusercontent.com/rodrigogaraujo/lanhop/main/install.sh | sh
```

The installer asks before each step:

1. It downloads the build for the machine (Linux or macOS, x86-64 or arm64) from the latest [release](https://github.com/rodrigogaraujo/lanhop/releases), checks it against the `SHA256SUMS` of that release and puts `lanhop` in `~/.local/bin`. On macOS it puts `lanhop.app` in Applications and makes `~/.local/bin/lanhop` a link to the program inside it. This needs no root.
2. It asks whether the keyboard and mouse are plugged into this machine. That machine shares them, and the others receive.
3. On Linux, it shows the commands of the [permission steps](docs/setup-linux.md), which need `sudo`, and asks before running them. Then it writes a systemd user unit and offers to enable it, so that lanhop starts when you log in.
4. On macOS, it offers a launch agent that starts lanhop when you log in. On the Mac with the keyboard, when its firewall is on, it shows the commands that let lanhop accept connections, which need `sudo`, and asks before running them.
5. It offers to pair the machine, and to arrange the desk on the machine with the keyboard.
6. It checks lanhop as it runs at login. On macOS it opens the pane of each permission that is missing, [Accessibility and Local Network](docs/setup-macos.md), and says what to switch on. It ends when the machine is connected, or says what is still missing.

`lanhop status` says the same at any time: whether lanhop is waiting for a permission, trying to connect, connected, or why it stopped.

Without a terminal to ask on, it installs lanhop and changes nothing else. To read the script before running it, download [install.sh](install.sh) and run `sh install.sh`.

| Variable | What it does |
|---|---|
| `LANHOP_VERSION=v0.1.1` | Installs that release instead of the latest one |
| `LANHOP_INSTALL_DIR=DIR` | Puts the binary in `DIR` instead of `~/.local/bin`. On macOS, `DIR` gets the `lanhop` command, and the app still goes to Applications |
| `LANHOP_ROLE=share` or `receive` | Answers the question about the keyboard and mouse |
| `LANHOP_YES=1` | Agrees to every step and asks nothing, for installs with nobody at the keyboard. Pairing still needs a person, and the check at the end prints what `lanhop status` says without waiting for a permission or a connection |
| `LANHOP_JOIN="ADDRESS CODE"` | From the next release. Joins the machine with the keyboard as a side machine, with the line that [`lanhop add`](#adding-a-machine) printed there. It agrees to every step, and still waits for the permissions of macOS |

A variable goes before `sh`, not before `curl`:

```sh
curl -fsSL https://raw.githubusercontent.com/rodrigogaraujo/lanhop/main/install.sh | LANHOP_ROLE=receive sh
```

To remove lanhop:

```sh
curl -fsSL https://raw.githubusercontent.com/rodrigogaraujo/lanhop/main/install.sh | sh -s -- --uninstall
```

It stops lanhop and removes the service, the binary, `lanhop.app` on macOS and, after asking, the udev rule that the installer wrote. It keeps the key of the machine and its trusted machines, and says where they are. If you installed with `LANHOP_INSTALL_DIR`, give it again.

## Adding a machine

> From the next release. This has passed only automated tests.

On the machine with the keyboard, name the new machine:

```sh
lanhop add office-mac
```

It prints a line and waits for ten minutes. Type that line on the new machine:

- On a machine without lanhop, the installer line with `LANHOP_JOIN` installs lanhop as a side machine, joins, and waits for the permissions of macOS until the machine is connected. It asks no other question, apart from the password that `sudo` asks on Linux for the permission steps.
- On a machine that has lanhop, the short line is `lanhop join ADDRESS CODE`.

When the new machine has joined, `lanhop add` prints its hotkey, such as `Ctrl+Alt+3 moves to it`, and opens the [layout editor](#arranging-the-desk-by-dragging) with the new machine waiting in its tray.

- The code in the line carries the key of the machine with the keyboard and a token that works once, for ten minutes ([ADR 12](docs/adr/0012-joining-with-a-line-that-carries-the-key-of-the-central.md)). Whoever reads the line in those ten minutes can join in place of the new machine, so pass it on yourself, by typing it or through a chat you trust.
- Three wrong codes end the wait. `lanhop add` prints the address of each connection that did not join.
- `lanhop status` on the machine with the keyboard lists each side machine with its hotkey, and whether it is connected or since when it is not.
- `lanhop pair`, which compares two codes on both screens, still works.

## Updating

> From the next release. This has passed only automated tests, and it does not work yet: the release keys of the project do not exist yet, so `lanhop update` refuses every download until a release carries them.

```sh
lanhop update
```

- On the machine with the keyboard, it moves lanhop to the latest release and restarts its service. Then it waits up to 90 seconds and prints each side machine that comes back on the new release, and names the ones that did not, the ones that need the installer line once, and the ones that do not follow.
- A side machine that runs as its service moves to the version of the machine with the keyboard by itself, when it connects. It restarts only when it has not been used for 6 seconds, so the keyboard and mouse never stop on the machine you are using.
- A release is installed only when its checksums carry the signature of the project, and only if it is newer than the one installed ([ADR 11](docs/adr/0011-signed-releases-and-side-machines-that-follow-the-central.md)).
- `lanhop update --version v0.1.3` moves to that release. With `--older`, it moves back, and the machine stops following the machine with the keyboard.
- `lanhop updates off` keeps a side machine on its version. `lanhop updates on` lets it follow again.
- `lanhop status` on the machine with the keyboard shows the version of each side machine, such as `connected, v0.1.3, follows`.
- Machines on 0.1.1 or older do not update by themselves. Type the installer line on each once.

Each release also has the archives themselves, one per system and architecture, with `SHA256SUMS`. The macOS archives hold `lanhop.app`, which is not signed with an Apple developer certificate: [Setting up lanhop on macOS](docs/setup-macos.md#the-macos-app-is-not-signed-by-apple) says what that means when you download one with a browser.

## Roadmap

- [x] Wire format for input events
- [x] Linux: virtual device (`uinput`) and device reader (`evdev`)
- [x] First release: Linux and macOS in any combination, encrypted with device pairing
  - [x] Switching with a hotkey ([spec 001](specs/001-hotkey-switching/spec.md))
    - [ ] A setting for the hotkey, and one to swap Alt and Super between Linux and macOS
  - [ ] Switching through the screen edges, with a layout file ([spec 002](specs/002-edge-switching/spec.md))
    - [x] The layout file, and crossing between the machines that are being controlled
    - [x] The edges of the own screen of a Mac that has the keyboard
    - [ ] The edges of the own screen of a Linux machine that has the keyboard
    - [ ] A hotkey that locks focus, and settings for the push time, the corner size and the cursor speed
- [ ] Windows
- [ ] Automatic discovery on the local network
- [ ] Clipboard sharing ([spec 005](specs/005-clipboard-sharing/spec.md)): the messages that carry a copy are built; lanhop does not use them yet, so nothing is copied between machines
- [ ] Packages ([spec 006](specs/006-packages/spec.md))
  - [ ] Release builds and an installer with the systemd unit, the udev rule and the launch agent: built; the installer of 0.1.0 ran on Macs and did not get them connected, which spec 008 is meant to fix
  - [ ] lanhop as an app on macOS, and an installer that checks every permission ([spec 008](specs/008-permissions-checked/spec.md)): built; the 0.1.0 program in a `lanhop.app` made by hand connected at login on three Macs; the app of a release and the installer have not run on a real Mac yet
  - [ ] Homebrew formula
- [ ] Adding a machine with one line ([spec 009](specs/009-adding-a-machine/spec.md)): built on `main`, with `lanhop add`, `lanhop join` and the installer line; passed only automated tests
- [ ] Updating without work ([spec 010](specs/010-updates/spec.md)): `lanhop update` and side machines that follow the machine with the keyboard are built on `main`; the release keys, the signing of releases and the signature of the macOS app with a certificate of the project are not done yet, so no update can be installed

## Layout

To switch by pushing the cursor through an edge of the screen, draw your desk in a text file named `layout`. Each line is a row of machines, from left to right. A dot holds an empty place.

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

- The names are the ones `lanhop peers` lists, plus the name of the machine itself. `lanhop layout` prints that name, where the file goes and the desk as lanhop understood it.
- Only the machine with the keyboard reads the file, and only that machine needs it.
- The name of the machine with the keyboard always means that machine. A paired machine with the same name cannot be placed, and its edges do not cross: run `lanhop forget` on both machines and pair them again, with `lanhop pair --as NAME` on the machine with the keyboard.
- `lanhop share` picks up the file when you save it, with no restart. When the file has a problem, lanhop prints it with its line and keeps the layout it had.
- The cursor crosses after it pushes against an edge for a moment. Short pushes one after the other add up, so a few swipes on a trackpad cross too. It does not cross while a mouse button is held, nor in the corners of the screen. It does not leave the machine with the keyboard while a key is held there either, so that no key stays held on it.
- Without the file, the hotkeys work as before.
- While lanhop controls another machine with a layout, it places the cursor of that machine itself, at one speed that assumes a screen of 1920 by 1080. A setting for the speed is planned.

**A Linux machine with the keyboard does not cross the edges of its own screen yet**, because lanhop cannot find where its cursor is. Leave it with a hotkey. From there on the edges work: between the machines it controls, and back to it.

### Arranging the desk by dragging

Instead of writing the file, you can drag the machines to their places:

```sh
lanhop layout --edit
```

- lanhop prints an address and opens it in the browser of this machine. The page shows one box for this machine and one for each machine `lanhop peers` lists.
- Drag each box to its place on the desk, or back to "Not on the desk". With the keyboard alone, press Enter or Space on a box to pick it up, and again on a free place to put it down.
- The page says the neighbours of each machine as you move them. Save writes the layout file, and a running `lanhop share` uses it at once. Comments in the file are not kept.
- Press Done on the page, or Ctrl+C in the terminal, to stop.
- The page is served to this machine alone, at an address with a secret that changes on every run, and it works with no internet connection.
- On a machine with no browser, write the file by hand.

## Permissions

lanhop reads and creates keyboard and mouse input, and every system asks you to allow that first:

- **macOS:** the Accessibility permission, and Local Network on a Mac that is controlled from another machine. See [Setting up lanhop on macOS](docs/setup-macos.md).
- **Linux:** access to the input devices and to `uinput`. See [Setting up lanhop on Linux](docs/setup-linux.md).

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
