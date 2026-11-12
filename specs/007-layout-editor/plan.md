# Implementation Plan: A drag-and-drop editor for the layout

**Spec**: [spec.md](spec.md) · **Created**: 2026-11-12 · **Status**: Draft

## Goal

`lanhop layout --edit` opens a page in the browser where the machines are dragged to their places, and saves the layout file of [spec 002](../002-edge-switching/plan.md).

## How it works

```
lanhop layout --edit
        │ listens on 127.0.0.1, on a port the system chooses, and makes a random token
        ▼
http://127.0.0.1:PORT/?token=TOKEN ──► "open" on macOS, "xdg-open" on Linux
        │
        ▼
   the page ──► GET  /desk      the machines, and the desk as the layout file has it
            ──► POST /arrange   an arrangement in, the grid to show and the neighbours or the problem out
            ──► POST /save      an arrangement in, the layout file written when lanhop can read it back
            ──► POST /done      lanhop stops
```

- **The page only draws and moves boxes.** After each move it sends the arrangement and shows what comes back. Everything that can be right or wrong lives in Go, where it has tests: the size of the grid, the neighbours, the problems and the text of the file.
- **A save is checked by the parser of the layout file.** lanhop draws the arrangement as text, reads that text back with `internal/layout`, and writes it only when the parser accepts it and this machine is in it.
- **The file is replaced in one step**: written to a temporary file beside it, then renamed over it, as `internal/trust` saves the list of trusted machines. `lanhop share` already reloads the file when it changes.

## The page

One HTML file with its style and script inside, kept in the binary with `embed`. It loads nothing else.

| Part | What it is for |
|---|---|
| Desk | The grid. A place holds a box or is free. A free place takes a drop, or Enter or Space while a box is picked up |
| Tray, titled "Not on the desk" | The boxes with no place, and one free place that sends a box back |
| Box | A machine. It is dragged, or picked up with a click, Enter or Space |
| Words | The neighbours of each placed machine, or the problem |
| Save and Done | Save writes the file. Done stops lanhop |
| Two sentences | Saving does not keep comments, shown only while the file has them. A running `lanhop share` uses a saved layout at once |

## Decisions

| Decision | Choice | Why | Given up |
|---|---|---|---|
| Where the editor runs | A web page served by lanhop, in the browser the machine already has | One page works on Linux and on macOS, with the standard library alone ([ADR 3](../../docs/adr/0003-go-with-no-dependencies.md)) | A native window. A machine with no browser writes the file by hand |
| Who may talk to it | Loopback address only, a 128-bit random token in the address and in every request, and the Host of every request must be the address it listens on | Another machine cannot connect. A web site cannot guess the token, and cannot reach the editor under its own name. Another user of the machine does not know the token | A page that can be opened again from a bookmark. Each run has a new address |
| Which names are accepted | This machine and the trusted machines, read again at every request | The file never gets a name `lanhop share` would refuse | Placing a machine before it is paired |
| Where the rules live | In Go. The page asks after every move | The script was not run in a browser by a test, so it must hold as little as it can | One request per move, to the same machine |
| Dragging | The drag and drop of HTML, and the same boxes and places as buttons for the keyboard and for a click | No library, and one way to move a box with either hand | Touch screens, which do not drag this way |
| Size of the desk | One empty row and column around the machines, until a side has nine places | Room to grow in every direction. Nine is the sharer plus the eight machines the hotkeys reach | A wider desk made by dragging. A file written by hand may be wider, and is shown whole |
| Comments | Not kept. The page says so before the first save | The file is written from the arrangement | Comments of a file that is also edited on the page |
| A file with problems | The command prints them and does not open the editor | The page would have to guess which machines to show, and a save would throw the rest away without saying | Fixing a broken file by dragging |
| A file that is a symbolic link | It is replaced by a plain file, as the list of trusted machines is | One step, with no half-written file | Layout files kept elsewhere through a link |
| This machine | It must be on the desk to save | A layout without the sharer has no edge to leave it by | Layouts of only the other machines, which the file format still allows by hand |

## Tasks

- [ ] **1. Write and read back.** `internal/layout` draws a grid as the text of the file, gives the rows of a parsed layout, and says whether the file had comments. → Verify: table tests, and each drawn desk is read back as the same desk.
- [ ] **2. The editor.** `internal/layoutedit`: the grid that grows, the checks, the save in one step, and the handlers behind the token and the Host. → Verify: tests for every placement of three machines on three by three, for each request without the token or with another Host, for unknown names, a machine placed twice and this machine left out, and for the file after a refused save.
- [ ] **3. The command.** `lanhop layout --edit` serves the editor, opens the browser, and stops on Done or when interrupted. → Verify: tests start the command with a browser that is not real, read the address it prints, press Done and see the port closed.
- [ ] **4. The page.** Drag and drop, the keyboard, and the words. → Verify: by hand, in Firefox, Chrome and Safari: the steps are in the pull request.

## Done when

- [ ] Every acceptance scenario of the spec passes on Linux and on macOS, tried by a person in a browser.
- [ ] The README says how to open the editor.

## Risks

- **The page was not run in a browser by a test.** There is no browser in the tests, and the project takes no dependency to drive one. The script is small, the rules are in Go, and a person tries the page before a release.
- **Drag and drop differs between browsers.** The click and keyboard way does not depend on it.
- **A browser that keeps the address.** The address is in the history of the browser. It stops working when lanhop stops, and the next run has another token.
- **The address on a command line.** lanhop gives the address, with its token, to `open` or `xdg-open` as an argument, and a browser that starts for it may keep it as its own argument. Another user of the same machine who lists the running programs at that moment can read it, and could then move the trusted machines around the desk until the editor stops. They cannot add a machine, nor write any other file. Opening a private file that leads to the address would close this, and is left for later.
