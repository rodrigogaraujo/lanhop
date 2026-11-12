# 1. Kernel-level input on Linux

Date: 2026-11-11 · Status: accepted

## Context

Linux has three ways to capture and replay input: X11 extensions, Wayland portals and compositor protocols, and the kernel interfaces `evdev` and `uinput`. X11 does not work on Wayland. The Wayland options differ between compositors and versions.

## Decision

Capture with `evdev` (using an exclusive grab while another machine has focus) and replay with a `uinput` virtual device.

## Consequences

- One code path for X11, every Wayland compositor and the text console.
- The process needs permission to open `/dev/input/event*` and `/dev/uinput`. We will ship a udev rule and a dedicated group so it does not have to run as root.
- The kernel does not know where the cursor is on screen. Switching by screen edge needs another source for the cursor position, so the first versions switch with a hotkey.
