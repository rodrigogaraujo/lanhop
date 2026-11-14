# 10. A stable signature from a certificate of the project

Date: 2026-11-13 · Status: accepted on 2026-11-14

## Context

macOS gives Accessibility to a program and remembers it with a code requirement taken from the program's signature. lanhop.app is signed ad hoc ([ADR 6](0006-an-app-bundle-with-its-own-identifier-on-macos.md)). An ad hoc signature has no certificate, so its requirement is the hash of that exact build:

```
designated => cdhash H"…"
```

Every release changes the hash. Seen on 2026-11-13 on three Macs (macOS 15.5, 26.5.2 and 26.6.2), when they moved from an ad hoc lanhop.app made by hand to the 0.1.1 app: the old "lanhop" line stayed in System Settings, the new app had no permission, turning the old line on again did not help, and only removing it with − and adding the app with + did. Spec 008 has not recorded this yet, and ADR 6 calls it deduced. [Spec 010](../../specs/010-updates/spec.md) wants updates without that work.

A signature made with a certificate gives a requirement that names the identifier and the certificate instead of the build. Measured on 2026-11-13 with `codesign` and a self-signed test certificate, on two builds that differ:

```
designated => identifier "com.github.rodrigogaraujo.lanhop-signtest" and certificate leaf = H"d128a4df96312c2b76715bfb36725773783089e9"
```

Both builds had this same requirement and different code hashes, and `codesign --verify --strict` accepted both.

First run of task 0, on 2026-11-13, on a MacBook with macOS 26.5.2 (arm64). The test app has its own identifier, sits in a temporary folder and runs from a launch agent:

- After the owner gave Accessibility to the first build, the first build swapped the second one in at the same path with `RENAME_SWAP`. The second build, with another code hash and another `LC_UUID`, answered twice that it had Accessibility, with no new click.
- A build of the same app signed ad hoc, swapped in after that, did not have it. This control is not clean: the owner had switched the line off in the meantime. After the line was switched on again, the second build did not have Accessibility either.

Clean repetition of task 0, on 2026-11-14, on the same MacBook (macOS 26.5.2), with no VPN on and nobody touching a line after the first approvals. The ad hoc build of the control is the second build signed again ad hoc, with the same `LC_UUID`:

- In the temporary folder, asking for Accessibility showed no question, and adding the app with the + button of System Settings left no "lanhop-signtest" line. `tccutil reset` answered that it knew no such identifier, even after `lsregister -f` registered that copy. System Settings and `tccutil` find an app by its identifier through LaunchServices, which did not resolve the copy in the temporary folder (deduced). With the owner's agreement, the test went on in `~/Applications`, where lanhop itself sits.
- In `~/Applications`, the first build had Accessibility at once, with no new click: the + had recorded the approval for its requirement without showing the line (deduced). The owner allowed Local Network once, and the first build reached the router.
- The first build, from its launch agent, swapped the second one in with `RENAME_SWAP`. The second build, with another code hash and another `LC_UUID`, had Accessibility with no click. Its first connection to the router, less than a second after the swap, failed with "No route to host", and the next one, 15 seconds later, reached it, with no question.
- A swap back to the first build, run from a shell inside the Cursor editor, not Terminal.app, left it with both permissions.
- No App Management question appeared, and every swap, from the launch agent and from the shell, succeeded.
- Control: the ad hoc build, swapped in at the same path, did not have Accessibility. It ran twice, once to check Accessibility without asking for it and once to swap the second build back in from the shell. After that, the second build, signed with the certificate, did not have Accessibility either. Its first connection failed again with "No route to host", and the next one, 3 seconds later, reached the router.
- The test lines were removed with `tccutil reset All com.github.rodrigogaraujo.lanhop-signtest` after `lsregister -f` registered the copy in `~/Applications`, and the test app and its launch agent were removed. System Settings never showed a "lanhop-signtest" line under Local Network, so that approval stays.

The clean run confirms the first one for Accessibility and measures Local Network for the first time: on macOS 26.5.2, the same certificate keeps both across builds.

A Developer ID certificate from Apple costs a yearly fee. The owner of the project chose the free way.

## Decision

- The project has its own code signing certificate: self-signed, RSA 2048, with the code signing usage, valid for 20 years, named `lanhop`.
- The release workflow signs lanhop.app with it, on the macOS runner, from a temporary keychain that it deletes when it ends. The certificate and its key are secrets of the GitHub environment `release`, which only `v` tags reach and whose jobs each wait for the approval of the owner ([ADR 11](0011-signed-releases-and-side-machines-that-follow-the-central.md)). Pull requests and manual runs on a branch sign with a certificate made inside the job and thrown away.
- The repository keeps the SHA-1 of the certificate, the `certificate leaf` hash, in a file. The release workflow fails when the signed app does not have the requirement with that hash and the identifier `com.github.rodrigogaraujo.lanhop`.
- lanhop carries that identifier in its code, with two certificate hashes: the current one, and the one of a next certificate that the owner keeps offline. `lanhop update` replaces the app only with one that meets the requirement built from the identifier and either hash, checked with the Security framework (spec 010, FR-005).
- The owner keeps a copy of both certificates and their keys outside GitHub.

## Consequences

- From the first release signed this way on, an update keeps Accessibility, as task 0 measured on macOS 26.5.2. The move from 0.1.1, signed ad hoc, to that release needs the permission one last time.
- In task 0, an app signed ad hoc with the same identifier took Accessibility away from the signed one, although it only checked for the permission and did not ask for it. Running the 0.1.1 app, or one built by hand, on a Mac that has the signed release would cost the permission once more (deduced from that one observation). `lanhop update` cannot move to 0.1.1, which carries no signature.
- Local Network keeps its approval across builds on macOS 26.5.2, although the `LC_UUID` changes. Right after a swap, the first connection of the build swapped in can fail once with "No route to host"; in task 0 the next one, 3 and 15 seconds later, passed with no question. Each of those tries was a new process: that lanhop's own retry clears it within one running process is deduced. The installer waits 15 seconds on a blocked connection before asking for Local Network. The firewall of macOS is measured in task 7.
- On macOS 26.5.2, lanhop can replace its own bundle without an App Management question, from its launch agent and from a shell. A swap typed in Terminal.app was not measured.
- Opening the app downloaded with a browser still meets Gatekeeper, as today: the certificate does not come from Apple. The installer and `lanhop update` download without the quarantine mark, as today.
- Anyone with the key can sign a program that macOS takes as lanhop for its permissions. Any program that runs as the person on a Mac can put such an app in `~/Applications` and get the Accessibility that lanhop has, which is what macOS asks for the permission to prevent. The key has to be kept as carefully as the permission is worth.
- If the key leaks or is lost, the next certificate replaces it. `lanhop update` takes the change, because the next hash is already in every machine, but macOS knows the new certificate as another program, so each Mac gives Accessibility once more.
- The certificate expires in 20 years. Whether macOS checks the dates of a self-signed certificate for a requirement like this one is not measured.

## Alternatives

- **A Developer ID certificate from Apple.** The same stable requirement, and Gatekeeper would know the app, with notarization. It costs a yearly fee, and the owner chose not to pay it.
- **Keep the ad hoc signature, and reset the permission in the update.** `tccutil reset` removes the line, but giving the permission back still needs a click in System Settings, on every Mac, at every release.
- **A configuration profile that grants Accessibility.** macOS takes such a profile for Accessibility only from a device management server, which a home desk does not have.
