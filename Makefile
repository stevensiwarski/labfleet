.PHONY: build test lint fmt

GO ?= go

build:
	$(GO) build -o bin/fleetctl ./cmd/fleetctl
	$(GO) build -o bin/node-doctor ./cmd/node-doctor

test:
	$(GO) test ./...

lint:
	@files=$$(gofmt -l .) || exit 1; \
	 test -z "$$files" || { printf 'Go files need formatting; run make fmt\n%s\n' "$$files"; exit 1; }
	$(GO) vet ./...

fmt:
	$(GO) fmt ./...
