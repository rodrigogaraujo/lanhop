# Implementation Plan: An installer that asks for every permission and checks it

**Spec**: [spec.md](spec.md) · **Created**: 2026-11-13 · **Status**: Draft

## Goal

On a Mac, lanhop becomes `lanhop.app`, with an identifier of its own, so that macOS can tie the Accessibility and Local Network permissions to the program that the launch agent starts. The service writes down what it is waiting for. `lanhop status` reads that, and the installer uses it to check each permission and the connection before it ends.

## The app

```
lanhop.app/
  Contents/
    Info.plist        CFBundleIdentifier com.github.rodrigogaraujo.lanhop
                      CFBundleExecutable lanhop, CFBundlePackageType APPL
                      CFBundleShortVersionString / CFBundleVersion: the release
                      LSUIElement true, LSMinimumSystemVersion 11.0
                      NSLocalNetworkUsageDescription
    MacOS/lanhop      the same program as before
    _CodeSignature/   written by codesign
```

- `packaging/macos/Info.plist` holds the file, with `@VERSION@` where the version goes. A tag `v0.1.1` gives `0.1.1`. A pull request gives `0.0.0`, because these keys take only numbers and dots.
- The release workflow builds the program as before, puts it in the bundle and signs the bundle ad hoc: `codesign --force --sign - --identifier com.github.rodrigogaraujo.lanhop lanhop.app`. It then checks that `codesign --verify --strict` passes, that the identifier is that one, and that the `Info.plist` is sealed.
- The macOS archives hold `lanhop.app`, `LICENSE` and `README.md`. The Linux archives do not change.

## The service and `lanhop status`

`lanhop share` and `lanhop receive` keep a file named `status` in the configuration directory, next to the key. Each change replaces the whole file (written to a temporary name, then renamed), so a reader never sees half of it. It holds a state, the machines and the address it is about, and the last error. One process owns it: the first to write takes a lock on `status.lock` and keeps it until it ends. Another `lanhop share` or `lanhop receive` of the same user, such as one started by hand next to the service, writes nothing while that lock is taken.

| State | Who | Meaning |
|---|---|---|
| `accessibility` | both, macOS | waiting for the Accessibility permission |
| `connecting` | receive | trying the central for the first time |
| `connected` | receive | connected to the central |
| `not-reached` | receive | the last try could not open a connection to the central, the service keeps trying |
| `failing` | receive | the connection opened and then failed (TLS, pairing), the service keeps trying |
| `waiting` | share | ready, no side machine connected |
| `sharing` | share | one or more side machines connected |

`lanhop status` prints one line, which starts with a word and a colon:

- `stopped:` when there is no status file, or nobody holds its lock. When the service wrote `stopped` before it ended, its last error follows. The last try of a service that died is not repeated.
- the state of the file otherwise, with a sentence.
- for `not-reached`, it tries the same address itself, for 3 seconds, and prints `blocked:` when it gets through, or `unreachable:` when it does not. While macOS kept the service off the local network on 2026-11-12, its connections failed this way, with `no route to host`. A central that is off, or that runs no lanhop, fails this way too, and the try from the terminal fails as well. Run from a terminal, this try has the permissions of the terminal, which macOS lets use the local network. So `blocked` means that macOS blocks the service and not the network.

The installer reads only the first word. People read the sentence.

**Waiting for Accessibility.** When the permission is missing, the service writes `accessibility` and checks again every second, instead of ending. When `AXIsProcessTrusted` turns true during the wait but the checks for posting and listening stay false for 3 seconds, the process ends with an error and launchd starts a new one, which sees the permission. A process that was trusted from its first check keeps waiting, because a new one would see the same. Either way the person restarts nothing.

## The installer on a Mac

```
download ─► check ─► lanhop.app in /Applications (or ~/Applications)
                     ~/.local/bin/lanhop ─► link to the program in the app
        │
        ▼
role ─► launch agent (program in the app, AssociatedBundleIdentifiers) ─► start
        │
        ▼
pairing ─► layout (share only)
        │
        ▼
check, polling "lanhop status" every second:
  accessibility ─► say what to allow, open the Accessibility pane, wait
  connecting    ─► on a Mac that receives: say that macOS may ask about the local network, and that the answer is Allow
  blocked       ─► the same sentence about Allow; after 15 seconds of it on a Mac, say that macOS blocks lanhop,
                   open the Local Network pane, wait. On Linux, print the line once and wait
  unreachable   ─► print the line once (it says what to check on the central), keep waiting
  failing       ─► print the line once, keep waiting
  stopped       ─► wait: launchd and systemd start lanhop again after 10 seconds. After 25 seconds of it, print the line, end
  connected     ─► print the line, end
  share: firewall on ─► show the two socketfilterfw commands, run them with sudo if agreed
         firewall blocking every connection ─► say to switch that off, in Firewall > Options
         waiting ─► wait up to 60 seconds for a side machine
         sharing ─► name the side machines, end
```

- **`lanhop status` from a terminal**: it runs the program inside the app through the link, started by the terminal. macOS lets programs started from Terminal use the local network, which `blocked` relies on. Not measured yet: task 6.
- **Where the app goes**: `/Applications` when the user can write it (members of the `admin` group can), `~/Applications` otherwise. An app that is already installed is updated where it is, so that the launch agent and the Accessibility permission keep pointing at it. `LANHOP_SYSTEM_ROOT` stands for `/` in the tests, as it already does for the udev rule.
- **The command**: `LANHOP_INSTALL_DIR` (default `~/.local/bin`) gets a link named `lanhop` to the program inside the app. A file that is not a link, left by 0.1.0, is replaced by the link, and the installer says that old "lanhop" or "a.out" entries in System Settings can be removed.
- **Pane addresses**: `x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility` and `...?Privacy_LocalNetwork`.
- **Time limits**: 10 minutes for the whole check, counted on the clock, because a try from the terminal can take 3 seconds; 60 seconds for a side machine to reach the central. When the limit runs out, the installer prints the last status line and says that `lanhop status` shows it later. The check is the last thing the installer prints, so its last line is the status.
- **Nobody at the keyboard**: no pane is opened and nothing waits for a person. The installer waits up to 10 seconds for the service to start, prints `lanhop status` once, and also prints the Accessibility steps when that is what the service waits for.
- **An installed lanhop without `lanhop status`** (an older release through `LANHOP_VERSION`): the installer keeps the steps of 0.1.0, with the Accessibility pane and Enter. It finds out by looking for `lanhop status` in the program, without running it.
- **Older releases**: when the archive has no `lanhop.app` (0.1.0), the installer installs the bare program as before.

## The installer on Linux

The same check, without the panes: the installer waits for `connected`, `waiting` or `sharing`, and prints what `lanhop status` says when the limit runs out.

## Uninstall

It also removes the app (from `/Applications` or `~/Applications`, only the one that the installer would have written), the link, and the `status` and `status.lock` files. The key and the trusted machines stay.

## Decisions

| Decision | Choice | Why | Given up |
|---|---|---|---|
| Identity on macOS | An application bundle with its own identifier, signed ad hoc | macOS could not tie the Local Network setting to a bare program signed as `a.out` (measured). A bundle gives it an identifier and a sealed `Info.plist` | A single file. The archive holds a folder |
| Signing | Ad hoc, in the release workflow | A Developer ID needs a paid Apple account. Task 0 measures whether ad hoc is enough | Gatekeeper still blocks a download made with a browser, as in 0.1.0 |
| Where lanhop runs | A launch agent, as now | A launchd daemon as root would skip the Local Network permission, but it runs outside the session of the user, where the keyboard and mouse are | Nothing |
| Agent and app | `AssociatedBundleIdentifiers` in the launch agent | TN3179 asks for it for agents that are not installed with `SMAppService`, which needs an app with code of its own to register | Nothing |
| How the installer sees the service | A status file read by `lanhop status` | The installer must check the program that launchd starts. A check run from the terminal gets the permissions of the terminal | One more file in the configuration directory |
| Telling a blocked service from a dead network | `lanhop status` tries the same address from the terminal | It is how the cause was found on 2026-11-12. It needs no system log and no root | A false `blocked` when the central accepts this terminal but not the service, which does not happen in lanhop |
| Waiting for Accessibility | Check every second, end and be restarted when only a new process sees it | No restart loop in the log, and the person restarts nothing | Up to 10 seconds of delay when a new process is needed, the launchd throttle |
| Firewall of a central | Show and offer the `socketfilterfw` commands | The same pattern as the `sudo` steps on Linux | Nothing |

## Tasks

- [x] **0. Measure the identity on a real Mac.** The program of 0.1.0, unchanged, inside a hand-made `lanhop.app` with the `Info.plist` above, signed ad hoc with the identifier, started by the launch agent with `AssociatedBundleIdentifiers`. → Verify: after Accessibility and Local Network are given to the app, the log of the service says `Connected to`. Done on 2026-11-13 on a MacBook with macOS 26.5.2 (arm64): once the app was added under Accessibility, the service started by launchd connected to the central, and macOS keeps a Local Network setting for `com.github.rodrigogaraujo.lanhop`, switched on. Whether that setting came from the alert or was switched on by hand was not recorded. Not tried yet with the app: after a restart, on an Intel Mac, on a Mac central, and after an update to a new build. Reported later the same night from two more Macs (macOS 15.5 and 26.6.2, the second with Zscaler): the same hand-made app connected; the first try failed with `no route to host`, the Local Network alert came, and the next try connected. The Mac that shared on that desk is an Intel Mac running the 0.1.0 build for Intel, started by its launch agent, and the other machines connected to it.
- [x] **1. The app in the release.** `packaging/macos/Info.plist`, the bundle and its signature in `release.yml`, and the ADR. → Verify: on a pull request, both macOS jobs pass the `codesign` checks and the archives hold `lanhop.app`.
- [x] **2. Status.** The `status` file, `lanhop status`, and the wait for Accessibility. → Verify: unit tests for each state, for a dead process and for the `blocked` and `unreachable` cases against a local listener; the macOS job runs the tests that do not need a permission.
- [x] **3. Installer: the app.** The app in Applications, the link, the agent with `AssociatedBundleIdentifiers`, the move from 0.1.0, uninstall. The install step of `release.yml` on a Mac stops being only for tags and runs on every pull request, with `LANHOP_SYSTEM_ROOT` set to a temporary directory. → Verify: `install_test.go` with a fake app archive, with `/Applications` writable and not, with a 0.1.0 install in place, and with an archive without the app; on a pull request, the Mac install step puts the app where the installer says.
- [x] **4. Installer: the check.** The loop over `lanhop status`, the panes, the firewall of a central, Linux, and nobody at the keyboard. → Verify: `install_test.go` with a fake `lanhop` whose `status` walks through each sequence of states, and a fake `sleep`.
- [x] **5. Documentation.** `docs/setup-macos.md` gets Local Network and the app, and its steps for a browser download become `xattr -dr com.apple.quarantine lanhop.app`; the README gets `lanhop status`; the plan of spec 006 points here for the macOS archives. → Verify: the guide still describes every step by hand.
- [ ] **6. Real machines.** The installer on two Macs and on the Linux machine, as central and as side machines, then a restart, then an update to a newer build of the app. → Verify: SC-001 to SC-003 of the spec, after the restart and after the update.

## Done when

- [ ] Two Macs that never had lanhop end connected by following only what the installers say (SC-001).
- [ ] An install that ends without a connection names what is missing in its last line (SC-002).

## Risks

- **Ad hoc may stop being enough.** Apple says ad hoc signatures make tracking unreliable, and the Apple forums have reports of a Local Network setting that is on and does not apply. Task 0 worked on one Mac, once. Task 6 tries again after a restart and on more Macs.
- **An old entry looks like the right one.** In task 0, the Accessibility list showed a "lanhop" that was switched on: the entry of the 0.1.0 program, tied to its old path. It did not apply to the app. The installer therefore never trusts the list: it waits for the running service to report the permission, and it says that an older "lanhop" or "a.out" entry is a different program and can be removed with the − button.
- **`CGPreflightPostEventAccess` may not change inside a running process** after the permission is given. The service then ends and launchd starts it again, as the plan says.
- **An update may leave a switch on that no longer applies.** An ad hoc signature changes with every build, and macOS may keep the old setting switched on for a program that no longer exists. The installer waits for the service to report, so it notices, and it says to remove the entry and add the app again. Not measured yet: task 6.
- **The Local Network alert may not appear.** TN3179 lists cases where it does not. The installer then opens the pane and says what to switch on, which works whether or not the alert appeared.
- **Accepting connections on a Mac central without the Local Network permission** is assumed from the test of 2026-11-12, where a side machine started from a terminal connected to a central started by launchd. If a central needs it too, the check of the central waits for it in the same way.
