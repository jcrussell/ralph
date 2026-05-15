BIN := ralph
PKG := ./cmd/ralph

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X github.com/jcrussell/ralph/pkg/cmd/version.Version=$(VERSION) \
	-X github.com/jcrussell/ralph/pkg/cmd/version.Commit=$(COMMIT) \
	-X github.com/jcrussell/ralph/pkg/cmd/version.Date=$(DATE)

export CGO_ENABLED := 0

.PHONY: build
build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) $(PKG)

.PHONY: install
install:
	go install -ldflags "$(LDFLAGS)" $(PKG)

.PHONY: test
test:
	go test ./...

.PHONY: vet
vet:
	go vet ./...

.PHONY: lint
lint:
	golangci-lint run ./...

.PHONY: tidy
tidy:
	go mod tidy

.PHONY: clean
clean:
	rm -f $(BIN)
