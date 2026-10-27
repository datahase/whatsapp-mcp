.PHONY: build test test-race e2e lint vet fmt vuln parity licenses image image-multi smoke clean

SHELL := /bin/bash
BIN := ./bin/whatsapp-mcp
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
IMAGE ?= datahase/whatsapp-mcp:local

build:
	mkdir -p bin
	go build -ldflags "-X main.Version=$(VERSION)" -o $(BIN) ./cmd/whatsapp-mcp

test:
	go test ./...

test-race:
	go test -race ./...

e2e:
	go test -tags=e2e -count=1 ./e2e/...

vet:
	go vet ./... && go vet -tags=e2e ./e2e/...

fmt:
	@diff -u <(echo -n) <(gofmt -l cmd internal e2e) || (echo "gofmt differences found"; exit 1)

lint: fmt vet
	staticcheck ./...

vuln:
	govulncheck ./...

parity:
	bash scripts/mdtest-parity.sh

licenses:
	bash scripts/third-party-licenses.sh

# Image for the local Docker architecture.
image:
	docker build --build-arg VERSION=$(VERSION) --build-arg REVISION=$$(git rev-parse HEAD 2>/dev/null || echo unknown) -t $(IMAGE) .

# Both architectures, kept in the local build cache (nothing is pushed).
image-multi:
	docker buildx build --platform linux/amd64,linux/arm64 --build-arg VERSION=$(VERSION) -t $(IMAGE) .

smoke: image
	bash scripts/container-smoke.sh $(IMAGE)

clean:
	rm -rf bin cover.out
