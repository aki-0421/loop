GO ?= go
CGO_ENABLED ?= 0
GO_TAGS ?=
GO_TAG_FLAGS := $(if $(strip $(GO_TAGS)),-tags $(GO_TAGS),)
BIN ?= dist/loop
INSTALLED_BIN := $(shell gobin="$$( $(GO) env GOBIN )"; if [ -n "$$gobin" ]; then printf "%s/loop" "$$gobin"; else printf "%s/bin/loop" "$$( $(GO) env GOPATH )"; fi)
GORELEASER ?= goreleaser
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse HEAD 2>/dev/null || echo none)
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS ?= -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

.PHONY: test build verify install replace-local ci release-check release-snapshot release clean

test:
	CGO_ENABLED=$(CGO_ENABLED) $(GO) test $(GO_TAG_FLAGS) ./...

build:
	mkdir -p $(dir $(BIN))
	CGO_ENABLED=$(CGO_ENABLED) $(GO) build $(GO_TAG_FLAGS) -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/loop

verify: build
	./$(BIN) version

install:
	CGO_ENABLED=$(CGO_ENABLED) $(GO) install $(GO_TAG_FLAGS) ./cmd/loop

replace-local: install
	@command -v loop
	@echo $(INSTALLED_BIN)
	@$(INSTALLED_BIN) version
	@loop version

ci: test build verify

release-check:
	$(GORELEASER) check

release-snapshot:
	$(GORELEASER) build --snapshot --clean --single-target

release:
	$(GORELEASER) release --clean

clean:
	rm -rf dist
