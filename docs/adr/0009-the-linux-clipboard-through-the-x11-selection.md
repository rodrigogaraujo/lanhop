# 9. The Linux clipboard through the X11 selection, in pure Go

Date: 2026-11-13 · Status: accepted

## Context

Clipboard sharing ([spec 005](../../specs/005-clipboard-sharing/spec.md)) needs to read and write the clipboard of a Linux machine. The kernel has no clipboard: it belongs to the desktop session, which is X11 or Wayland. The spec asks for both, and [ADR 3](0003-go-with-no-dependencies.md) rules out a Go dependency.

Measured on the Linux machine of the desk (Ubuntu 24.04.5), on 2026-11-13:

- The desktop session is GNOME 46 on Xorg, an X11 session started by LightDM on display `:0`. No Xwayland runs, so no Wayland session could be measured there.
- None of `wl-clipboard`, `xclip` and `xsel` is installed.
- The systemd user manager of the person holds `DISPLAY=:0` and `XAUTHORITY`, and `graphical-session.target` is active. A user service started after login gets both.
- A throwaway client in pure Go, about 130 lines, spoke to the X server over its socket as that person. It did the connection setup, `InternAtom` and `GetSelectionOwner`, and asked for the `XFIXES` extension, and it read nothing else.
  - It connected in 0.2 ms with the cookie from `XAUTHORITY`, and also without a cookie, because the X server accepts local connections from that person.
  - It saw that a window owns `CLIPBOARD`, and that `XFIXES` is there.
  - The same client also worked against Xvfb in a container without privileges, where `XFIXES` is there too.
- The X server takes requests of up to 256 KiB, so a copy near the 1 MiB limit needs the `INCR` transfer of the X11 conventions.

## Decision

On Linux, lanhop speaks the X11 protocol itself, over the socket of the X server, in a package of its own, `internal/x11`.

- **Read**: ask the owner of `CLIPBOARD` to convert it to `UTF8_STRING` onto a property of a window of lanhop, then read the property, taking `INCR` transfers in parts.
- **Write**: take ownership of `CLIPBOARD`, and answer requests for `TARGETS` and `UTF8_STRING` from other programs while lanhop owns it, sending large texts with `INCR`.
- **Mark**: a count of the selection events of `XFIXES` for `CLIPBOARD`. The X server sends one each time any program takes the clipboard, even the same program twice, so the count moves with every copy without reading it.
- **Private copies**: a copy that offers the target `x-kde-passwordManagerHint` is checked before its text is asked for, and is private when the value is `secret` (FR-014).
- **Where the session is**: `DISPLAY` and `XAUTHORITY` from the environment. When `DISPLAY` is missing, because the service started before the session, lanhop tries the sockets in `/tmp/.X11-unix` with the cookie from `~/.Xauthority` if there is one.
- **Wayland**: GNOME and KDE Plasma run Xwayland and keep the clipboard of Wayland applications and the one of X11 in step. This is deduced from how they are built, and not measured: the desk has no Wayland session.
- **No X server at all**, as at a login screen or in a Wayland session without Xwayland: clipboard sharing is off on that machine, lanhop says why in its log once, and keyboard and mouse work as before (FR-011).

## Consequences

- Nothing to install on Linux, and one implementation for X11 and for GNOME on Wayland.
- About 600 lines of X11 protocol to write and test. Xvfb in a container without privileges can test all of it, without the session of the person.
- lanhop keeps a connection to the X server open while it runs, so that it can answer requests for a text it wrote. When lanhop stops, the text it wrote leaves the clipboard, unless a clipboard manager of the desktop has taken a copy.
- Whether GNOME on Wayland keeps the two clipboards in step is still a risk, until someone measures it on a Wayland session.

## Alternatives

- **Run `wl-copy` and `wl-paste`, or `xclip` and `xsel`.** Little code, but none of them is installed on the desk, so the person would have to install them. Without the data-control protocol of Wayland, which GNOME does not offer, `wl-paste` has to give itself keyboard focus to read.
- **The Wayland data-control protocol.** KDE Plasma and the wlroots compositors offer it. GNOME, the default desktop of Ubuntu, does not.
- **The owner of the selection as the mark.** No extension needed, but a program that copies twice keeps the same window, so the second copy would not move the mark.
