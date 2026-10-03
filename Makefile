# Visualible — single-binary build. No Node.js, no codegen required.

BINARY  := visualible
PKG     := ./cmd/visualible
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: all build generate test vet fmt check integration run run-verbose clean

all: check build

## build: compile the single executable (frontend embedded via go:embed)
build:
	go build -ldflags "-X main.version=$(VERSION)" -o $(BINARY) $(PKG)

## generate: run code generation (currently a no-op placeholder for the
## documented workflow; the frontend needs no build step)
generate:
	go generate ./...

## test: unit tests — no network or Ansible required
test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

## check: fmt + vet + test
check: fmt vet test

## integration: sweep the real local Ansible through normalization
## (requires ansible-doc on PATH; scope with VISUALIBLE_IT_PREFIX/MAX)
integration:
	go test -tags integration ./internal/ansible/ -run Integration -v

## run: build and start the server
run: build
	./$(BINARY)

## run-verbose: build and start with DEBUG logging on
run-verbose: build
	./$(BINARY) -verbose

clean:
	rm -f $(BINARY)
