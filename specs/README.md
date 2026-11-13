# Specs

Each feature gets a folder with two files:

- `spec.md` says **what** the feature does and **why**, from the user's point of view. It has no implementation detail.
- `plan.md` says **how** it will be built: the design, the tasks and how each task is verified.

A spec is agreed on before its code is written. To propose a feature, open an issue first (see [CONTRIBUTING.md](../CONTRIBUTING.md)).

| # | Feature | Status | Release |
|---|---|---|---|
| [001](001-hotkey-switching) | Linux and macOS with hotkey switching | Accepted | First |
| [002](002-edge-switching) | Switching through the screen edges, with a layout file | Draft | First |
| 003 | Windows | Not started | |
| 004 | Automatic discovery on the local network | Not started | |
| 005 | Clipboard sharing | Not started | |
| [006](006-packages) | Packages and services (systemd, udev, launchd, Homebrew) | Draft | |
| [007](007-layout-editor) | A drag-and-drop editor for the layout file | Draft | |
| [008](008-permissions-checked) | An installer that asks for every permission and checks it, and lanhop as an app on macOS | Draft | 0.1.1 |
