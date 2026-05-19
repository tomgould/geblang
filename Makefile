.PHONY: test test-go test-lang check-lang build build-with-path install bench bench-docker run repl check doctor cache-stats clean fmt docs docker-build compose-build vscode-build vscode-install vscode-install-wsl vscode-install-native

BINARY ?= geblang
GO ?= go
GOCACHE ?= /tmp/geblang-go-cache
DOCS_SRC ?= docs/user
DOCS_OUT ?= docs/site
DOCS_API_SRC ?= stdlib
DOCS_EXAMPLES_SRC ?= docs/examples
DOCKER_IMAGE ?= geblang-build
DOCKER_CONTAINER ?= geblang-build-artifacts
VSCODE_IMAGE ?= geblang-vscode-artifacts
VSCODE_CONTAINER ?= geblang-vscode-build-artifacts

test: test-go test-lang

test-go:
	GOCACHE=$(GOCACHE) GOTOOLCHAIN=auto $(GO) test ./...

# test-lang runs the Geblang-level regression suite under tests/.
# It depends on the geblang binary being built (target: build).
test-lang: build
	./$(BINARY) test tests/

# check-lang statically checks every .gb file under tests/. The tests/check
# subdirectory contains intentionally invalid files that must each emit at
# least one diagnostic; the script verifies that and lets `geblang check`
# pass clean over the rest.
check-lang: build
	@./scripts/check-lang.sh

build:
	GOCACHE=$(GOCACHE) GOTOOLCHAIN=auto $(GO) build -o $(BINARY) ./cmd/geblang

# Build and install the binary into INSTALL_DIR (defaults to /usr/local/bin).
# Override with `make build-with-path INSTALL_DIR=~/.local/bin`. Uses sudo
# when INSTALL_DIR is not writable by the current user.
INSTALL_DIR ?= /usr/local/bin

build-with-path: build
	@if [ -w "$(INSTALL_DIR)" ]; then \
		install -m 0755 $(BINARY) $(INSTALL_DIR)/$(BINARY); \
		echo "installed $(INSTALL_DIR)/$(BINARY)"; \
	else \
		echo "installing into $(INSTALL_DIR) (requires sudo)"; \
		sudo install -m 0755 $(BINARY) $(INSTALL_DIR)/$(BINARY); \
		echo "installed $(INSTALL_DIR)/$(BINARY)"; \
	fi

# Alias of build-with-path for muscle memory.
install: build-with-path

bench:
	GOCACHE=$(GOCACHE) GOTOOLCHAIN=auto ./benchmarks/run.sh

bench-docker:
	GOCACHE=$(GOCACHE) GOTOOLCHAIN=auto ./benchmarks/run.sh --docker

docs:
	GOCACHE=$(GOCACHE) GOTOOLCHAIN=auto $(GO) run ./cmd/docsite $(DOCS_SRC) $(DOCS_OUT) $(DOCS_API_SRC) --examples $(DOCS_EXAMPLES_SRC)

docker-build:
	rm -rf build
	mkdir -p build
	docker build -t $(DOCKER_IMAGE) .
	-docker rm -f $(DOCKER_CONTAINER) 2>/dev/null
	docker create --name $(DOCKER_CONTAINER) $(DOCKER_IMAGE)
	docker cp $(DOCKER_CONTAINER):/out/. build/
	docker rm -f $(DOCKER_CONTAINER)

compose-build:
	rm -rf build
	mkdir -p build build/vscode/out build/vscode/vsix
	docker compose build
	-docker rm -f $(DOCKER_CONTAINER) $(VSCODE_CONTAINER) 2>/dev/null
	docker create --name $(DOCKER_CONTAINER) geblang-artifacts
	docker cp $(DOCKER_CONTAINER):/out/. build/
	docker rm -f $(DOCKER_CONTAINER)
	docker create --name $(VSCODE_CONTAINER) geblang-vscode-artifacts
	docker cp $(VSCODE_CONTAINER):/out/. build/vscode/out/
	docker cp $(VSCODE_CONTAINER):/vsix/. build/vscode/vsix/
	docker rm -f $(VSCODE_CONTAINER)

vscode-build:
	mkdir -p build/vscode/out build/vscode/vsix
	docker compose build vscode-ext
	-docker rm -f $(VSCODE_CONTAINER) 2>/dev/null
	docker create --name $(VSCODE_CONTAINER) geblang-vscode-artifacts
	docker cp $(VSCODE_CONTAINER):/out/. build/vscode/out/
	docker cp $(VSCODE_CONTAINER):/vsix/. build/vscode/vsix/
	docker rm -f $(VSCODE_CONTAINER)

vscode-install-wsl:
	@VSIX_SRC=build/vscode/vsix/geblang.vsix; \
	VSIX_TMP=/mnt/c/Windows/Temp/geblang.vsix; \
	cp "$$VSIX_SRC" "$$VSIX_TMP" && \
	code --install-extension "$$(wslpath -w $$VSIX_TMP)"

vscode-install-native:
	code --install-extension build/vscode/vsix/geblang.vsix

vscode-install:
	@if [ -n "$$WSL_DISTRO_NAME" ]; then \
		$(MAKE) vscode-install-wsl; \
	else \
		$(MAKE) vscode-install-native; \
	fi

run repl:
	GOCACHE=$(GOCACHE) GOTOOLCHAIN=auto $(GO) run ./cmd/geblang repl

check:
	GOCACHE=$(GOCACHE) GOTOOLCHAIN=auto $(GO) run ./cmd/geblang check .

doctor:
	GOCACHE=$(GOCACHE) GOTOOLCHAIN=auto $(GO) run ./cmd/geblang doctor

cache-stats:
	GOCACHE=$(GOCACHE) GOTOOLCHAIN=auto $(GO) run ./cmd/geblang cache stats

clean:
	rm -f $(BINARY)
	rm -rf build
	GOCACHE=$(GOCACHE) GOTOOLCHAIN=auto $(GO) run ./cmd/geblang cache clean

fmt:
	gofmt -w cmd internal
