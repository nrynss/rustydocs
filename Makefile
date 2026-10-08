# Makefile for rustydocs

# Version from git tag, commit, and date
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE    ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

# Build flags
LDFLAGS := -ldflags "-s -w \
	-X main.version=$(VERSION) \
	-X main.commit=$(COMMIT) \
	-X main.date=$(DATE)"

# Output binary
BINARY := rustydocs

.PHONY: all build clean install test

all: build

build:
	go build $(LDFLAGS) -o $(BINARY) ./cmd/rustydocs

install:
	go install $(LDFLAGS) ./cmd/rustydocs

clean:
	rm -f $(BINARY)
	rm -rf reports/

test:
	go test -v ./...

# Build archives and local container images without publishing.
# Requires GoReleaser v2.18.2 and Docker Buildx with amd64/arm64 support.
GORELEASER ?= goreleaser
.PHONY: release

release:
	$(GORELEASER) release --snapshot --clean
