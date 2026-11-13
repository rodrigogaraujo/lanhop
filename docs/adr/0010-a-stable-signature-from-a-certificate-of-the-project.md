# 10. A stable signature from a certificate of the project

Date: 2026-11-13 · Status: proposed

## Context

macOS gives Accessibility to a program and remembers it with a code requirement taken from the program's signature. lanhop.app is signed ad hoc ([ADR 6](0006-an-app-bundle-with-its-own-identifier-on-macos.md)). An ad hoc signature has no certificate, so its requirement is the hash of that exact build:

```
designated => cdhash H"…"
```

Every release changes the hash. macOS then keeps the old line in System Settings, which no longer matches, and the new build has no permission. Spec 008 measured that turning the old line back on does not help: only removing it with − and adding the app with + does. [Spec 010](../../specs/010-updates/spec.md) wants updates without that work.

A signature made with a certificate gives a requirement that names the identifier and the certificate instead of the build. Measured on 2026-11-13 with `codesign` and a self-signed test certificate, on two builds that differ:

```
designated => identifier "com.github.rodrigogaraujo.lanhop-signtest" and certificate leaf = H"d128a4df96312c2b76715bfb36725773783089e9"
```

Both builds had this same requirement and different code hashes, and `codesign --verify --strict` accepted both. Whether macOS keeps Accessibility for the second build is not measured yet: task 0 of the plan measures it, and this ADR is accepted only if it does.

A Developer ID certificate from Apple costs a yearly fee. The owner of the project chose the free way.

## Decision

- The project has its own code signing certificate: self-signed, RSA 2048, with the code signing usage, valid for 20 years, named `lanhop`.
- The release workflow signs lanhop.app with it, on the macOS runner, from a temporary keychain that it deletes when it ends. The certificate and its key are GitHub Actions secrets of the repository, and only the release workflow reads them, only on a tag.
- The repository keeps the SHA-1 of the certificate, the `certificate leaf` hash, in a file. The release workflow fails when the signed app does not have the requirement with that hash and the identifier `com.github.rodrigogaraujo.lanhop`.
- `lanhop update` replaces the app only with one that meets the requirement of the running app (spec 010, FR-005).
- The owner keeps a copy of the certificate and its key outside GitHub.

## Consequences

- From the first release signed this way on, an update keeps Accessibility, if task 0 confirms it. The move from 0.1.1, signed ad hoc, to that release needs the permission one last time.
- Local Network and the firewall of macOS are expected to keep their approval the same way. Task 7 of the plan measures both.
- Opening the app downloaded with a browser still meets Gatekeeper, as today: the certificate does not come from Apple. The installer and `lanhop update` download without the quarantine mark, as today.
- Anyone with the key can sign a program that macOS takes as lanhop for its permissions. To use it on a Mac, that program has to replace lanhop.app on that Mac, which needs write access there already. If the key leaks or is lost, a new certificate replaces it, and each Mac gives Accessibility once more.
- The certificate expires in 20 years. Whether macOS checks the dates of a self-signed certificate for a requirement like this one is not measured.

## Alternatives

- **A Developer ID certificate from Apple.** The same stable requirement, and Gatekeeper would know the app, with notarization. It costs a yearly fee, and the owner chose not to pay it.
- **Keep the ad hoc signature, and reset the permission in the update.** `tccutil reset` removes the line, but giving the permission back still needs a click in System Settings, on every Mac, at every release.
- **A configuration profile that grants Accessibility.** macOS takes such a profile for Accessibility only from a device management server, which a home desk does not have.
