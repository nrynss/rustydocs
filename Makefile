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

# Release builds for multiple platforms
.PHONY: release release-linux release-darwin release-windows

release: release-linux release-darwin release-windows

release-linux:
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o dist/$(BINARY)-linux-amd64 ./cmd/rustydocs
	GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o dist/$(BINARY)-linux-arm64 ./cmd/rustydocs

release-darwin:
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o dist/$(BINARY)-darwin-amd64 ./cmd/rustydocs
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o dist/$(BINARY)-darwin-arm64 ./cmd/rustydocs

release-windows:
	GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o dist/$(BINARY)-windows-amd64.exe ./cmd/rustydocs
