.PHONY: build build-linux install clean test

BINARY_GO=bin/ezs-go
BINARY_LINUX=bin/ezs-go-linux
WRAPPER=bin/ezs
WRAPPER_LINUX=bin/ezs-linux
INSTALL_PATH=$(HOME)/bin

build:
	mkdir -p bin
	go build -o $(BINARY_GO) ./cmd/ezs
	cp scripts/ezs-wrapper.sh $(WRAPPER)
	chmod +x $(WRAPPER)

build-linux:
	mkdir -p bin
	GOOS=linux GOARCH=amd64 go build -o $(BINARY_LINUX) ./cmd/ezs
	cp scripts/ezs-wrapper.sh $(WRAPPER_LINUX)
	chmod +x $(WRAPPER_LINUX)
	@echo "Built Linux binaries: $(BINARY_LINUX) and $(WRAPPER_LINUX)"

install: build
	mkdir -p $(INSTALL_PATH)
	cp $(BINARY_GO) $(INSTALL_PATH)/ezs-go
	cp $(WRAPPER) $(INSTALL_PATH)/ezs
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

