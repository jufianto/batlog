# ADR-0001: Pure Go, built with `CGO_ENABLED=0`
Date: 2026-09-26
Status: accepted

## Context
batlog is a single binary for Apple Silicon and Intel Macs, released through
Homebrew and built in CI. It needs a local database (SQLite) and battery data
that macOS exposes through C APIs. Both could pull in cgo.

## Options considered
- **cgo** (C SQLite, IOKit calls): fastest at runtime; needs a C toolchain on
  every build machine, must build on a Mac per architecture, slower builds.
- **Pure Go** (`modernc.org/sqlite`, shell out for battery data): builds
  anywhere including Linux CI, cross-compiles both architectures from one job;
  a little slower per query, which does not matter at one write a minute.
- **purego** (call C without cgo): pure-Go build, but hand-written memory
  management around CoreFoundation for no gain at our sampling rate.

## Decision
The whole binary builds with `CGO_ENABLED=0`. SQLite is `modernc.org/sqlite`.
Anything macOS-specific is read by running a system command and parsing its
output ([ADR-0002](./0002-battery-via-ioreg-plist.md),
[ADR-0003](./0003-app-energy-from-top-without-sudo.md)).

## Consequences
+ `go build` works on any machine; goreleaser makes both Mac builds from one
  Linux job; contributors do not need Xcode.
+ Tests use captured command output as fixtures and run on Linux CI too.
- SQLite in Go is roughly 2× slower than C SQLite. At ~1 500 rows a day this
  is invisible.
- We depend on `ioreg`, `top` and `pmset` existing. They have shipped with
  every macOS release; the risk is accepted.
- If batlog ever needs sub-second sampling or instant plug/unplug events, the
  answer is purego over IOKit — a new ADR, not a return to cgo.
