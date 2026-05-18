GO ?= go
CGO_ENABLED ?= 1
GO_TAGS ?= sqlite_fts5
BIN ?= dist/loop
GORELEASER ?= goreleaser

.PHONY: test build verify install replace-local ci release-check release-snapshot release clean

test:
	CGO_ENABLED=$(CGO_ENABLED) $(GO) test -tags $(GO_TAGS) ./...

build:
	mkdir -p $(dir $(BIN))
	CGO_ENABLED=$(CGO_ENABLED) $(GO) build -tags $(GO_TAGS) -trimpath -o $(BIN) ./cmd/loop

verify: build
	./$(BIN) version

install:
	CGO_ENABLED=$(CGO_ENABLED) $(GO) install -tags $(GO_TAGS) ./cmd/loop

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
