# 10. A stable signature from a certificate of the project

Date: 2026-11-13 · Status: proposed

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

Both builds had this same requirement and different code hashes, and `codesign --verify --strict` accepted both. Whether macOS keeps Accessibility for the second build is not measured yet: task 0 of the plan measures it, and this ADR is accepted only if it does.

A Developer ID certificate from Apple costs a yearly fee. The owner of the project chose the free way.

## Decision

- The project has its own code signing certificate: self-signed, RSA 2048, with the code signing usage, valid for 20 years, named `lanhop`.
- The release workflow signs lanhop.app with it, on the macOS runner, from a temporary keychain that it deletes when it ends. The certificate and its key are secrets of the GitHub environment `release`, which only `v` tags reach and which needs the approval of the owner for each run ([ADR 11](0011-signed-releases-and-side-machines-that-follow-the-central.md)). Pull requests and manual runs on a branch sign with a certificate made inside the job and thrown away.
- The repository keeps the SHA-1 of the certificate, the `certificate leaf` hash, in a file. The release workflow fails when the signed app does not have the requirement with that hash and the identifier `com.github.rodrigogaraujo.lanhop`.
- lanhop carries that identifier in its code, with two certificate hashes: the current one, and the one of a next certificate that the owner keeps offline. `lanhop update` replaces the app only with one that meets the requirement built from the identifier and either hash, checked with the Security framework (spec 010, FR-005).
- The owner keeps a copy of both certificates and their keys outside GitHub.

## Consequences

- From the first release signed this way on, an update keeps Accessibility, if task 0 confirms it. The move from 0.1.1, signed ad hoc, to that release needs the permission one last time.
- Local Network is expected to keep its approval too, but TN3179 says that macOS also tracks the `LC_UUID` of the program, which changes with each Go build. Task 0 measures it with the same test app. If it is lost, the plan gets a task for it: either keep the `LC_UUID` of the program the same from release to release, or have `lanhop update` and `lanhop status` say that macOS asks for Local Network once after each update. The firewall of macOS is measured in task 7.
- macOS 13 and later can ask for App Management before a program changes an app bundle. Task 0 measures whether lanhop can replace its own bundle without that question. If it is asked, the installer asks for App Management once, as it does for Accessibility, and `lanhop status` says so when it is missing.
- Opening the app downloaded with a browser still meets Gatekeeper, as today: the certificate does not come from Apple. The installer and `lanhop update` download without the quarantine mark, as today.
- Anyone with the key can sign a program that macOS takes as lanhop for its permissions. Any program that runs as the person on a Mac can put such an app in `~/Applications` and get the Accessibility that lanhop has, which is what macOS asks for the permission to prevent. The key has to be kept as carefully as the permission is worth.
- If the key leaks or is lost, the next certificate replaces it. `lanhop update` takes the change, because the next hash is already in every machine, but macOS knows the new certificate as another program, so each Mac gives Accessibility once more.
- The certificate expires in 20 years. Whether macOS checks the dates of a self-signed certificate for a requirement like this one is not measured.

## Alternatives

- **A Developer ID certificate from Apple.** The same stable requirement, and Gatekeeper would know the app, with notarization. It costs a yearly fee, and the owner chose not to pay it.
- **Keep the ad hoc signature, and reset the permission in the update.** `tccutil reset` removes the line, but giving the permission back still needs a click in System Settings, on every Mac, at every release.
- **A configuration profile that grants Accessibility.** macOS takes such a profile for Accessibility only from a device management server, which a home desk does not have.
