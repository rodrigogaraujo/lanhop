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

On Linux, the input tests create real virtual keyboards and mice with `uinput`. Whatever they emit is real input for the machine that runs them, and the system acts on it. A test once pressed the power key on a fake power button, and the CI machine shut down in the middle of the run.

Running these tests with `sudo` types the letter "a" a few times, moves the pointer by a few pixels, clicks the left mouse button once, wherever the cursor is, and presses Ctrl+Alt+2 a few times, on the machine that runs them.

- Never emit a key the system acts on: power, sleep, suspend, restart, SysRq, or combinations such as Ctrl+Alt+Delete and Ctrl+Alt+F1.
- A test may create a device that *has* such a key, to check how it is classified. It must not press it.
- In `internal/evdev`, tests reach a device only through the `emit` test helper. It lets through the letter A, the left mouse button, mouse movement and scrolling, and the keys of the hotkeys Ctrl+Alt+1 and Ctrl+Alt+2: left Ctrl, left Alt and the digits 1 and 2. It stops the test on anything else. A test fails if any test in that package calls `Emit` directly.
- Press Ctrl and Alt only as part of those two hotkeys. The end-to-end tests press Ctrl+Alt+2 once each to give the focus to their receiver. The machine that runs them sees that hotkey, but nothing that is typed while a receiver has the focus: the sharer takes the fake keyboard and mouse away from that machine until the focus comes back.
- A test that replays input on a real virtual device takes that device for itself before anything is replayed: it opens the event node of the device, calls `Grab` on it and keeps it open until the replay has stopped. Only the test then reads what is replayed, so the machine that runs the test does not act on it. `receiveFrom` in `internal/evdev/endtoend_test.go` does this, and it is the pattern for every new test that replays.
- Tests in other packages that replay through a real virtual device, such as the ones in `cmd/lanhop`, are not covered by the `emit` helper and do not take their device for themselves yet. They may replay only the letter A, the left mouse button, Shift, mouse movement and scrolling.
- Keep mouse movement and scrolling small.

On macOS, the tests that post real input are off by default, because they act on the Mac that runs them:

- `LANHOP_TEST_REAL_INPUT=1 go test ./internal/macos/` presses Shift and F19, moves the cursor by 3 pixels and scrolls one line, and undoes each of them.
- `LANHOP_TEST_REAL_INPUT=clicks go test ./internal/macos/` also clicks, double clicks and drags by 3 pixels where the cursor is. Leave the cursor over something a click cannot harm, such as an empty text field.

Both need the Accessibility permission, and macOS may also ask for Input Monitoring (see [docs/setup-macos.md](docs/setup-macos.md)). Every event lanhop posts carries a marker, and the tests count only marked events, so typing while they run does not confuse them.

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
