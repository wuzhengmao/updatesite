BINARY := updatesite
IMAGE  ?= mti/updatesite
TAG    ?= $(shell date -u +%Y%m%d)
VERSION := $(TAG)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X github.com/mti/updatesite/internal/buildinfo.Version=$(VERSION) \
	-X github.com/mti/updatesite/internal/buildinfo.Commit=$(COMMIT) \
	-X github.com/mti/updatesite/internal/buildinfo.Date=$(DATE)

.PHONY: help run build test vet fmt image push clean

help:
	@echo "make run     - run locally against ./examples/apps"
	@echo "make build   - build the binary into ./dist"
	@echo "make test    - run the test suite"
	@echo "make image   - build the amd64/arm64 image (no push)"
	@echo "make push    - build and push the multi-arch image"

run:
	DATA_DIR=./examples CACHE_DIR=./cache ADDR=:8080 SCAN_INTERVAL=5s \
		go run -ldflags "$(LDFLAGS)" ./cmd/updatesite

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY) ./cmd/updatesite

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w .

image:
	IMAGE=$(IMAGE) TAG=$(TAG) ./scripts/build.sh

push:
	PUSH=1 IMAGE=$(IMAGE) TAG=$(TAG) ./scripts/build.sh

clean:
	rm -rf dist cache
