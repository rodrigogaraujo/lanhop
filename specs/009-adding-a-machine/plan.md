# Implementation Plan: Adding a machine with one line

**Spec**: [spec.md](spec.md) · **Created**: 2026-11-13 · **Status**: Draft

## Goal

`lanhop add NAME` on the central prints a join line and waits. The line, typed on the new machine, installs lanhop if needed and joins the central with a code that carries the key of the central and a one-time token ([ADR 12](../../docs/adr/0012-joining-with-a-line-that-carries-the-key-of-the-central.md)). The central then shows the new machine with its hotkey and opens the layout editor.

## How a machine joins

```
central                                              new machine
───────                                              ───────────
lanhop add office-mac
  token = 40 random bits
  code  = first 80 bits of own key + token
  listen on 24746, print:
    curl …/v0.1.2/install.sh | LANHOP_VERSION=v0.1.2 LANHOP_JOIN="192.168.1.10 RF4K-…-5LDE" sh
    lanhop join 192.168.1.10 RF4K-…-5LDE
                                                     install lanhop v0.1.2 as a side machine
                                                     lanhop join 192.168.1.10 RF4K-…
                                          ◄───────── TLS 1.3, both keys; during the handshake:
                                                     does the key of the central match
                                                     the first 80 bits of the code?
  Central: "imac" ───────────────────────────────►   can "imac" be stored here?
                                          ◄───────── Join: token
  under the lock: token right, not spent,
  key and name free? spend it, store office-mac
  Welcome ───────────────────────────────────────►   store "imac" at 192.168.1.10:24745
  stop listening
  "office-mac joined from 192.168.1.14.
   Ctrl+Alt+3 moves to it."
  open the layout editor; office-mac waits in the tray
                                                     the installer starts the service
                                          ◄───────── hello on 24745
```

## The pieces

**`lanhop add NAME`** runs on the central, apart from `lanhop share`. It refuses a name that is invalid, taken, or the name of the central. It always listens on every interface, `:24746`; `--address` changes only the printed line. Binding that port keeps a second `lanhop add`, or a `lanhop pair` that waits on every interface, from running at the same time, and both commands say that another pairing is waiting when the port is taken. It serves each connection on its own, with a deadline of 10 seconds from accept and at most eight at once (ADR 12). It prints the address of each wrong token and of each connection closed for silence.

**After the join**, `lanhop add` prints the name, the address and the hotkey of the new machine, opens the editor, and ends. Whether the new machine is connected shows in `lanhop status`, since a new Mac may still wait for its permissions. `lanhop share` reads peers from disk at each use (`peersOnDisk` in `cmd/lanhop/share.go`), so the new machine can take focus at once, and `lanhop add` does not talk to `lanhop share`.

**The address in the line** is a private IPv4 address (10/8, 172.16/12, 192.168/16) of an interface that is up and is neither a tunnel nor a bridge for virtual machines or containers (`utun`, `tun`, `ppp`, `bridge`, `virbr`, `docker`, `br-`), the one with the default route first. When more than one is left, `lanhop add` prints them all with the line. `lanhop add --address` picks another.

**The version in the line** is the release of the central, so that `install.sh`, which installs the latest release unless `LANHOP_VERSION` says otherwise, installs the same one. A central that is not a release prints a line without it.

**`lanhop join ADDRESS CODE`** runs on the new machine. Before connecting, it looks in its own store, in the order of ADR 12: a machine already trusted whose key starts with the 80 bits of the code ends with status 0, so that the installer can be typed again (FR-010), and says that if the central no longer lists this machine, `lanhop forget` of the central here and the same line again start over; another central refuses; its own key refuses. Then it does the exchange of ADR 12 and stores the central. It starts nothing: under the installer, the installer starts the service next. Run by hand, it ends by saying that a running side machine service should connect within about 10 seconds (deduced from `RestartSec=10` and the throttle of launchd), or, when there is no such service, by printing the installer line. When the central is not waiting, the connection is refused, and `lanhop join` says to run `lanhop add` again. When it cannot reach the central at all, it says that a VPN can block the local network.

**The hotkey** of a side machine is its place in `lanhop peers` plus one, as today (`firstReceiverNumber` in `internal/sharer/sharer.go`). `lanhop add` prints it. A ninth side machine joins without a hotkey, and `lanhop add` says so.

**The layout editor** of spec 007 opens with the new machine in its tray, where spec 007 already puts machines that are not on the desk. `lanhop add` writes nothing in the layout file. Where no browser opens, it prints the address of the editor.

**`lanhop status` on the central** keeps the line of spec 008 first, which the installer reads, and lists after it each side machine: its name, its hotkey, and `connected`, `not connected since HH:MM` (with the date when it is not today), or `not connected since lanhop started`. `lanhop share` writes the list in its status file. Spec 010 adds the version of each machine to the same list.

## The installer

- `LANHOP_JOIN="ADDRESS CODE"` makes the role `receive`, answers yes to the questions of the installer without asking, skips the advice about `lanhop pair`, and runs `lanhop join` after installing and before it writes and starts the service. Unlike `LANHOP_YES=1`, it still opens the terminal, so that the installer waits for Accessibility and Local Network as spec 008 says. The installer says what it did at each step.
- When the join fails, refused or unreachable, the installer stops before it writes or starts the service, ends with an error, and says to run `lanhop add` again on the central.
- Typing the line again is safe: the steps that are done are done again or skipped, as today, and `lanhop join` ends with status 0 when the central is already trusted.
- When the install folder is not on the `PATH`, the installer asks, or goes ahead at once under `LANHOP_YES=1` or `LANHOP_JOIN`, adds one line to a file the shell reads in a new terminal, and says which file:

| Shell | File |
|---|---|
| zsh, macOS and Linux | `~/.zshrc` |
| bash on Linux | `~/.bashrc` |
| bash on macOS | The first that exists of `~/.bash_profile`, `~/.bash_login` and `~/.profile`, or a new `~/.bash_profile` |
| Any other | None: the installer prints the line to add, as today |

- The installer that the join line names is `install.sh` at the tag of the release of the central, as spec 010 does.

## Wire

The join uses the pairing port, 24746. Each message is a length byte, as in `internal/pairing/message.go`, then a tag byte from 0x01 to 0x04. No name can start with those bytes (`trust.ValidName`), so a join that reaches a `lanhop pair`, or the other way round, fails at the first message:

| Message | Tag | From | Content after the tag |
|---|---|---|---|
| Central | 0x01 | Central, right after TLS | Its name |
| Join | 0x02 | New machine | The 5 bytes of the token |
| Welcome | 0x03 | Central | Nothing |
| Refused | 0x04 | Central | One byte: 1 the token is wrong, 2 the key is the central's own, 3 the key is already trusted, 4 the name is taken, 5 the central could not store the machine |

The messages of `lanhop pair` stay as they are.

## Decisions

| Decision | Choice | Why | Given up |
|---|---|---|---|
| How the two machines learn each other | A join code with 80 bits of the key of the central and a 40-bit token. [ADR 12](../../docs/adr/0012-joining-with-a-line-that-carries-the-key-of-the-central.md) | One line in one direction, no cryptography of our own, and the token never reaches a machine in the middle | A code of 24 characters to type when it cannot be copied |
| Who listens for joins | `lanhop add`, only while it waits | Nothing listens for joins the rest of the time, and `lanhop share` does not change | A terminal on the central for the length of the join |
| Who names the machine | The central, in `lanhop add NAME` | The person names it once, in the place where the names are used | The name of the host is no longer proposed for side machines |
| Placing the machine | The tray of the layout editor | `lanhop add` does not rewrite the layout file, and edge switching does not turn on by itself | One drag for the person |
| Role of a machine that joins | Side machine, without asking | The question about the keyboard made a laptop a central | A machine cannot become a central by joining |
| `PATH` | One line in a file the shell reads, after asking, or at once with a join line | A new terminal finds `lanhop` | A file of the person is changed |

## Tasks

One task, one pull request. Each verify list says what must be true before the task counts as done.

- [ ] **0. Measure what the join relies on.** → Verify on the desk that, on a Mac central with the firewall of macOS on and `lanhop share` running as its service, `lanhop add` run from a terminal through the link to lanhop.app takes a connection on 24746 from another machine without a new question of macOS. The result goes in this plan.
- [x] **1. The join in `internal/pairing`.** Code, token, messages, lock, attempts and deadlines, as in ADR 12 and "Wire". → Verify with tests over TLS on loopback:
  - A good code joins, both stores hold the other machine with the right names, and the new machine stores the central at port 24745.
  - A code with another key prefix: the handshake fails and the new machine sends nothing more.
  - A name that the new machine cannot store: it refuses before sending the token, and the token stays good.
  - A wrong token counts one attempt; the third ends the wait and its address is reported.
  - The own key of the central, a key already trusted, and a name taken during the wait are refused.
  - A plain TCP connection, a TLS handshake that stops, garbage, and a silent connection each count no attempt, and a good join that arrives during a silent connection still joins at once.
  - Nine connections at once: the ninth is refused, and the wait goes on.
  - Two right tokens at the same time: exactly one joins.
  - A `lanhop pair` against a waiting `lanhop add` fails at the first message, and the wait goes on. A join against a waiting `lanhop pair` fails at the first message, and `lanhop pair` ends, as it does on any stray connection today (FR-012).
  - After a join, and when the wait ends (shortened in the test), the port takes no more connections.
- [ ] **2. `lanhop add` and `lanhop join`.** The commands, the address, the version, the refusals, and the wait for the connection. → Verify with tests:
  - `lanhop add` refuses a taken name, the name of the central, an invalid name, and a second `lanhop add`; `lanhop add` and `lanhop pair` each say that another pairing waits when the port is taken.
  - A machine stored by `lanhop add` takes focus in a running `lanhop share` with no restart, in an integration test.
  - `lanhop add` prints the name, address and hotkey of the new machine right after the join.
  - With nothing listening: `lanhop join` on a machine that already trusts the central ends with status 0, changes nothing, and prints how to start over; on a machine that trusts another central, or with its own key in the code, it refuses; otherwise it says to run `lanhop add` again. With an address that does not answer, it mentions a VPN.
  - The printed lines carry the address, the code, the version, and the installer at the tag of the release; the address skips tunnels, bridges and public addresses, and several candidates are all printed.
- [ ] **3. The hotkey and the editor.** → Verify with tests: the hotkey printed is the one `lanhop share` uses; a ninth machine is told it has no hotkey; the editor opens with the new machine in the tray, and the layout file is not written.
- [ ] **4. `lanhop status` lists the desk.** → Verify with tests on the status file, after connections and losses in an integration test: each side machine with its hotkey, and `connected`, `not connected since HH:MM`, with the date on another day, or `not connected since lanhop started`.
- [ ] **5. The installer.** `LANHOP_JOIN`, the role without asking, going on from where it stopped, and the `PATH` line. → Verify in `install_test.go`:
  - With `LANHOP_JOIN`, no question is asked, each step is said, and `lanhop join` runs before the service is written.
  - On macOS with `LANHOP_JOIN`, the installer waits for Accessibility and Local Network until it sees `connected`, as spec 008 does.
  - A join that is refused, or a central that does not answer, ends the installer with an error before any service is written, with the advice to run `lanhop add` again.
  - Stopped after the join and typed again, it runs to the end, and the join ends with status 0.
  - The `PATH` line goes in the file of the table for zsh, bash on Linux and bash on macOS, with an existing `~/.profile` and without, only once, only after a yes or with `LANHOP_YES=1` or `LANHOP_JOIN`, and the installer names the file; other shells get the printed advice.
- [ ] **6. Documents, and the test on a real desk.** README and the setup guides lead with `lanhop add`, and keep `lanhop pair` as the other way. → Verify, with the owner present: a Mac that never had lanhop joins the central with one join line; SC-001 and SC-002 are timed and counted; and each step of the list in "Why" of the spec is checked off for SC-003.
