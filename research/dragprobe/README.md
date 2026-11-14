# Drag probes

These programs answer the questions of task 8 of spec 011 ("Research: dragging") at the desk. They are research only. They are not part of lanhop, and they never touch the installed lanhop app, its folder or its service. The results go into ADR 15.

The session takes about 25 minutes: about 10 on the Mac and 15 on the Linux machine.

## Get the probes

Nothing is built on the Mac. CI builds both probes in the "Drag probes" workflow of PR #74.

```sh
run=$(gh run list --repo rodrigogaraujo/lanhop --workflow dragprobe.yml --branch research/drag-probes --limit 1 --json databaseId --jq '.[0].databaseId')
gh run download "$run" --repo rodrigogaraujo/lanhop -n lanhop-dragprobe-mac
gh run download "$run" --repo rodrigogaraujo/lanhop -n lanhop-dragprobe-linux-amd64
```

- **Mac**: `mkdir -p ~/lanhop-dragprobe && tar -xzf lanhop-dragprobe-mac.tar.gz -C ~/lanhop-dragprobe`. This gives `~/lanhop-dragprobe/lanhop-dragprobe.app`. Keep it there. Never put it in `/Applications` or next to lanhop.app.
- **Linux**: copy `lanhop-dragprobe-linux-amd64.tar.gz` to the Linux machine and run `tar -xzf lanhop-dragprobe-linux-amd64.tar.gz -C ~`. This gives `~/lanhop-dragprobe`.

## Before you start

- **Scratch folder, on both machines:**

  ```sh
  mkdir -p ~/drag-scratch/target
  echo throwaway > ~/drag-scratch/throwaway.txt
  ```

  Every drag uses `~/drag-scratch/throwaway.txt` and goes over `~/drag-scratch/target`, never over a real folder. If a cancel fails, the drop lands there, and nothing real moves.
- **Mac: open a second terminal with the log:**

  ```sh
  tail -F ~/Library/Logs/lanhop-dragprobe.log
  ```

  The probe runs under `open`, so it has no terminal of its own. The log is the only place where its output, its questions and its errors appear.
- **Linux**: the probe prints in the terminal where it runs. Use a terminal in the GNOME session on the Xorg display, so that `echo $DISPLAY` prints `:0`.

## Stopping

- **Mac**: `pkill -x lanhop-dragprobe`. Never use `pkill lanhop`, which would kill lanhop too.
- **Linux**: `pkill -f lanhop-dragprobe`, or Ctrl+C in its terminal.
- On the Mac, Ctrl+C on `open -W` stops only `open`, not the app. Every Mac probe also stops by itself: `watch` after 5 minutes, `cancel` after 2 minutes without a drag, and `promise` after 30 seconds (90 with `-manual`).

## Recovery

- If the system behaves as if a mouse button is still held, click once.
- On the Mac, a probe that stops, on a time limit, an error or `pkill`, first posts the mouse up of any mouse down it posted. It posts that mouse up back on its own blue square, which refuses the drop.
- On Linux, the pointer grab of `drop` and `grab` ends when the probe exits, even if it crashes.
- **Do not press Ctrl+Z** on a Linux probe. It freezes the probe with the pointer grab held, so the mouse stops working until the probe is resumed (`fg`) or killed.

## Gatekeeper and permissions on the Mac

- Download the app with `gh run download`, as above, so it carries no quarantine mark. If it was downloaded with a browser, remove the mark from the probe only:

  ```sh
  xattr -dr com.apple.quarantine ~/lanhop-dragprobe/lanhop-dragprobe.app
  ```

  Never disable Gatekeeper.
- `cancel` and `promise` post events, so they need Accessibility for `lanhop-dragprobe`. This is its own entry, with the identifier `com.github.rodrigogaraujo.lanhop-dragprobe`. It is not Terminal and not lanhop.
  - The first run asks macOS for it, says so in the log, and stops. Turn on `lanhop-dragprobe` in System Settings > Privacy & Security > Accessibility, then run the probe again.
  - `watch` and `promise -manual` post nothing and need no permission.
- The app is signed ad hoc, so macOS ties the grant to this exact build. After each new build, remove the probe's Accessibility entry with the minus button, and add it again.

## During the Mac probes

- Keep away from the screen edge that leads to the Linux machine. lanhop may switch machines in the middle of a drag.
- For `promise`, put a Finder window of `~/drag-scratch/target` at the target point, or give the point with `-to x,y`. Do one `-manual` run first.
- Start each probe from the keyboard, with the cursor resting where the probe needs it.

## The Mac probes, in order

Every command starts the app with `open -n -W ~/lanhop-dragprobe/lanhop-dragprobe.app --args`, followed by the subcommand.

1. **watch**. Question: does a background program see that a file drag started in Finder, and which files?

   ```sh
   open -n -W ~/lanhop-dragprobe/lanhop-dragprobe.app --args watch
   ```

   Drag `throwaway.txt` around in Finder, then drop it back where it was.
   - In the log, look for "drag pasteboard changed", the types, and the file path under "file URLs".
   - Note any alert that macOS shows about reading the pasteboard.
   - Stop it with `pkill -x lanhop-dragprobe`.

2. **cancel**. Question: do a posted Escape and a posted mouse up end the drag in Finder without a drop?

   ```sh
   open -n -W ~/lanhop-dragprobe/lanhop-dragprobe.app --args cancel
   ```

   Start dragging `throwaway.txt`, bring it over the `target` folder within 2 seconds, hold it still there, and keep the button held. About 2 seconds after the drag starts, the probe posts Escape and a mouse up. `-wait` changes the delay, up to 10s.
   - Watch whether the drag image slides back at that moment, while the button is still held.
   - Then release, and check that `target` is still empty.
   - The log shows the left button state from the hardware, and with the posted events counted.

3. **promise -manual**. Question: does Finder take a file promise from a window of the probe at all?

   ```sh
   open -n -W ~/lanhop-dragprobe/lanhop-dragprobe.app --args promise -manual ~/drag-scratch/throwaway.txt
   ```

   A blue square appears under the cursor. Drag from it by hand onto the Finder window of `target`.
   - In the log, look for "the drag session began", "the destination asked for the file at ...", "wrote a copy", and the end operation, which is "copy" when the drop is accepted.
   - A copy of `throwaway.txt` should appear in `target`.

4. **promise**. Question: does the same drag work while posted events drive the mouse?

   ```sh
   open -n -W ~/lanhop-dragprobe/lanhop-dragprobe.app --args promise ~/drag-scratch/throwaway.txt
   ```

   Rest the cursor about 300 points to the left of the open Finder window of `target`. Without `-to`, the probe drags 300 points to the right of the cursor. Press Enter, and do not touch the mouse until the log says the probe ended.
   - The probe puts its square around the cursor and posts a mouse down in the middle of it, then a few small drag steps, then a drag to the target and a mouse up there.
   - It posts nothing if the cursor moved, or if another window, such as a notification, covers the square. The log says which.
   - If the square does not get the mouse down, or no drag session begins, the probe posts only a mouse up on its square, moves nothing, and says why in the log.
   - Look for the same lines as in step 3. Delete the copies from `target` afterwards.
   - Flags: `-to x,y` (display points from the top left of the main display, as the log prints them), `-dx`, `-dy`, `-steps` (at most 200), `-hold` (at most 3s), `-timeout`.

## The Linux probes, in order

`cancel` and `grab` create a uinput device. They need write access to `/dev/uinput`, which the `input` group has after lanhop's setup. Check it with `ls -l /dev/uinput` and `id`. Do not use sudo, because it loses `DISPLAY` and `XAUTHORITY`.

1. **watch**. Question: does a background program see that a file drag started in Nautilus?

   ```sh
   ~/lanhop-dragprobe watch
   ```

   Drag `throwaway.txt` in Nautilus and drop it back where it was.
   - Look for "XdndSelection: new owner", `TARGETS`, the `text/uri-list`, and "every address a file: true".
   - Ctrl+C stops it.

2. **drop**. Question: does Nautilus accept a drop from lanhop's pure Go XDND source?

   ```sh
   ~/lanhop-dragprobe drop ~/drag-scratch/throwaway.txt
   ```

   The pointer is grabbed at once. Press the left button anywhere, move over the Nautilus window of `~/drag-scratch/target`, and release.
   - Look for "XdndEnter sent", "XdndStatus ... accepts true, action XdndActionCopy", "served text/uri-list", and "XdndFinished".
   - A copy should appear in `target`.
   - The probe drops only when Nautilus accepts with XdndActionCopy. If Nautilus chooses another action, such as XdndActionMove, the probe sends XdndLeave, drops nothing, and prints the action. That is a finding.
   - Only button 1 starts the drag, so the wheel does nothing.

3. **cancel**. Question: do Escape and a button release through uinput end a drag of Nautilus without a drop?

   ```sh
   ~/lanhop-dragprobe cancel
   ```

   Start dragging `throwaway.txt` in Nautilus, bring it over `target` quickly, hold it still there, and keep the button held. About 800 ms after the drag starts, the probe checks that X reports button 1 held, then sends Escape and a left button release through its uinput device. Use `-wait 2s` if 800 ms is too short to reach `target`.
   - Watch whether the drag ends while you still hold the button. Then release and check that `target` is empty.
   - If X does not report button 1 held at that moment, the probe sends nothing and says so.

4. **cancel -press-first**. The same, but the uinput device sends a press before its release. The kernel drops a release from a device that never pressed that button, so this run shows whether that matters.

   ```sh
   ~/lanhop-dragprobe cancel -press-first
   ```

5. **grab**. Question: does the cursor still move from a uinput device while lanhop holds a pointer grab?

   ```sh
   ~/lanhop-dragprobe grab
   ```

   Start with the cursor in the middle of the screen, away from the top-left hot corner. Do not touch the mouse.
   - The probe moves +100,+50 without a grab, then -100,-50 with the grab.
   - Look for "the cursor moved while lanhop held the grab: true". Pointer acceleration can change the size of the move. The question is only whether it moves.

6. **drop -now**. Question: can the source start at once while the button is already held?

   ```sh
   sleep 3; ~/lanhop-dragprobe drop -now ~/drag-scratch/throwaway.txt
   ```

   Press Enter, then within 3 seconds press and hold the left button over an empty part of the desktop. Move over `target`, then release.

## What to expect

- `cancel -press-first` cannot release the button while the real one is held. Only Escape can end the drag. If Escape fails, your own release drops the throwaway file into the scratch folder.
- `drop -now` at the desk will likely report AlreadyGrabbed, because the program under the cursor took the pointer when the button was pressed. That is a finding: in lanhop the press never reaches the Linux machine.

## Afterwards

- On the Mac, remove the probe's permissions, always naming its bundle identifier:

  ```sh
  tccutil reset Accessibility com.github.rodrigogaraujo.lanhop-dragprobe
  tccutil reset PostEvent com.github.rodrigogaraujo.lanhop-dragprobe
  ```

  Never run `tccutil reset` without the identifier. It would also wipe lanhop's own grant.
- Delete `~/lanhop-dragprobe` and `~/drag-scratch` on both machines. Keep `~/Library/Logs/lanhop-dragprobe.log` and the Linux terminal output for ADR 15.
