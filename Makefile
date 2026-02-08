.PHONY: build install clean test fmt vet

# XDG convention
INSTALL_PATH=$(HOME)/.local/bin

build:
	go build -o bin/ezs ./cmd/ezs

install:
	mkdir -p $(INSTALL_PATH)
	go build -o $(INSTALL_PATH)/ezs ./cmd/ezs
	@echo "Installed ezs to $(INSTALL_PATH)"
	@echo "Make sure $(INSTALL_PATH) is in your PATH"
	@echo "Add to your shell config: eval \"\$$(ezs --shell-init)\""

clean:
	rm -rf bin
	rm -rf test/testrepo test/worktrees

test:
	go test -v ./...

fmt:
	go fmt -w ./...

vet:
	go vet ./...

