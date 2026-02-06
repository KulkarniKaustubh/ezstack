.PHONY: build build-mac build-linux install-mac install-linux clean test

# macOS binary
MAC_DIR=bin/mac
MAC_BINARY=$(MAC_DIR)/ezs

# Linux binary
LINUX_DIR=bin/linux
LINUX_BINARY=$(LINUX_DIR)/ezs

# XDG convention
INSTALL_PATH=$(HOME)/.local/bin

build: build-mac build-linux

build-mac:
	mkdir -p $(MAC_DIR)
	GOOS=darwin GOARCH=arm64 go build -o $(MAC_BINARY) ./cmd/ezs

build-linux:
	mkdir -p $(LINUX_DIR)
	GOOS=linux GOARCH=amd64 go build -o $(LINUX_BINARY) ./cmd/ezs

install-mac: build-mac
	mkdir -p $(INSTALL_PATH)
	cp $(MAC_BINARY) $(INSTALL_PATH)/ezs
	@echo "Installed ezs to $(INSTALL_PATH)"
	@echo "Make sure $(INSTALL_PATH) is in your PATH"
	@echo "Add to your shell config: eval \"\$$(ezs --shell-init)\""

install-linux: build-linux
	mkdir -p $(INSTALL_PATH)
	cp $(LINUX_BINARY) $(INSTALL_PATH)/ezs
	@echo "Installed ezs to $(INSTALL_PATH)"
	@echo "Make sure $(INSTALL_PATH) is in your PATH"
	@echo "Add to your shell config: eval \"\$$(ezs --shell-init)\""

clean:
	rm -rf bin
	rm -rf test/testrepo test/worktrees

test: build
	./test/test_ezstack.sh

# Run go fmt
fmt:
	go fmt ./...

# Run go vet
vet:
	go vet ./...

