## What and why

<!-- What does this change, and which spec, ADR or issue does it serve? -->

## Checklist

- [ ] `CGO_ENABLED=0 go vet ./...` and `CGO_ENABLED=0 go test ./...` pass
- [ ] Behaviour changes are reflected in the command's spec in `docs/specs/`
- [ ] New `--json` fields are documented in the spec
- [ ] Device captures in `testdata/` have serial numbers removed
