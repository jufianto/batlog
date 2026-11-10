.PHONY: test brand-check

# Everything CI runs, in one go. CGO stays off (ADR-0001).
test: brand-check
	CGO_ENABLED=0 go vet ./...
	CGO_ENABLED=0 go test ./...
	GOOS=linux CGO_ENABLED=0 go vet ./...

# One palette source (brand/tokens.json): fails on colour drift, on a colour with no token, on a
# mapped name that disappears, and on personal paths in brand text; then the check's own mutation
# tests. Standard library Go only. docs/reference/brand.md explains each failure and its fix.
brand-check:
	CGO_ENABLED=0 go run ./brand/check
	CGO_ENABLED=0 go test ./brand/check
