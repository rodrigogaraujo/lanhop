# Feature Specification: Installer and release builds

**Created**: 2026-11-12 · **Status**: Draft · **Depends on**: [spec 001](../001-hotkey-switching/spec.md)

I paste one line into a terminal on each machine. lanhop is downloaded, allowed to use the keyboard and mouse, and set to start by itself. I need no Go and no compiler, and the program is not installed as root.

This spec covers the builds of a release and one installer for Linux and macOS. A Homebrew formula comes after them, as the last task of the plan.

## Terms

- **Release**: a version of lanhop published on GitHub, with one archive per system and architecture.
- **Archive**: a `tar.gz` with the program, the license and the README.
- **Role**: what a machine does. It **shares** when the keyboard and mouse are plugged into it, and it **receives** otherwise.
- **Service**: what starts lanhop when the user logs in. A systemd user unit on Linux, a launch agent on macOS.

Sharer, receiver and pairing mean the same as in spec 001.

## User scenarios

### Story 1 - Install with one line (Priority: P1)

I run one command and `lanhop` is on my machine, in the build that fits it.

**Why this priority**: until now a person needed Go, and Xcode on a Mac, to try lanhop.

**Independent test**: on a machine without Go, run the one line and then `lanhop version`.

**Acceptance scenarios**:

1. **Given** a Linux or macOS machine, x86-64 or arm64, **When** I run the installer, **Then** it downloads the archive of the latest release for that machine and puts `lanhop` in `~/.local/bin`, without asking for root.
2. **Given** I name a version or a directory, **When** I run the installer, **Then** it installs that version, in that directory.
3. **Given** the download does not match the checksum published with the release, **When** the installer checks it, **Then** it refuses, says why, and leaves whatever was installed before as it was.
4. **Given** a system or an architecture that has no build, **When** I run the installer, **Then** it says so and changes nothing.
5. **Given** the directory is not on my `PATH`, **When** the installer ends, **Then** it says which line to add, and where.
6. **Given** lanhop is installed, **When** I run the installer again, **Then** the newer version replaces the old one and nothing is duplicated.

### Story 2 - Be set up to start with my session (Priority: P1)

The installer asks me what it needs to know, explains each permission, and leaves lanhop starting by itself.

**Why this priority**: the permission steps are where a new user gives up. On a Mac, the program to allow changes when lanhop runs as a service.

**Independent test**: on a fresh machine, answer the questions, log out and in, and find lanhop running in the role chosen.

**Acceptance scenarios**:

1. **Given** the installer is piped into a shell, **When** it has a question, **Then** it asks on the terminal, and my answers are not read from the pipe.
2. **Given** the question "are the keyboard and mouse plugged into this machine?", **When** I answer yes, **Then** the machine is set up to share. **When** I answer no, **Then** it is set up to receive.
3. **Given** a Linux machine, **When** a permission step needs `sudo`, **Then** the installer explains the step, shows the exact commands and runs them only if I agree. If I refuse, it carries on.
4. **Given** a Linux machine, **When** the role is known, **Then** the installer writes a systemd user unit that runs lanhop in that role, and enables it only if I agree.
5. **Given** a Mac, **When** I agree, **Then** the installer writes a launch agent that runs lanhop in that role at login, explains that lanhop itself must be allowed under Accessibility, opens that pane, waits until I say it is done, and starts lanhop.
6. **Given** the role is known, **When** the installer reaches pairing, **Then** it explains the command of each machine and offers to run the one of this machine.
7. **Given** the machine shares, **When** pairing is over, **Then** the installer offers the editor of the layout, or says where the layout file is when the installed lanhop has no editor.

### Story 3 - Install with nobody at the keyboard (Priority: P2)

A script installs lanhop on my machines.

**Why this priority**: the one line is also what configuration tools run, and there it must never wait or change more than it was told to.

**Independent test**: run the installer with its input and output away from a terminal, with and without the variables.

**Acceptance scenarios**:

1. **Given** no terminal, **When** the installer runs, **Then** it installs the program and nothing else: no service, nothing started, nothing as root. It says how to do the rest.
2. **Given** no terminal and the role in a variable, **When** the installer runs, **Then** every question is answered with no.
3. **Given** the role and "yes to everything" in variables, **When** the installer runs, **Then** it does every step without asking. It does not pair, because pairing needs a person to compare the codes.

### Story 4 - Remove it (Priority: P2)

One command undoes what the installer did.

**Why this priority**: a tool that starts by itself and reads the keyboard must be easy to remove completely.

**Independent test**: install, uninstall, and look for what is left.

**Acceptance scenarios**:

1. **Given** lanhop was installed by the installer, **When** I run it with `--uninstall`, **Then** lanhop stops, and the service and the program are removed.
2. **Given** the installer wrote the udev rule, **When** I uninstall, **Then** it shows the commands that remove the rule and runs them only if I agree. A rule that I wrote by hand is left alone.
3. **Given** I uninstall, **When** it ends, **Then** the key of the machine and its trusted machines are still there, and the installer says where.

### Story 5 - Publish a release by pushing a tag (Priority: P1)

As the maintainer, I push a version tag and the release appears with its archives.

**Why this priority**: the installer has nothing to download without it.

**Independent test**: push a tag to a fork and look at the release.

**Acceptance scenarios**:

1. **Given** a tag `v*` is pushed, **When** the workflow ends, **Then** a release of that tag has four archives (Linux and macOS, x86-64 and arm64) and a `SHA256SUMS` file.
2. **Given** a release build, **When** I run `lanhop version`, **Then** it prints the version of the tag.
3. **Given** a pull request, **When** the workflow runs, **Then** it builds and packs the same four archives, keeps them with the run, installs one of them with the installer, and publishes nothing.

## Edge cases

- The release exists but one of its files cannot be downloaded.
- The directory of the program has a space in its name, or a character that a unit or a launch agent cannot hold.
- The user is already in the `input` group, or the udev rule already exists.
- The user joined the `input` group in this session, so the group does not apply yet.
- The machine has no systemd.
- A command that needs `sudo` fails, or the password is wrong.
- lanhop is updated on a Mac where it was already allowed under Accessibility.
- The archive of a Mac is downloaded with a browser instead of the installer.
- `LANHOP_CONFIG_DIR` is set in the shell that runs the installer, and the service does not see it.
- `lanhop pair` or the layout editor is stopped with Ctrl+C while the installer runs it.

## Requirements

### Functional requirements

- **FR-001**: Each release MUST have one archive for each of Linux x86-64, Linux arm64, macOS arm64 and macOS x86-64, with the program, the license and the README, and one file with the SHA-256 of every archive.
- **FR-002**: The Linux builds MUST be static and MUST NOT need any library of the system.
- **FR-003**: The program of a release MUST report the version of that release.
- **FR-004**: The installer MUST be one POSIX shell script for both systems, and MUST work when it is piped into a shell.
- **FR-005**: The installer MUST check the archive against the checksum of the release before it installs anything, and MUST refuse a file that does not match.
- **FR-006**: The installer MUST install the program for the current user, in `~/.local/bin` unless told otherwise, and MUST NOT need root for it.
- **FR-007**: The installer MUST read answers from the terminal, never from its standard input.
- **FR-008**: Without a terminal, every question MUST count as refused: the installer installs the program, starts nothing and changes nothing that needs root.
- **FR-009**: The installer MUST show each command that needs root before it asks to run it, and MUST carry on when the user refuses or the command fails.
- **FR-010**: The installer MUST set lanhop up to start at login in the chosen role, with a systemd user unit on Linux and a launch agent on macOS, and MUST start it only when the user agrees.
- **FR-011**: On macOS the installer MUST explain that lanhop itself needs the Accessibility permission, open that pane and wait for the user.
- **FR-012**: The installer MUST explain pairing and offer to run it, and on a sharer MUST offer the layout editor or name the layout file.
- **FR-013**: The role and "yes to everything" MUST be settable through environment variables, for installs with nobody at the keyboard.
- **FR-014**: `--uninstall` MUST stop lanhop and remove the service and the program, MUST remove the udev rule only when the installer wrote it and the user agrees, and MUST keep the configuration directory and say where it is.
- **FR-015**: Running the installer again MUST leave one program and one service, in the newest version.
- **FR-016**: The documentation MUST say that the macOS build is not signed and what follows from that.

### Out of scope for this feature

- Packages for distributions (`deb`, `rpm`, AUR). They can use the same archives later.
- Signing and notarizing the macOS build.
- Installing for every user of a machine, or as a system service.
- Starting lanhop on a Linux system without systemd. The installer still installs the program there.
- Updating by itself.
- Windows.

## Success criteria

- **SC-001**: On a machine without Go, a person goes from the one line to a running, paired lanhop in under 5 minutes, following only what the installer says.
- **SC-002**: A person who refuses every question ends with the program installed and nothing else changed.
- **SC-003**: After `--uninstall`, nothing of lanhop runs or starts at login, and the only thing left in the home directory is the configuration directory.
- **SC-004**: A pushed tag becomes a release with no manual step.

## Assumptions

- The machine has `curl`, `tar` and either `sha256sum` or `shasum`. Every supported system ships them.
- The Linux machine uses systemd and has the `input` group, as the desktop distributions do.
- The checksum guards against a damaged or incomplete download. It comes from the same place as the archive, so it is not a signature.
