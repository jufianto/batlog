# Contributing

## How the docs work

Three kinds of document, each with a strict length:

| Folder | What | Length |
|---|---|---|
| `docs/prd/` | Why a feature exists, for whom, what "done" means | one page |
| `docs/adr/` | Why we chose X over Y, and what we gave up | under one page, append-only |
| `docs/specs/` | Exactly how one command behaves, with edge cases | 1–2 pages, written just before building |

A new command that serves an existing PRD gets a spec. A new *problem* gets a
PRD. A hard-to-reverse technical choice gets an ADR; an easy one gets a code
comment.

## The most useful thing you can contribute right now

batlog reads the battery through `ioreg`, and the keys differ between Mac
models. If you have an Intel Mac, or a macOS version we have not tested, run:

```sh
ioreg -rc AppleSmartBattery -a > "batt-$(sysctl -n hw.model)-$(sw_vers -productVersion).plist"
```

and attach the file to an issue. It becomes a test fixture, and batlog is then
verified against your model forever. The file contains your battery's serial
number — remove the `Serial` lines first if you would rather not share them.

## Build and test

Not yet — see the status note in the README.
