VERSION ?= $(shell git describe --tags --exact-match 2>/dev/null || printf "dev-%s" $$(git rev-parse --short HEAD 2>/dev/null || echo unknown))
LDFLAGS := -ldflags "-X github.com/walkindude/gosymdb/internal/cmd.Version=$(VERSION)"

.PHONY: build build-cgo test testbench lint clean

build:
	go build $(LDFLAGS) -o gosymdb .

build-cgo:
	CGO_ENABLED=1 go build $(LDFLAGS) -tags cgo -o gosymdb .

test:
	go test ./internal/cmd/... ./indexer/... ./store/... -count=1

testbench:
	go test ./internal/cmd/... -run TestBench -count=1

lint:
	golangci-lint run
	@echo "Lint: OK"

clean:
	rm -f gosymdb gosymdb-* *.sqlite
