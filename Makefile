.PHONY: build build-mcp build-all install install-mcp install-all clean test fmt fmt-check vet docs ci

# XDG convention
INSTALL_PATH=$(HOME)/.local/bin

build:
	go build -o bin/ezs ./cmd/ezs

build-mcp:
	go build -o bin/ezs-mcp ./cmd/ezs-mcp

build-all: build build-mcp

install:
	mkdir -p $(INSTALL_PATH)
	go build -o $(INSTALL_PATH)/ezs ./cmd/ezs
	@echo "Installed ezs to $(INSTALL_PATH)"
	@echo "Make sure $(INSTALL_PATH) is in your PATH"
	@echo "Add to your shell config: eval \"\$$(ezs --shell-init)\""

install-mcp:
	mkdir -p $(INSTALL_PATH)
	go build -o $(INSTALL_PATH)/ezs-mcp ./cmd/ezs-mcp
	@echo "Installed ezs-mcp to $(INSTALL_PATH)"
	@echo "Register with Claude Code:"
	@echo "  claude mcp add ezstack -- $(INSTALL_PATH)/ezs-mcp --repo \$$(pwd)"

install-all: install install-mcp

clean:
	rm -rf bin
	rm -rf test/testrepo test/worktrees

test:
	go test -v ./...

fmt:
	gofmt -w .

# Same check CI runs — non-zero exit if any file is not formatted, with the
# offending file list on stdout. Mirrors the workflow's "Format check" step
# so `make ci` catches formatting drift before a commit / tag goes out.
fmt-check:
	@output=$$(gofmt -l .); \
	if [ -n "$$output" ]; then \
	  echo "Files not formatted:"; \
	  echo "$$output"; \
	  exit 1; \
	fi

vet:
	go vet ./...

# `make ci` mirrors the .github/workflows/ci.yml `go` job exactly: format
# check, vet, race-tests. Run this before tagging a release — the release
# pipeline now also runs it as a gating preflight, but local invocation
# saves a CI round-trip.
ci: fmt-check vet
	go test -race -count=1 ./...

docs:
	go run ./cmd/docgen -w
	go run ./cmd/docsgen
