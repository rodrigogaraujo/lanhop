# Setting up lanhop on macOS

> **macOS support is being built.** This page explains the permission lanhop needs on a Mac, so you know what to expect. The commands will be added here when macOS support lands.

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
| As a background service (planned) | **lanhop** itself |

## If it does not work

- **The switch is on, but lanhop still says the permission is missing.** Select the program in the list, remove it with the **−** button and add it again. This is needed after the program was updated or rebuilt, because macOS ties the permission to the exact program it approved.
- **You granted it and nothing changed.** Quit the terminal completely (Cmd+Q, not only closing the window) and open it again.
- **The + button is greyed out, or the list cannot be changed.** The Mac is managed by an organisation that blocks this setting. Ask whoever manages it.
- **macOS also asks for "Input Monitoring".** Allow it for the same program, in **Privacy & Security → Input Monitoring**.

## What lanhop cannot do on a Mac

These are rules of macOS, not settings:

- **Password fields hide the keyboard.** While a password field is active on a Mac that is sharing its keyboard, macOS hides the keys from every other program. The switching hotkey does not work until you leave the field.
- **Login window and lock screen.** A Mac that is being controlled cannot be unlocked from another machine in the first release. Unlock it with its own keyboard.

## Removing the permission

Open **System Settings → Privacy & Security → Accessibility**, select the program and click **−**. lanhop stops working on that Mac at once.
