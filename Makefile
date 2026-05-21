GO ?= go
CGO_ENABLED ?= 0
GO_TAGS ?=
GO_TAG_FLAGS := $(if $(strip $(GO_TAGS)),-tags $(GO_TAGS),)
BIN ?= dist/loop
GORELEASER ?= goreleaser

.PHONY: test build verify install replace-local ci release-check release-snapshot release clean

test:
	CGO_ENABLED=$(CGO_ENABLED) $(GO) test $(GO_TAG_FLAGS) ./...

build:
	mkdir -p $(dir $(BIN))
	CGO_ENABLED=$(CGO_ENABLED) $(GO) build $(GO_TAG_FLAGS) -trimpath -o $(BIN) ./cmd/loop

verify: build
	./$(BIN) version

install:
	CGO_ENABLED=$(CGO_ENABLED) $(GO) install $(GO_TAG_FLAGS) ./cmd/loop

replace-local: install
	@command -v loop
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
