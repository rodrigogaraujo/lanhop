# Contributing to lanhop

Thanks for your interest. This document says how to get a change merged.

## Before you write code

- **Bug:** open an issue with the steps to reproduce it, or go straight to a pull request if the fix is small.
- **Feature or design change:** open an issue first and wait for a maintainer to agree on the approach. This saves you from writing code that will not be merged.
- **Security problem:** do not open an issue. Follow [SECURITY.md](SECURITY.md).

## Setting up

You need Go 1.23 or newer. There are no other dependencies.

```sh
git clone https://github.com/rodrigogaraujo/lanhop
cd lanhop
go test ./...
```

On Linux, run the input tests with access to `/dev/uinput`:

```sh
sudo go test ./internal/evdev/
```

## Rules for a pull request

1. **One change per pull request.** A bug fix and a refactor go in separate pull requests.
2. **Tests come with the change.** A bug fix includes a test that fails without the fix. A feature includes tests for its behaviour.
3. **The checks pass.** Run these before you push:
   ```sh
   gofmt -l .
   go vet ./...
   go test ./...
   ```
4. **Say how you tested it.** Name the operating system, the desktop session (X11, Wayland compositor or console) and what you ran. Input code behaves differently on each.
5. **Keep the dependency list empty where you can.** A new dependency needs a reason in the pull request description.

## Tests that create input devices

On Linux, the input tests create real virtual keyboards and mice with `uinput`. Whatever they press is real input for the machine that runs them, and the system acts on it. A test once pressed the power key on a fake power button, and the CI machine shut down in the middle of the run.

- Press only the letter A, Shift and the mouse buttons, and move the mouse by small amounts.
- Never press a key the system acts on: power, sleep, suspend, restart, SysRq, or combinations such as Ctrl+Alt+Delete and Ctrl+Alt+F1.
- A test may create a device that *has* such a key, to check how it is classified. It must not press it.
- In `internal/evdev`, press keys only through the `emit` test helper. It stops the test before any other key reaches the device.

## Code style

- Format with `gofmt`.
- Prefer names that explain the code over comments that explain it. If a line needs prose to be understood, rename or restructure it.
- Platform code lives behind build tags, in its own package under `internal/`.
- Significant design decisions get a short record in [docs/adr](docs/adr).
- A feature gets a spec and a plan in [specs](specs) before its code is written.

## Commit messages

Use [Conventional Commits](https://www.conventionalcommits.org/): `feat: ...`, `fix: ...`, `docs: ...`, `test: ...`, `refactor: ...`, `chore: ...`. Write the subject in the imperative and keep it under 72 characters.

## Code you did not write

- **Do not copy code from Deskflow, Synergy, Barrier, Input Leap or lan-mouse.** They are GPL projects and lanhop is MIT. You may read their documentation and describe their behaviour. Their source must not end up here.
- You are responsible for every line you submit, whatever tool helped you write it. Be ready to explain any of it in review.

## Licensing

By contributing, you agree that your contribution is licensed under the [MIT License](LICENSE).

## Conduct

This project follows a [Code of Conduct](CODE_OF_CONDUCT.md). By taking part, you agree to follow it.
