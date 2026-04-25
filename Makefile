.PHONY: build test test-integration test-race test-asan conformance lint generate fmt docker clean

BIN ?= bin/triplet
PKG ?= ./...

build:
	BIN_PATH=$(BIN) ./scripts/build.sh

test: build
	./scripts/test.sh $(PKG)

test-integration: build
	./scripts/test-integration.sh $(PKG)

test-race:
	GO_TEST_FLAGS='-v -race -count=1' ./scripts/test.sh $(PKG)

test-asan:
	GO_TEST_FLAGS='-v -asan -count=1' ./scripts/test.sh $(PKG)

conformance:
	./scripts/conformance.sh

lint:
	golangci-lint run $(PKG)

fmt:
	gofmt -w .
	goimports -w .

generate: fmt

docker:
	docker build -t triplet:dev .

clean:
	rm -rf bin coverage.out
