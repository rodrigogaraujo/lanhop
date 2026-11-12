# 2. Linux input events as the wire format

Date: 2026-11-11 · Status: accepted

## Context

Every platform names keys and buttons differently. The machines need one shared vocabulary.

## Decision

Events travel as Linux input events: `type` (2 bytes), `code` (2 bytes) and `value` (4 bytes, signed), big-endian, 8 bytes per event. A batch ends with a `SYN_REPORT` event, as it does in the kernel.

## Consequences

- Linux machines forward and replay events without translating them.
- Other platforms translate at the edge, to and from their own key codes.
- The codes describe physical keys, so each machine applies its own keyboard layout.
- Fixed-size frames need no length prefix and no allocation to parse.
