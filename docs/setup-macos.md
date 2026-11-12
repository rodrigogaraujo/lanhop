# Setting up lanhop on macOS

> **macOS support is new.** A Mac can be controlled from another machine with `lanhop receive`, and can share its own keyboard and mouse with `lanhop share`. One person has shared the keyboard and trackpad of one Mac (macOS 26.5, Apple silicon) with a Linux machine, by hotkey and through the screen edge. Two Macs together, an Intel Mac, and a Mac controlled by a person typing on another machine have been tried only by automated tests.

## The quick way: the installer

The [installer](../README.md#install) downloads lanhop, offers a launch agent that starts it when you log in, opens the Accessibility pane and tells you what to add there. [Starting lanhop when you log in](#starting-lanhop-when-you-log-in) describes what it sets up.

The rest of this page explains the permission and the same setup by hand.

## Why lanhop needs a permission

To share a keyboard and mouse, lanhop has to do two things that macOS protects:

- **read** what you type and how you move the mouse, when the Mac is the machine with the keyboard;
- **create** key presses and mouse movement, when the Mac is controlled from another machine.

macOS allows this only to programs you have approved under **Accessibility**. Without it, lanhop starts, tells you the permission is missing and does nothing else.

You grant it once per Mac.

## Granting the permission

1. Open **System Settings → Privacy & Security → Accessibility**.

   You can jump straight to it from a terminal:

   ```sh
   open "x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility"
   ```

2. Click the **+** button under the list. macOS asks for your password or Touch ID.
3. Choose the program that starts lanhop (see the table below) and click **Open**.
4. Make sure the switch next to it is **on**.
5. Quit that program completely and open it again. The permission applies only to programs started after you granted it.

### Which program to choose

macOS gives the permission to the program that starts lanhop, not always to lanhop itself.

| How you start lanhop | Program to add to the list |
|---|---|
| From a terminal window | Your terminal: **Terminal**, **iTerm**, **Ghostty** and so on |
| From the terminal inside an editor | The editor: **Visual Studio Code**, **Zed** and so on |
| From the launch agent that the installer sets up | **lanhop** itself, the file `~/.local/bin/lanhop` |

### Adding lanhop itself

The file chooser of step 3 does not show `~/.local`, because its name starts with a dot. With the chooser open, press Command+Shift+G, type `~/.local/bin/lanhop`, press Return and click **Open**.

## If it does not work

- **The switch is on, but lanhop still says the permission is missing.** Select the program in the list, remove it with the **−** button and add it again. This is needed after the program was updated or rebuilt, because macOS ties the permission to the exact program it approved.
- **You granted it and nothing changed.** Quit the terminal completely (Cmd+Q, not only closing the window) and open it again.
- **The + button is greyed out, or the list cannot be changed.** The Mac is managed by an organisation that blocks this setting. Ask whoever manages it.
- **macOS also asks for "Input Monitoring".** Allow it for the same program, in **Privacy & Security → Input Monitoring**.

## Starting lanhop when you log in

The installer writes a launch agent to `~/Library/LaunchAgents/com.github.rodrigogaraujo.lanhop.plist`. It runs `~/.local/bin/lanhop share` or `~/.local/bin/lanhop receive` every time you log in, as your own user.

- The program to allow under Accessibility is then **lanhop** itself: see [Adding lanhop itself](#adding-lanhop-itself).
- lanhop writes what it prints to `~/Library/Logs/lanhop.log`.
- When lanhop ends with an error, macOS starts it again after 10 seconds. So it starts working by itself soon after you allow it, and soon after `lanhop pair`. `pkill lanhop` ends it until the next login.
- `launchctl bootout gui/$(id -u)/com.github.rodrigogaraujo.lanhop` stops it now. It starts again at the next login for as long as the file exists.
- After every update of lanhop, remove it from the Accessibility list with the **−** button and add it again. [The macOS build is not signed](#the-macos-build-is-not-signed) says why.

`install.sh --uninstall` stops and removes the launch agent, its log and the binary. It keeps `~/Library/Application Support/lanhop`, with the key of the Mac and its trusted machines. Remove lanhop from the Accessibility list yourself.

## The macOS build is not signed

The macOS builds of a release are not signed with an Apple developer certificate and are not notarized by Apple. Two things follow:

- **A build downloaded with a browser does not start.** The browser marks the archive as downloaded from the internet, and macOS refuses to run a program that carries that mark unless Apple has checked it: it says that it could not verify that "lanhop" is free of malware. The installer downloads with `curl`, which sets no mark, so the lanhop it installs starts. If you download the archive with a browser, download `SHA256SUMS` too, check the archive against it, and then take the mark away from the unpacked program:

  ```sh
  shasum -a 256 --ignore-missing -c SHA256SUMS
  xattr -d com.apple.quarantine lanhop
  ```

  Or try to run it once, then allow it under **System Settings → Privacy & Security → Open Anyway**.

- **macOS ties the Accessibility permission to the exact file it approved.** A signed program keeps its permission across updates. lanhop does not: after each update, remove it from the Accessibility list and add it again.

## Letting another machine control the Mac

After granting the permission, pair the Mac with the machine that has the keyboard and mouse, then start receiving:

```sh
lanhop pair ADDRESS-OF-THE-OTHER-MACHINE
lanhop receive
```

`lanhop pair` shows two codes on each machine. Check that they match, swapped, and type `yes` on both.

Not done yet on a Mac that is being controlled:

- Alt and Super cannot be swapped yet, so Alt acts as Option and Super as Command.
- Caps Lock does not switch on the Mac.
- The volume and mute keys do nothing on the Mac.

## Sharing the keyboard and mouse of the Mac

On the Mac that has the keyboard and mouse, wait for each other machine to pair, then share:

```sh
lanhop pair
lanhop share
```

`lanhop share` prints the hotkeys. Control+Option+2 gives the keyboard and mouse to the first machine that `lanhop peers` lists, and Control+Option+1 brings them back.

While another machine has focus, lanhop hides the cursor of the Mac, and shows it again when focus comes back. To hide it, lanhop calls two functions of macOS that Apple does not document: [ADR 5](adr/0005-private-macos-functions-to-hide-the-cursor.md) says why. If a version of macOS stops honouring them, the cursor stays visible and still, and everything else works as before.

`lanhop devices` shows whether the Mac allows lanhop to read and to control the keyboard and mouse.

### Switching through the screen edges

With a [layout file](../README.md#layout) in `~/Library/Application Support/lanhop/layout`, pushing the cursor through an edge of the Mac's screen gives the keyboard and mouse to the machine on that side, and pushing through the opposite edge of that machine brings them back. `lanhop layout` shows the file as lanhop understood it. `lanhop share` says at start whether edge switching is on.

- With more than one display, the edges are the ones of the rectangle around all the displays. Moving between the displays of the Mac never crosses.
- While another machine has focus, the cursor of the Mac stays where it left. When focus comes back through an edge, lanhop puts the cursor on that edge, at the matching height.
- Switching through the edges of a Mac has been tried by one person, on one Mac with a trackpad and one display. A mouse and more than one display have been tried only by automated tests with a simulated screen.

### If the keyboard and mouse stop answering

While another machine has focus, lanhop keeps the keyboard and mouse of the Mac from everything else, and the hotkey brings them back. lanhop gives them back by itself when it stops, when it cannot keep up with the input, when nothing reads what it captures for 10 seconds, and when its permission is taken away. Whenever it gives them back, it also shows the cursor again.

If they are still held:

- From another machine, connect to the Mac with `ssh` and run `pkill lanhop`. The input comes back the moment lanhop ends.
- Without another way in, hold the power button to restart the Mac.

Until you have used lanhop for a while, keep a second way in, such as Remote Login turned on.

Stop lanhop before you remove its permission in System Settings.

## What lanhop cannot do on a Mac

- **Media keys and trackpad gestures.** While another machine has focus, the brightness, volume and playback keys and the trackpad gestures still act on the Mac that has the keyboard. They are not sent to the other machine.

These are rules of macOS, not settings:

- **Password fields hide the keyboard.** While a password field is active on a Mac that is sharing its keyboard, macOS hides the keys from every other program. The switching hotkey does not work until you leave the field.
- **Login window and lock screen.** A Mac that is being controlled cannot be unlocked from another machine in the first release. Unlock it with its own keyboard.

## Removing the permission

Open **System Settings → Privacy & Security → Accessibility**, select the program and click **−**. lanhop stops working on that Mac at once.
