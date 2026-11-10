.PHONY: test brand-check brand-kit brand-kit-check brand-kit-deps

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

# The brand kit (brand/kit): SVGs, PNGs, icons, review sheets, guide PDF and manifest, built from
# brand/tokens.json, brand/kit/source/geometry.py, the fonts and the supplied JPEGs. Pinned Python
# packages in a local venv and @resvg/resvg-js from the lockfile; needs python3 (3.11+) and npm.
# brand-kit-check rebuilds into a temporary copy and fails unless every file comes out the same.
BRAND_SRC := brand/kit/source
brand-kit-deps:
	@test -x $(BRAND_SRC)/.venv/bin/python || python3 -m venv $(BRAND_SRC)/.venv
	@$(BRAND_SRC)/.venv/bin/pip install -q --disable-pip-version-check -r $(BRAND_SRC)/requirements.txt
	@npm ci --prefix $(BRAND_SRC) --silent --no-audit --no-fund

brand-kit: brand-kit-deps
	$(BRAND_SRC)/.venv/bin/python $(BRAND_SRC)/build_kit.py
	$(BRAND_SRC)/.venv/bin/python $(BRAND_SRC)/verify_kit.py

brand-kit-check: brand-kit-deps
	$(BRAND_SRC)/.venv/bin/python $(BRAND_SRC)/build_kit.py --check
	$(BRAND_SRC)/.venv/bin/python $(BRAND_SRC)/verify_kit.py --check
