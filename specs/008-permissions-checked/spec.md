# Feature Specification: An installer that asks for every permission and checks it

**Created**: 2026-11-13 · **Status**: Draft · **Depends on**: [spec 006](../006-packages/spec.md)

I run the installer on each machine, answer its questions and allow what it asks me to allow. When it ends, the machines are connected. If something is still missing, the installer says exactly what it is and where to fix it. I never have to find out from a log that a permission was missing.

This spec covers the permissions on macOS (Accessibility and Local Network), how lanhop is installed on a Mac so that macOS can keep those permissions, and the check at the end of the installer on both systems.

## Why

The first install on two Macs, on 2026-11-12, needed many manual steps and then did not work. lanhop was allowed under Accessibility and under Local Network, and still could not reach the other Mac when it was started at login: `connect: no route to host`. Started from a terminal, the same program connected.

What was measured on that Mac (macOS 26.5.2, arm64):

- The Local Network setting for the program was on.
- The system log said, on every attempt: `Local network allowed by preference for lanhop (a.out)`, then `Failed to find a.out using neagent` and `Populating the cache with 0 UUID(s) for a.out`. macOS could not tie the setting to the running program, so the setting never applied.
- The program of the release is signed ad hoc by the linker, with the identifier `a.out`, and has no `Info.plist`.

What Apple documents in [TN3179](https://developer.apple.com/documentation/technotes/tn3179-understanding-local-network-privacy):

- Programs started from Terminal or over SSH may use the local network without asking. That is why the same program connected from a terminal.
- A launch agent is subject to the Local Network permission. A daemon started by launchd is not.
- macOS tracks a program by its code signature. Ad hoc signatures make that tracking unreliable.
- macOS also uses the UUID of the main executable (`LC_UUID`), which must be there and must be unique.
- A launch agent that is not installed with `SMAppService` should name its app in `AssociatedBundleIdentifiers`.
- A process that ends right after a local network operation fails may never show the Local Network alert.

So, on a Mac, lanhop needs an identity of its own (an application bundle with its own identifier), and the installer has to check the permissions on the lanhop that the service starts, not on a terminal.

## Terms

- **Central**: the machine with the keyboard and mouse. It shares them.
- **Side machine**: a machine controlled by the central. It receives.
- **Service**: what starts lanhop at login. A launch agent on macOS, a systemd user unit on Linux.
- **The app**: `lanhop.app`, the application bundle that holds the macOS program.
- **Status**: what the running service says about itself: waiting for a permission, trying to connect, connected.

Sharer, receiver and pairing mean the same as in spec 001.

## User scenarios

### Story 1 - lanhop is an app on a Mac (Priority: P1)

On a Mac, lanhop is installed as `lanhop.app` in Applications, where the file chooser of the + button of System Settings starts, and it shows up as "lanhop" in every list of System Settings.

**Why this priority**: without an identity of its own, macOS does not apply the Local Network permission to lanhop started at login, and lanhop cannot reach the other machines at all.

**Independent test**: install on a Mac, then look at the signature of the installed program and at System Settings.

**Acceptance scenarios**:

1. **Given** a release, **When** I look at the macOS archive, **Then** it holds `lanhop.app`, signed with the identifier `com.github.rodrigogaraujo.lanhop`, with an `Info.plist` that says why lanhop uses the local network.
2. **Given** a Mac, **When** I install, **Then** `lanhop.app` goes to `/Applications`, or to `~/Applications` when `/Applications` cannot be written, and `lanhop` still works as a command in a terminal.
3. **Given** the service starts lanhop, **When** I open System Settings > Privacy & Security, **Then** Accessibility and Local Network list it as "lanhop".
4. **Given** lanhop 0.1.0 was installed as a bare program, **When** I install the new version, **Then** the service runs the app, the old program is removed, and the installer says that the old "lanhop" or "a.out" entries in System Settings can be removed.

### Story 2 - The installer asks for each permission and checks it (Priority: P1)

The installer asks for one permission at a time, opens the right pane of System Settings, and moves on only when the running service has that permission.

**Why this priority**: today the installer asks me to press Enter when I think the switch is on. It cannot see if the switch applies to the program that runs at login, and on 2026-11-12 it did not.

**Independent test**: install on a Mac where lanhop has no permission yet, and follow only what the installer says.

**Acceptance scenarios**:

1. **Given** the service runs without the Accessibility permission, **When** the installer checks, **Then** it says what to allow, opens the Accessibility pane, and waits until the service has the permission. It does not ask me to press Enter.
2. **Given** the service is waiting for a permission, **When** I give it, **Then** the service carries on by itself within a few seconds, and I restart nothing.
3. **Given** a side machine, **When** the service tries the central for the first time, **Then** the installer tells me that macOS is about to ask whether lanhop may use the local network, and that the answer is Allow.
4. **Given** a side machine where the service cannot reach the central, **When** this terminal can reach it, **Then** the installer says that macOS blocks lanhop from the local network, opens the Local Network pane and waits until the service connects.
5. **Given** a side machine where neither the service nor this terminal can reach the central, **When** the installer checks, **Then** it says that the central cannot be reached at that address, and what to check on the central.
6. **Given** a central on macOS with the firewall on, **When** the installer checks, **Then** it shows the commands that let the app accept connections and runs them only if I agree.

### Story 3 - The installer ends with the machines connected (Priority: P1)

The last thing the installer does is wait for the service to connect, and say so.

**Why this priority**: an install that ends without a connection leaves me to find out what is wrong.

**Independent test**: install a central and a side machine, and read the last lines of both installers.

**Acceptance scenarios**:

1. **Given** a side machine that is paired, **When** its service connects to the central, **Then** the installer says "Connected to NAME" and ends.
2. **Given** a central, **When** a side machine connects while the installer waits, **Then** the installer names it and ends. **When** none connects in that time, **Then** it says that the central is ready and that the side machines confirm the connection on their side.
3. **Given** a wait runs out, **When** the installer ends, **Then** it says what the service is still waiting for, and the command that shows it later.
4. **Given** a Linux machine, **When** the service starts, **Then** the installer checks its status and its connection in the same way.

### Story 4 - See what lanhop is waiting for (Priority: P2)

At any time, one command says what the service is doing.

**Why this priority**: it is what the installer uses, and it is what a person needs when lanhop stops working later.

**Independent test**: run the command with the service stopped, waiting for a permission, and connected.

**Acceptance scenarios**:

1. **Given** the service is running, **When** I run `lanhop status`, **Then** it says in one line what the service is doing: waiting for a permission, trying to reach a machine, or connected, with the name of the machine.
2. **Given** the service is not running, **When** I run `lanhop status`, **Then** it says so, and does not repeat an old status.

## Edge cases

- The Accessibility permission is given to the app, but macOS ties it to a different program than the one the service starts.
- The Local Network alert never appears.
- The user clicks "Don't Allow" in the Local Network alert.
- The firewall of a Mac blocks lanhop, or is set to block every incoming connection.
- The central is off, or its address changed.
- The service is stopped and started while the installer waits.
- The installer runs with nobody at the keyboard (`LANHOP_YES=1` or no terminal).
- `/Applications` exists but the user is not an administrator.
- An older lanhop is still allowed in System Settings under its old path or as "a.out".
- lanhop is updated from one build of the app to the next. With an ad hoc signature, each build is a different program for macOS, so a setting that is switched on may stop applying.

## Requirements

### Functional requirements

- **FR-001**: The macOS archive of each release MUST hold `lanhop.app`, with an `Info.plist` that has the identifier `com.github.rodrigogaraujo.lanhop`, the version of the release and a reason for using the local network, signed with that identifier.
- **FR-002**: On macOS the installer MUST put the app in `/Applications`, or in `~/Applications` when it cannot write `/Applications`, and MUST keep `lanhop` usable as a command.
- **FR-003**: The service MUST start the program inside the app, and MUST tell macOS that the app is responsible for it.
- **FR-004**: When a permission is missing, `lanhop share` and `lanhop receive` MUST wait for it and carry on when it is given, instead of ending.
- **FR-005**: The service MUST keep its current status where `lanhop status` can read it, and `lanhop status` MUST say when the service is not running.
- **FR-006**: The installer MUST check each permission against the running service, MUST open the pane of System Settings for each missing one, and MUST wait for it with a time limit.
- **FR-007**: When the service of a side machine cannot reach the central, the installer MUST try the same address from its own terminal, and MUST say whether macOS blocks lanhop or whether the central cannot be reached.
- **FR-008**: On a central with the macOS firewall on, the installer MUST show the commands that let the app accept connections, and MUST run them only if the user agrees.
- **FR-009**: The installer MUST end by waiting for a connection, and MUST say whether it connected and, if not, what is missing.
- **FR-010**: With nobody at the keyboard, the installer MUST NOT wait for permissions or connections. It reports the status it finds and ends.
- **FR-011**: The documentation for macOS MUST describe the Local Network permission and the app, next to Accessibility.

### Out of scope for this feature

- Signing with an Apple developer certificate and notarizing. They need a paid account.
- Asking which machine is the central with one question, and finding the central without typing its address. They are spec 004.
- Moving lanhop between machines after the install.
- Windows.

## Success criteria

- **SC-001**: On two Macs that never had lanhop, a person goes from the one line to two connected machines by following only what the two installers say.
- **SC-002**: When the install ends without a connection, the last line of the installer names the missing permission or the unreachable machine.
- **SC-003**: lanhop started at login on a Mac reaches the other machines after the Local Network permission is given once.

## Assumptions

- Every Mac has `codesign`, `launchctl`, `open` and `nc`. They are part of macOS.
- An ad hoc signature with a fixed identifier, in an application bundle, is enough for macOS to keep the Local Network permission. That held on one Mac on 2026-11-13 (task 0 of the plan).
- Accepting a connection from a side machine does not need the Local Network permission on the central. Only the side machine, which opens the connection, needs it.
