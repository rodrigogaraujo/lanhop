# 5. Two private macOS functions to hide the cursor

Date: 2026-11-12 · Status: accepted

## Context

While another machine has focus, lanhop holds the cursor of a Mac that shares its keyboard and mouse in place. The cursor stayed visible, so a person saw two cursors: the one that moves on the other machine and a still one on the Mac. That was reported as confusing.

macOS has public functions to hide and show the cursor, `CGDisplayHideCursor` and `CGDisplayShowCursor`. Their documentation says that in most cases the caller must be the foreground application. lanhop has no window and is never in front, so on their own they do nothing.

No public function lifts that limit. What the public API leaves is to move the cursor to a place where it is not seen, or to turn lanhop into an application with a window that covers the cursor. Both are larger changes with side effects of their own.

## Decision

Before it hides the cursor for the first time, lanhop sets the property `SetsCursorInBackground` on its own connection to the window server. It does that with two private functions of macOS, `CGSSetConnectionProperty` and `_CGSDefaultConnection`. After that the public functions work from the background.

- The two functions are in no public header. They are declared by hand in `internal/macos/quartz_darwin.c`, as other programs that hide the cursor from the background declare them.
- They are the only private functions lanhop calls. Another one needs its own record.
- The cursor is hidden exactly while the input of the Mac is taken. Hiding and showing happen in one place, under the lock that guards the input, so everything that gives the input back also shows the cursor.

## Consequences

- Apple does not document these functions and may change them in any release of macOS. Apple does not accept programs that call private functions in the Mac App Store. lanhop is not distributed there.
- If macOS stops honouring the property, the cursor stays visible, held in place as before, and nothing else changes.
- If macOS removes one of the functions, a lanhop built before that stops with a "symbol not found" error from the dynamic loader, when it starts or the first time it hides the cursor, until a release drops the calls. CI links both functions on the newest macOS runner on every push, so a removal shows up there first. Declaring them as weak imports and checking them before the call would turn this case into the one above. That is left out until it can be tried on a Mac.
- macOS counts the hides, and the count belongs to the connection of lanhop to the window server. A cursor that lanhop hid should come back when lanhop ends, however it ends, because the connection ends with the process. Apple does not document this and it has not been tried with `kill -9`.
- While lanhop is stopped but not ended, for instance with `kill -STOP`, the cursor stays hidden until lanhop continues or ends.
- An Apple engineer wrote in the developer forums that the window server keeps control of the cursor for the Dock, which blocks this property there. A cursor that was left over the Dock may stay visible.
- With more than one display nothing changes: the documentation of both public functions says that their display argument has no effect, and the cursor is hidden wherever it is.
