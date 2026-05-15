BIN := ralph
PKG := ./cmd/ralph

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X github.com/jcrussell/ralph/internal/ralphcmd/build.Version=$(VERSION) \
	-X github.com/jcrussell/ralph/internal/ralphcmd/build.Commit=$(COMMIT) \
	-X github.com/jcrussell/ralph/internal/ralphcmd/build.Date=$(DATE)

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

.PHONY: docs
docs:
	go generate ./pkg/cmd/root/

.PHONY: clean
clean:
	rm -f $(BIN)
