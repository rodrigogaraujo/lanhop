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
    curl … | LANHOP_JOIN="192.168.1.10 RF4K-…-C5LD" sh
    lanhop join 192.168.1.10 RF4K-…-C5LD
                                                     install lanhop as a side machine
                                                     lanhop join 192.168.1.10 RF4K-…
                                          ◄───────── TLS 1.3, both keys
                                                     the key of the central matches
                                                     the first 80 bits of the code?
                                          ◄───────── token
  token matches, not spent, in time:
  store office-mac ───────────────────────────────►  store the central: its name,
  send "office-mac", own name, imac.local            imac.local and 192.168.1.10
  stop listening                                     start the service, connect
                                          ◄───────── hello on 24745
  "office-mac joined from 192.168.1.14.
   Ctrl+Alt+3 moves to it."
  open the layout editor with office-mac placed
```

## The pieces

**`lanhop add NAME`** runs on the central, apart from `lanhop share`. It refuses a name that is invalid, taken, or the name of the central, and a second `lanhop add` while one waits (a lock file in the configuration directory). It listens on 24746 only while it waits. Peers are read from disk by `lanhop share` at each use (`peersOnDisk` in `cmd/lanhop/share.go`), so a machine stored by `lanhop add` can take focus at once, and `lanhop add` does not talk to `lanhop share`.

**The address in the line** is the IPv4 address of the interface that holds the default route of the central. `lanhop add --address` picks another, for a central with several networks.

**`lanhop join ADDRESS CODE`** runs on the new machine. It does the exchange of ADR 12, stores the central, and, when the installer is not running it, starts the service of a side machine. On a machine that already trusts that central, it says so and ends with status 0, so that the installer can be typed again (FR-010).

**Finding the central later.** The new machine stores the address it used and the local name the central sent. `lanhop receive` tries the local name first, then the address. Whether a Go build resolves `name.local` on each system is measured in task 0: on macOS through the system resolver, on Linux only where the build can reach `nss-mdns`. Where it cannot, the stored address is used, as today (story 4 is P3).

**Placing the machine.** When a layout file exists, `lanhop add` puts the new machine to the right of the rightmost machine on the row of the central, then opens the layout editor of spec 007 so the person can move it. Without a layout file, it opens the editor with the new machine next to the central. Where no browser opens, it prints the address of the editor.

**The hotkey** of a side machine is its place in `lanhop peers` plus one, as today (`firstReceiverNumber` in `internal/sharer/sharer.go`). `lanhop add` prints it. A ninth side machine joins without a hotkey, and `lanhop add` says so.

**`lanhop status` on the central** lists each side machine: its name, its hotkey, and `connected`, or `not connected since HH:MM`. `lanhop share` writes the list in its status file. Spec 010 adds the version of each machine to the same list.

## The installer

- `LANHOP_JOIN="ADDRESS CODE"` makes the role `receive` without asking, and runs `lanhop join` after installing and before it starts the service and checks the permissions (spec 008).
- Typing the line again goes on from where it stopped: every step already checks what is in place, and `lanhop join` ends with status 0 when the central is already trusted.
- When the install folder is not on the `PATH`, the installer adds one line to the profile of the shell of the person (`~/.zprofile` for zsh, `~/.bashrc` for bash on Linux, `~/.bash_profile` for bash on macOS, `~/.profile` otherwise), says which file it changed, and asks first unless `LANHOP_YES=1`.
- The installer that the join line names is `install.sh` at the tag of the release of the central, as spec 010 does.

## Wire

The join uses the pairing port, 24746, and the length-prefixed messages of `internal/pairing/message.go`:

| Message | From | Content |
|---|---|---|
| Join | New machine | A tag byte for join, then the 5 bytes of the token |
| Welcome | Central | The name given to the new machine, the name of the central, and its local name |
| Refused | Central | One byte: wrong or spent token, or the wait is over |

A first message that is not Join, or none within 10 seconds, closes the connection without counting as an attempt. The comparison of `lanhop pair` keeps its own messages, unchanged.

## Decisions

| Decision | Choice | Why | Given up |
|---|---|---|---|
| How the two machines learn each other | A join code with 80 bits of the key of the central and a 40-bit token. [ADR 12](../../docs/adr/0012-joining-with-a-line-that-carries-the-key-of-the-central.md) | One line in one direction, no cryptography of our own, and the token never reaches a machine in the middle | A code of 24 characters to type when it cannot be copied |
| Who listens for joins | `lanhop add`, only while it waits | Nothing listens for joins the rest of the time, and `lanhop share` does not change | A terminal on the central for the length of the join |
| Who names the machine | The central, in `lanhop add NAME` | The person names it once, in the place where the names are used | The name of the host is no longer proposed |
| Finding the central later | Its local name first, then the address used | An address that changes does not break the side machines, where local names resolve | Discovery without any address is left to spec 004 |
| Role of a machine that joins | Side machine, without asking | The question about the keyboard made a laptop a central | A machine cannot become a central by joining |
| `PATH` | One line in the profile of the shell, after asking | A new terminal finds `lanhop` | A file of the person is changed |

## Tasks

One task, one pull request. Each verify list says what must be true before the task counts as done.

- [ ] **0. Measure what the join relies on.** → Verify, on the desk:
  - On a Mac central with the firewall of macOS on and `lanhop share` running as its service, a program run from a terminal, signed as lanhop.app, takes a connection on 24746 from another machine without a new question of macOS.
  - A Go build as released resolves the local name of the central on macOS, and on the Linux server (Ubuntu, `nss-mdns` installed or not).
  - The results go in this plan, and story 4 is kept or moved to spec 004 by them.
- [ ] **1. The join in `internal/pairing`.** Code, token, exchange, attempts and timeouts, as in ADR 12. → Verify with tests over TLS on loopback:
  - A good code joins, and both stores hold the other machine with the right names.
  - A code with another key prefix: the new machine sends nothing and stops.
  - A wrong token counts one attempt; the third ends the wait; a spent token and an expired wait are refused.
  - A plain TCP connection, a TLS handshake that stops, garbage, and a silent connection each leave the wait open and count no attempt.
  - The token is compared in constant time.
- [ ] **2. `lanhop add` and `lanhop join`.** The commands, the lock, the address, the refusals, and the start of the service after a join. → Verify with tests:
  - `lanhop add` refuses a taken name, the name of the central, an invalid name, and a second `lanhop add`.
  - A machine stored by `lanhop add` takes focus in a running `lanhop share` with no restart, in an integration test.
  - `lanhop join` on a machine that already trusts the central ends with status 0 and changes nothing.
  - The printed lines carry the address, the code, and the installer at the tag of the release.
- [ ] **3. Placing the machine and the hotkey.** → Verify with tests: with a layout file, the new machine lands to the right of the rightmost machine of the row of the central, and the file still parses; without one, the editor opens with it next to the central; the hotkey printed is the one `lanhop share` uses; a ninth machine is told it has no hotkey.
- [ ] **4. `lanhop status` lists the desk.** → Verify with tests on the status file: each side machine with its hotkey, `connected` or `not connected since HH:MM`, after connections and losses in an integration test.
- [ ] **5. The installer.** `LANHOP_JOIN`, the role without asking, going on from where it stopped, and the `PATH` line. → Verify in `install_test.go`:
  - With `LANHOP_JOIN`, no question about the keyboard is asked, and `lanhop join` runs before the service starts.
  - Stopped after the join and typed again, it skips the join and goes on.
  - The `PATH` line goes in the right file for zsh, bash on Linux, bash on macOS and other shells, only once, and only after a yes or with `LANHOP_YES=1`.
- [ ] **6. Documents, and the test on a real desk.** README and the setup guides lead with `lanhop add`, and keep `lanhop pair` as the other way. → Verify, with the owner present: a Mac that never had lanhop joins the central with one join line, and SC-001 and SC-002 are timed and counted.
