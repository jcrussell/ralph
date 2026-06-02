BIN := ralph
PKG := ./cmd/ralph

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

# Dev LDFLAGS keep symbols so delve / pprof / stack traces work.
# byob-release.6 strips -s -w via goreleaser for release builds only.
LDFLAGS := -X github.com/jcrussell/ralph/internal/ralphcmd/build.Version=$(VERSION) \
	-X github.com/jcrussell/ralph/internal/ralphcmd/build.Commit=$(COMMIT) \
	-X github.com/jcrussell/ralph/internal/ralphcmd/build.Date=$(DATE)

# Not named GOFLAGS — that is a reserved Go env var the toolchain
# would prepend to every `go` invocation, double-applying -trimpath.
GO_BUILD_FLAGS := -trimpath

# Single source of truth for the lint floor, shared with CI (.github/workflows/ci.yml
# reads the same file). byob-security.1: pinned, never `latest`.
GOLANGCI_VERSION := $(shell cat .golangci-version)

.PHONY: build
build:
	CGO_ENABLED=0 go build $(GO_BUILD_FLAGS) -ldflags "$(LDFLAGS)" -o bin/$(BIN) $(PKG)

.PHONY: install
install:
	CGO_ENABLED=0 go install $(GO_BUILD_FLAGS) -ldflags "$(LDFLAGS)" $(PKG)

.PHONY: test
test:
	go test -race ./...

.PHONY: vet
vet:
	go vet ./...

.PHONY: lint
lint:
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION) run ./...

.PHONY: vuln
vuln:
	go run golang.org/x/vuln/cmd/govulncheck@v1.3.0 ./...

.PHONY: tidy
tidy:
	go mod tidy

.PHONY: docs
docs:
	go generate ./pkg/cmd/root/

.PHONY: snapshot
snapshot:
	goreleaser build --snapshot --clean

.PHONY: release
release:
	goreleaser release --clean

.PHONY: clean
clean:
	rm -rf bin/ dist/
