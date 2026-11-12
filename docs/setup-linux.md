# Setting up lanhop on Linux

lanhop talks to the kernel's input layer, so it works the same on X11, on Wayland and on a text console. It needs permission on two things:

- the input devices in `/dev/input`, to read the keyboard and mouse when this machine shares them;
- `/dev/uinput`, to create the virtual keyboard and mouse when this machine is controlled.

You can run lanhop with `sudo` to try it. For daily use, give your own user the permissions below, so nothing runs as root.

> These steps follow the usual udev conventions. If they do not work on your distribution, please [open an issue](https://github.com/rodrigogaraujo/lanhop/issues) and say which one you use.

## 1. Let your user read the input devices

```sh
sudo usermod -aG input "$USER"
```

## 2. Let the `input` group create virtual devices

```sh
echo 'KERNEL=="uinput", SUBSYSTEM=="misc", GROUP="input", MODE="0660", OPTIONS+="static_node=uinput"' \
  | sudo tee /etc/udev/rules.d/60-lanhop.rules

echo uinput | sudo tee /etc/modules-load.d/uinput.conf

sudo modprobe uinput
sudo udevadm control --reload-rules
sudo udevadm trigger
```

## 3. Log out and log in again

Group changes apply only to new sessions.

## 4. Check

```sh
lanhop devices
```

It lists the input devices lanhop can read and the kind of each one:

```
/dev/input/event0  other     Power Button
/dev/input/event3  keyboard  AT Translated Set 2 keyboard
/dev/input/event5  mouse     USB Optical Mouse
```

The kind `other` covers what lanhop does not share, such as power buttons, touchpads and touchscreens. A "permission denied" error means step 1 or step 3 is missing.

## Switching through the screen edges

A Linux machine that is being controlled needs nothing more: the machine with the keyboard places its cursor, and pushing the cursor through an edge of its screen moves on to the machine on that side of the [layout](../README.md#layout).

**A Linux machine with the keyboard does not cross the edges of its own screen yet.** The kernel does not know where the cursor is, and lanhop does not ask the desktop yet. `lanhop share` says so when it starts. On that machine:

- leave it with a hotkey, such as Ctrl+Alt+2;
- with a layout file in `~/.config/lanhop/layout`, the edges of the machines it controls work, and the edge that faces the Linux machine brings the keyboard and mouse back to it;
- Ctrl+Alt+1 brings them back as well.

## What this permission means

A member of the `input` group can read every key typed on that machine, in any program. Add only the user who runs lanhop. To undo it:

```sh
sudo gpasswd -d "$USER" input
sudo rm /etc/udev/rules.d/60-lanhop.rules /etc/modules-load.d/uinput.conf
```
