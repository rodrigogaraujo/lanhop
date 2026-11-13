# 6. An application bundle with its own identifier on macOS

Date: 2026-11-13 · Status: accepted

## Context

macOS asks the person before a program may use the keyboard and mouse (Accessibility) and before it may reach other machines on the local network (Local Network). Each permission belongs to a program, and macOS has to recognise that program every time it runs.

The macOS build of 0.1.0 was a bare program, signed ad hoc by the linker with the identifier `a.out`, the name every program built this way gets. Started by its launch agent on a Mac, it could not reach the central: `connect: no route to host`. Its Local Network setting was on, and the system log said on every attempt that the setting allowed "lanhop (a.out)", that it could not find `a.out`, and that it had 0 executables to apply the setting to. Started from a terminal, the same program connected, because macOS lets programs started from Terminal use the local network without asking.

Apple's [TN3179](https://developer.apple.com/documentation/technotes/tn3179-understanding-local-network-privacy) says that macOS tracks a program by its code signature, that ad hoc signatures make that unreliable, that launch agents are subject to the Local Network permission and launchd daemons running as root are not, and that a launch agent not installed with `SMAppService` should name its app in `AssociatedBundleIdentifiers`.

## Decision

On macOS, lanhop is an application bundle, `lanhop.app`, with the identifier `com.github.rodrigogaraujo.lanhop`.

- Its `Info.plist` holds the identifier, the version and `NSLocalNetworkUsageDescription`, the sentence macOS shows when it asks about the local network.
- The release workflow signs the bundle ad hoc with that identifier, which seals the `Info.plist` into the signature.
- The launch agent starts the program inside the bundle and names the bundle in `AssociatedBundleIdentifiers`.
- The installer puts the bundle in `/Applications`, or in `~/Applications` when `/Applications` cannot be written, and links `lanhop` on the `PATH` to the program inside it.

The 0.1.0 program, unchanged, was put in such a bundle by hand on a Mac with macOS 26.5.2 and started by the launch agent. After the bundle was allowed under Accessibility, it connected to the central, and macOS kept a Local Network setting for the identifier.

## Consequences

- macOS shows "lanhop" in Accessibility and in Local Network, and the + button of System Settings opens in Applications, where lanhop now is.
- The archive of a Mac holds a folder instead of one file.
- The signature is still ad hoc. A browser download still needs the quarantine mark removed, and macOS may still ask again for Accessibility after an update, because an ad hoc signature changes with every build. A Developer ID would fix both, and it needs a paid Apple account.
- The Accessibility and Local Network settings of 0.1.0 belong to another program, `a.out` at its old path. They stay in System Settings until the person removes them, and they look like the settings of lanhop.
- A launchd daemon as root would skip the Local Network permission, but it runs outside the session of the person, where the keyboard and mouse are. lanhop stays a launch agent.
