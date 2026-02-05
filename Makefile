.PHONY: build build-mac build-linux install clean test deploy-linux

# macOS binaries
MAC_DIR=bin/mac
MAC_BINARY_GO=$(MAC_DIR)/ezs-go
MAC_WRAPPER=$(MAC_DIR)/ezs

# Linux binaries
LINUX_DIR=bin/linux
LINUX_BINARY_GO=$(LINUX_DIR)/ezs-go
LINUX_WRAPPER=$(LINUX_DIR)/ezs

INSTALL_PATH=$(HOME)/bin

build: build-mac build-linux

build-mac:
	mkdir -p $(MAC_DIR)
	GOOS=darwin GOARCH=arm64 go build -o $(MAC_BINARY_GO) ./cmd/ezs
	cp scripts/ezs-wrapper.sh $(MAC_WRAPPER)
	chmod +x $(MAC_WRAPPER)

build-linux:
	mkdir -p $(LINUX_DIR)
	GOOS=linux GOARCH=amd64 go build -o $(LINUX_BINARY_GO) ./cmd/ezs
	cp scripts/ezs-wrapper.sh $(LINUX_WRAPPER)
	chmod +x $(LINUX_WRAPPER)

install: build
	mkdir -p $(INSTALL_PATH)
	cp $(MAC_BINARY_GO) $(INSTALL_PATH)/ezs-go
	cp $(MAC_WRAPPER) $(INSTALL_PATH)/ezs
	@echo "Installed ezs and ezs-go to $(INSTALL_PATH)"
	@echo "Make sure $(INSTALL_PATH) is in your PATH"

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

