# 3. Go, with no dependencies on Linux

Date: 2026-11-11 · Status: accepted

## Context

The tool is a small daemon that talks to the kernel and to the network. It should be easy to install on several machines and easy for newcomers to read.

## Decision

Write it in Go. The Linux build uses only the standard library and calls `ioctl` directly. macOS support will use cgo to reach CoreGraphics.

## Consequences

- One static binary per platform. Linux builds cross-compile from any machine.
- The standard library covers TLS 1.3, so the transport needs no third-party cryptography.
- macOS builds need a macOS machine or CI runner because of cgo.
