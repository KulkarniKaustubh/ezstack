.PHONY: build build-mcp build-all install install-mcp install-all clean test fmt vet docs

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

vet:
	go vet ./...

docs:
	go run ./cmd/docgen -w
	go run ./cmd/docsgen
