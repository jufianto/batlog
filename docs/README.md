# batlog — documentation

| Folder | Contents |
|---|---|
| [prd/](./prd/) | Product requirements — one page per problem we solve |
| [adr/](./adr/) | Architecture decision records — why we chose X over Y |
| [specs/](./specs/) | Functional specs — one per command, the exact behaviour |

**Reading order for a newcomer:** [prd/tracker.md](./prd/tracker.md) →
[adr/README.md](./adr/README.md) → the spec for whichever command you touch.

**Build order (v1):** F1 status → F2 health → F5 daemon → F3 history → F4 top
→ F6 report → F7 export. The daemon comes third because every day it is not
running is history we never get back.

**Shared rules for every command:**
- Timestamps stored as UTC unix seconds; rendered in local time.
- A gap of more than 90 s between samples means the Mac was asleep (or the
  daemon was down). Sleep is excluded from every duration and rate.
- `--json` everywhere. Schemas are stable and documented in the spec. A value
  we cannot compute is `null`, never a guess.
- Commands degrade per field, never crash on partial data. A parse error
  names the probe that failed (`ioreg`, `top`, `pmset`).
- Exit codes: 0 success (including "no data yet"), 1 operational error,
  2 usage error.
