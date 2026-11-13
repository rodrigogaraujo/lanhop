# Feature Specification: Updating without work

**Created**: 2026-11-13 · **Status**: Draft · **Depends on**: [spec 005](../005-clipboard-sharing/spec.md) (the Features frame), [spec 006](../006-packages/spec.md), [spec 008](../008-permissions-checked/spec.md)

I run one command on the machine that has the keyboard and mouse, and every machine of my desk ends up on the new version. On a Mac, macOS does not ask for its permissions again.

## Why

Today a new version means the installer line on each machine, one by one. On a Mac it also means the Accessibility permission again, every time, in the way that works only one way: remove the old line with −, add the app with +, and restart lanhop. The owner of the project put it like this: "it has to be dynamic and easy, this is too boring".

Two things cause the work:

- Nothing in lanhop knows how to update itself, or the other machines.
- The macOS app is signed ad hoc. macOS ties the Accessibility permission to the exact build, so each new build is a stranger to it. Three Macs of the desk showed this on 2026-11-13 ([ADR 10](../../docs/adr/0010-a-stable-signature-from-a-certificate-of-the-project.md)).

## Terms

- **Central**, **side machine** and **focus** mean the same as in specs 001 and 002.
- **Release**: a version published on the GitHub page of the project, with its archives and checksums.
- **Service**: lanhop as the installer sets it up to start with the session: the launch agent on macOS, the systemd user unit on Linux.
- **Stable signature**: a signature of the macOS app that stays the same from one release to the next, so macOS knows the new build as the same app.

## User scenarios

### Story 1 - One command updates the whole desk (Priority: P1)

I run `lanhop update` on the central. It updates the central, and each side machine updates itself to the same version within a minute.

**Why this priority**: it is what the owner asked for, and it turns five installer runs into one command.

**Independent test**: with a central and two side machines on the previous release, run `lanhop update` on the central, then run `lanhop status` on it and `lanhop version` on each machine.

**Acceptance scenarios**:

1. **Given** a desk on an older release, **When** I run `lanhop update` on the central, **Then** the central moves to the latest release, and each side machine moves to the same version, without anything typed on it.
2. **Given** `lanhop update` is running, **When** the side machines update, **Then** it prints each machine with its old and new version as each one comes back, and ends with a line that says which machines did not come back, if any.
3. **Given** a side machine is off during the update, **When** it starts later and connects, **Then** it updates to the version of the central then.
4. **Given** a side machine has focus when its update arrives, **When** the update is ready, **Then** it waits until focus leaves it, and until what it was handing over (a copy, a file) is done, before it restarts, so the keyboard and mouse never freeze on the machine I am using. A file copy that waits on that machine for a later paste is lost at the restart, as at any restart of lanhop.
5. **Given** I run `lanhop update` on a side machine, **When** it ends, **Then** that machine is on the latest release and restarts at once, and the others are not touched.
6. **Given** every machine is already on the latest release, **When** I run `lanhop update`, **Then** it says so and changes nothing.

### Story 2 - A Mac keeps its permissions after an update (Priority: P1)

After an update, a Mac goes on controlling and sharing without a new click in System Settings.

**Why this priority**: without it, the command of story 1 still leaves the − and + work on every Mac, and the desk is mostly Macs.

**Independent test**: on a Mac that has the Accessibility permission for lanhop, update to a newer build, and check with `lanhop status` that it reaches `connected` or `sharing` without any click.

**Acceptance scenarios**:

1. **Given** a Mac where lanhop has Accessibility and Local Network, **When** it updates to a newer release, **Then** lanhop keeps both, and no question of macOS appears.
2. **Given** a Mac on lanhop 0.1.1 or older, signed ad hoc, **When** it moves to the first release with the stable signature, **Then** macOS asks for Accessibility once more, and the installer says that this is the last time.
3. **Given** the firewall of macOS lets lanhop take connections, **When** lanhop updates, **Then** it still does.

### Story 3 - Updates I can trust and stop (Priority: P2)

**Independent test**: serve a release whose archive was changed after it was signed, and run `lanhop update` against it; switch updates off on one side machine and update the central.

**Acceptance scenarios**:

1. **Given** a download that does not match the signed checksums of the release, **When** lanhop checks it, **Then** nothing is replaced, the old version keeps running, and the reason is printed or written in the log.
2. **Given** the update stops for any reason (no network, a full disk, a failed check, a crash in the middle), **When** I look at the machine, **Then** it still runs the version it had, or the new one whole, never a mix.
3. **Given** someone on the way, such as a proxy that reads TLS, offers an older signed release as the latest one, **When** lanhop checks it, **Then** nothing is replaced.
4. **Given** I switched off following the central on a side machine, **When** the central updates, **Then** that machine stays on its version, and `lanhop status` on the central shows it with its version.
5. **Given** a side machine is on a newer version than the central, **When** they connect, **Then** the side machine is not moved back.
6. **Given** a side machine on 0.1.1 or older, which knows nothing of updates, **When** the central updates, **Then** it keeps working with keyboard and mouse, and `lanhop status` on the central says that it needs the installer line once.

## Edge cases

- A release that needs more than a new program: a new launch agent key, a new udev rule, a new group. The update stops before replacing anything and prints the installer line.
- lanhop was not installed by the installer, or runs from a folder the person cannot write.
- lanhop runs by hand in a terminal instead of as a service.
- A Mac whose disk cannot swap two folders in one step.
- A build that is not a release: `dev` from the source code, or `dev-<commit>` from a run of the workflow.
- Two `lanhop update` at the same time on one machine.
- The central updates while a side machine is in the middle of its own update.
- GitHub cannot be reached, or answers slowly.
- The machine sleeps in the middle of a download.
- A Mac with the app in `~/Applications` instead of `/Applications`.

## Requirements

### Functional requirements

- **FR-001**: `lanhop update` MUST move the machine it runs on to the latest release, or to the version given with `--version`, and restart its service. It MUST move to a lower version only when `--older` is typed with `--version` on that machine.
- **FR-002**: A side machine MUST move to the version of its central when it connects to a central on a newer release, unless following the central is switched off on it.
- **FR-003**: A side machine MUST NOT move to a version older than its own because of the central.
- **FR-004**: lanhop MUST download only from the releases of the project on GitHub, following the redirects of GitHub, and MUST replace nothing unless the download matches the checksums of that release, the checksums carry a valid signature of the project, and the signed files name the version that was asked for.
- **FR-005**: On macOS, the new app MUST meet the code requirement built from the identifier and the certificate hashes compiled into lanhop, or nothing is replaced.
- **FR-006**: Replacing MUST be whole or nothing: a failure before the program is replaced leaves the old version untouched, and a crash at any moment leaves either the old program or the new one, whole.
- **FR-007**: A side machine that updates for the central MUST NOT restart while it has focus, before it has answered the last leave, or while a file transfer is open. `lanhop update` typed on a machine restarts it at once.
- **FR-008**: Only a lanhop that its service started MUST follow the central. A lanhop run by hand, or a build that is not a release, MUST NOT take updates from the central.
- **FR-009**: Machines on 0.1.0 and 0.1.1 MUST keep working with keyboard and mouse, and MUST NOT be sent Update.
- **FR-010**: `lanhop status` on the central MUST show the version of each side machine, and say which ones cannot update by themselves: on 0.1.1 or older, below the oldest version the release can update from, or with updates off. The central MUST NOT send Update to a machine that needs the installer.
- **FR-011**: From the first release with the stable signature on, an update MUST NOT need a new Accessibility, Local Network, App Management or firewall approval on macOS.
- **FR-012**: A release that needs the installer MUST be refused by `lanhop update`, which MUST print the installer line instead.

### Out of scope for this feature

- lanhop checking for new releases by itself, or updating the central without a command. An update restarts lanhop and pauses the keyboard and mouse for a few seconds, so the person chooses when.
- Signing with an Apple developer certificate, and notarization. They cost money, and the stable signature is enough for the permissions (ADR 10).
- Moving back to an older version on every machine with one command.
- Package managers (Homebrew, apt). They update lanhop their own way.
- Windows. It is spec 003.

## Success criteria

- **SC-001**: From `lanhop update` on the central to the last side machine connected on the new version takes under 60 seconds on the desk of the owner, with no side machine in focus.
- **SC-002**: After an update from one release with the stable signature to the next, no Mac of the desk shows a question of macOS, and each reaches `connected` or `sharing` by itself.
- **SC-003**: A changed archive, a changed checksum file or a missing signature is refused in every case of the tests, and the old program keeps running.

## Assumptions

- The machines can reach github.com when they update.
- The installer put lanhop where it is, as spec 006 and spec 008 describe.
- The owner keeps a copy of the signing keys of the project outside GitHub (ADR 10, ADR 11).

## Open questions

None at the moment.
