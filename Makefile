define USAGE
USAGE:
> make [
	test: run all unit tests
	test-unit: run all unit tests
	lint: run golangci-linter

	clean: clean test cache and other deps

	help: print this help message
]
endef
export USAGE

SHELL		:= /bin/bash

BIN	:= $(PWD)/bin
GO	:= go
GOTEST := $(BIN)/gotest
LINT := $(BIN)/golangci-lint

export PATH := $(PATH):$(BIN)

help:
	@echo "$$USAGE"

usage:
	@echo "$$USAGE"

sense:
	@echo "$$USAGE"

install-dep-tools:
	GOBIN=$(BIN) $(GO) get -u github.com/rakyll/gotest
	@make install-golangci-lint

install-golangci-lint:
	# binary will be $(BIN)
	curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(BIN) v1.40.0

test: test-unit

test-unit: install-dep-tools testclean
	$(GOTEST) ./... -v -race

testclean:
	$(GO) clean -testcache || true

clean: testclean
	rm -rf $(BIN)/*
	$(GO) clean -cache
	$(GO) clean -modcache

lint: install-dep-tools
	$(LINT) run --config golangci.yml ./...

ci: test lint
