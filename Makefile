# OMABUDGET ships source only: bin/ is not committed, so the plugin is built
# once after install. Requires the Go toolchain.

VERSION ?= 0.1.0
LDFLAGS := -X main.version=$(VERSION)
BINARIES := omabudgetd omabudget

# Every tool is named through a variable, and the one that only ever does one
# thing is named absolutely, so a build does not depend on what PATH resolves
# it to. A packager can override any of them.
GO      ?= go
INSTALL ?= /usr/bin/install

# Pin the compiler. Without this the go command will fetch a different
# toolchain over the network to satisfy the directive in go.mod; with it the
# build uses the toolchain that is installed, or fails and says so.
export GOTOOLCHAIN = local

# Go turns cgo on whenever it finds a C compiler and off when it does not, so
# the same source silently produces different binaries on different machines.
# Pin it off: the SQLite driver is pure Go and nothing here needs C.
export CGO_ENABLED = 0

# readonly refuses to edit go.mod or go.sum during a build, so every module is
# the one the committed checksums name. trimpath keeps the output free of local
# paths, and buildvcs=false keeps the git revision out, so the bytes depend on
# the source and the toolchain and nothing else.
BUILDFLAGS := -trimpath -mod=readonly -buildvcs=false

PLUGIN_DIR ?= $(HOME)/.config/omarchy/plugins/karamble.omabudget
PLUGIN_FILES := manifest.json App.qml BarWidget.qml Service.qml README.md LICENSE
PLUGIN_DIRS := views charts components
# preview.png is generated, so it is copied only when it exists.
PREVIEW := $(wildcard preview.png)

.PHONY: all build test validate verify clean install install-check

all: build

build: verify
	@$(INSTALL) -d bin
	@for b in $(BINARIES); do \
		echo "building bin/$$b"; \
		$(GO) build $(BUILDFLAGS) -ldflags "$(LDFLAGS)" -o bin/$$b ./cmd/$$b || exit 1; \
	done
	@echo
	@echo "built. next:"
	@echo "  ./bin/omabudget health"

# The race detector needs cgo, so the tests turn it back on for themselves.
test:
	CGO_ENABLED=1 $(GO) test -race ./...

# The gate every change passes before it lands: vet, then the race suite.
validate:
	$(GO) vet ./...
	CGO_ENABLED=1 $(GO) test -race ./...

# Omarchy refuses symlinks inside a plugin folder, so installing copies.
install: build
	@$(INSTALL) -d "$(PLUGIN_DIR)/bin"
	@for d in $(PLUGIN_DIRS); do \
		$(INSTALL) -d "$(PLUGIN_DIR)/$$d"; \
		ls $$d/*.qml >/dev/null 2>&1 && $(INSTALL) -m 0644 $$d/*.qml "$(PLUGIN_DIR)/$$d/"; \
	done
	@$(INSTALL) -m 0644 $(PLUGIN_FILES) $(PREVIEW) "$(PLUGIN_DIR)/"
	@# install writes through a fresh inode, so a running daemon holding the old
	@# binary open does not block the replacement, which plain cp would.
	@$(INSTALL) -m 0755 bin/* "$(PLUGIN_DIR)/bin/"
	@echo "installed to $(PLUGIN_DIR)"
	@echo "enable it with: omarchy plugin enable karamble.omabudget right"

clean:
	rm -rf bin

# Check every module against the committed checksums before anything compiles.
verify:
	@$(GO) mod verify >/dev/null || { echo "module verification failed"; exit 1; }

install-check: verify
	@command -v $(GO) >/dev/null || { echo "go toolchain not found"; exit 1; }
	@echo "toolchain ok, modules verified"
