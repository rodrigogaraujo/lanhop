# Setting up lanhop on macOS

> **macOS support is new.** A Mac can be controlled from another machine with `lanhop receive`, and can share its own keyboard and mouse with `lanhop share`. One person has shared the keyboard and trackpad of one Mac (macOS 26.5, Apple silicon) with a Linux machine, by hotkey and through the screen edge. On 2026-11-13, on one desk, an x86-64 machine running macOS ran the 0.1.0 build for x86-64 as the machine with the keyboard, and three Macs with macOS 15.5, 26.5 and 26.6 and a Linux machine each connected to it during the night. Those three Macs ran the lanhop 0.1.0 program inside a `lanhop.app` made by hand and started at login. The `lanhop.app` of a release, `lanhop status`, the wait for the Accessibility permission and the installer have been tried only by automated tests.

## The quick way: the installer

The [installer](../README.md#install) puts `lanhop.app` in Applications, offers a launch agent that starts it when you log in, and then checks lanhop as it runs: it opens the pane of each permission that is missing, says what to switch on, and ends when the Mac is connected or says what is still missing. [Starting lanhop when you log in](#starting-lanhop-when-you-log-in) describes what it sets up.

`lanhop status` says at any time what lanhop is doing on the Mac: waiting for a permission, trying to connect, connected, or why it stopped.

The rest of this page explains the permissions and the same setup by hand.

## Why lanhop needs permissions

To share a keyboard and mouse, lanhop has to do things that macOS protects:

- **read** what you type and how you move the mouse, when the Mac is the machine with the keyboard;
- **create** key presses and mouse movement, when the Mac is controlled from another machine;
- **reach** the machine with the keyboard over the local network, when the Mac is controlled from another machine and lanhop starts at login.

macOS allows the first two only to programs you have approved under **Accessibility**, and the third under **Local Network**. Without Accessibility, lanhop waits, says that the permission is missing, and carries on by itself once you give it. Without Local Network, it cannot connect: see [The Local Network permission](#the-local-network-permission).

You grant each one once per Mac.

## Granting the permission

1. Open **System Settings → Privacy & Security → Accessibility**.

   You can jump straight to it from a terminal:

   ```sh
   open "x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility"
   ```

2. Click the **+** button under the list. macOS asks for your password or Touch ID.
3. Choose the program that starts lanhop (see the table below) and click **Open**.
4. Make sure the switch next to it is **on**.
5. If you chose your terminal or your editor, quit it completely and open it again: the permission applies only to programs started after you granted it. The lanhop app that starts at login waits for the permission and carries on by itself within about 20 seconds, from the release after 0.1.1. lanhop 0.1.1 needs a restart: `launchctl kickstart -k gui/$(id -u)/com.github.rodrigogaraujo.lanhop`.

### Which program to choose

macOS gives the permission to the program that starts lanhop, not always to lanhop itself.

| How you start lanhop | Program to add to the list |
|---|---|
| From a terminal window | Your terminal: **Terminal**, **iTerm**, **Ghostty** and so on |
| From the terminal inside an editor | The editor: **Visual Studio Code**, **Zed** and so on |
| From the launch agent that the installer sets up | **lanhop**, the app in Applications |

### Adding the lanhop app

If the list already has a **lanhop** or an **a.out**, remove it with the **−** button before you add the app. The one left by lanhop 0.1.0 is another program for macOS, and its switch does nothing for the app.

The file chooser of step 3 starts in Applications: choose **lanhop** and click **Open**. If the installer put the app in `~/Applications`, because it could not write to `/Applications`, press Command+Shift+G in the chooser, type `~/Applications`, press Return, then choose **lanhop** and click **Open**.

## The Local Network permission

A Mac that is controlled from another machine connects to it over the local network. macOS lets a program started at login do that only after you allow it, while a program started from a terminal may do it without asking.

- The first time the lanhop that starts at login tries to connect, macOS asks whether lanhop may find and connect to devices on your local network. Click **Allow**. On two of the Macs tried so far, the first try failed while macOS asked, and the next one connected.
- If you missed the question, said no, or it never came, open **System Settings → Privacy & Security → Local Network** and switch on **lanhop**. lanhop should connect on its next try, with no restart. That has not been measured yet: if it does not, run `launchctl kickstart -k gui/$(id -u)/com.github.rodrigogaraujo.lanhop`.
- While it is blocked, the log says `no route to host`, and `lanhop status` should say `blocked` (not tried on a real Mac yet).
- An **a.out** in that list is lanhop 0.1.0, a bare program that macOS could not tie the setting to. Its switch does nothing for the app. [ADR 6](adr/0006-an-app-bundle-with-its-own-identifier-on-macos.md) says why lanhop is an app now.

## The firewall of the Mac with the keyboard

The other machines connect to the Mac that shares its keyboard and mouse. When its firewall is on (**System Settings → Network → Firewall**), it must let lanhop in. The installer shows these commands and runs them if you agree:

```sh
sudo /usr/libexec/ApplicationFirewall/socketfilterfw --add /Applications/lanhop.app
sudo /usr/libexec/ApplicationFirewall/socketfilterfw --unblockapp /Applications/lanhop.app
```

If the app is in `~/Applications`, use that path. By hand, open **Firewall → Options** and allow incoming connections for lanhop. If **Block all incoming connections** is on, switch it off, or no machine can connect. This has not been tried on a real Mac yet.

## If it does not work

- **Start with `lanhop status`.** It reports what the lanhop started at login is doing, not what your terminal is allowed to do. `lanhop devices` answers for the terminal you type it in.
- **The switch is on, but lanhop still says the permission is missing.** Select the program in the list, remove it with the **−** button and add it again. macOS ties the permission to the program it approved, and after an update or a new build it may take lanhop for another program.
- **You granted it and nothing changed.** Quit the terminal completely (Cmd+Q, not only closing the window) and open it again.
- **The + button is greyed out, or the list cannot be changed.** The Mac is managed by an organisation that blocks this setting. Ask whoever manages it.
- **macOS also asks for "Input Monitoring".** Allow it for the same program, in **Privacy & Security → Input Monitoring**.

## Starting lanhop when you log in

The installer puts `lanhop.app` in `/Applications`, or in `~/Applications` when `/Applications` cannot be written, and makes `~/.local/bin/lanhop` a link to the program inside it. An app that is already installed is updated where it is.

It writes a launch agent to `~/Library/LaunchAgents/com.github.rodrigogaraujo.lanhop.plist`. Every time you log in, it runs the program inside the app, such as `/Applications/lanhop.app/Contents/MacOS/lanhop receive`, as your own user, and names the app in `AssociatedBundleIdentifiers` as responsible for it.

- The program to allow under Accessibility is then the **lanhop** app: see [Adding the lanhop app](#adding-the-lanhop-app). On a Mac that is controlled from another machine, it also needs [Local Network](#the-local-network-permission).
- lanhop writes what it prints to `~/Library/Logs/lanhop.log`, and what it is doing to `~/Library/Application Support/lanhop/status`, which `lanhop status` reads.
- Without Accessibility, lanhop waits. Every 10 seconds it ends, and macOS starts it again after another 10 seconds, so that the new process gets a fresh answer: it carries on within about 20 seconds of the moment you allow it, from the release after 0.1.1. When it ends with an error, such as on a Mac that receives, before `lanhop pair`, macOS starts it again after 10 seconds. `pkill lanhop` ends it until the next login.
- `launchctl bootout gui/$(id -u)/com.github.rodrigogaraujo.lanhop` stops it now. It starts again at the next login for as long as the file exists.
- After an update, macOS may treat the new app as another program. If `lanhop status` says `accessibility` although the switch under Accessibility is on, remove lanhop from that list with the **−** button and add the app again. If it says `blocked` although lanhop is on under Local Network, see [The Local Network permission](#the-local-network-permission). This has not been measured yet: [The macOS app is not signed by Apple](#the-macos-app-is-not-signed-by-apple) says why it may happen.

`install.sh --uninstall` stops and removes the launch agent, its log, the app and the link. It keeps `~/Library/Application Support/lanhop`, with the key of the Mac and its trusted machines. Remove lanhop from the Accessibility and Local Network lists yourself.

## The macOS app is not signed by Apple

Each macOS archive of a release unpacks to a folder, `lanhop-darwin-arm64` or `lanhop-darwin-amd64`, that holds `lanhop.app`, `LICENSE` and `README.md`. The app is signed ad hoc with its own identifier, `com.github.rodrigogaraujo.lanhop`. It is not signed with an Apple developer certificate and is not notarized by Apple. Two things follow:

- **An app downloaded with a browser does not start.** The browser marks the archive as downloaded from the internet, and macOS refuses to run a program that carries that mark unless Apple has checked it: it says that it could not verify that "lanhop" is free of malware. The installer downloads with `curl`, which sets no mark, so the lanhop it installs starts. If you download the archive with a browser, download `SHA256SUMS` too and check the archive against it. Then unpack it, take the mark away from the app, move the app to Applications and link the `lanhop` command to it. On an Intel Mac, write `amd64` instead of `arm64`:

  ```sh
  shasum -a 256 --ignore-missing -c SHA256SUMS
  tar -xzf lanhop-darwin-arm64.tar.gz
  xattr -dr com.apple.quarantine lanhop-darwin-arm64/lanhop.app
  mv lanhop-darwin-arm64/lanhop.app /Applications/
  mkdir -p ~/.local/bin
  ln -sf /Applications/lanhop.app/Contents/MacOS/lanhop ~/.local/bin/lanhop
  ```

  If `/Applications` already has a `lanhop.app`, remove it first. If you cannot write to `/Applications`, use `~/Applications`.

  Or try to open it once, then allow it under **System Settings → Privacy & Security → Open Anyway**.

- **macOS may take each new build for another program.** An ad hoc signature changes with every build, so a switch that is on for the old app may do nothing for the new one. An app signed with an Apple developer certificate keeps its permissions across updates. This has not been measured yet: if it happens, remove lanhop from the Accessibility list with the **−** button and add it again.

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

`lanhop devices` shows whether the Mac allows the terminal you type it in to read and to control the keyboard and mouse. For the lanhop that runs at login, use `lanhop status`.

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

## Removing the permissions

Open **System Settings → Privacy & Security → Accessibility**, select the program and click **−**. lanhop stops working on that Mac at once. Under **Local Network**, switch lanhop off.
