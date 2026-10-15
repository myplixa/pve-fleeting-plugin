.DEFAULT_GOAL := build

export NAME ?= fleeting-plugin-proxmox
export VERSION := v$(shell cat VERSION)
export OUT_PATH ?= out
export CGO_ENABLED ?= 0

export CHECKSUMS_FILE_NAME := release.sha256
export CHECKSUMS_FILE := $(OUT_PATH)/$(CHECKSUMS_FILE_NAME)

REVISION := $(shell git rev-parse --short=8 HEAD || echo unknown)
REFERENCE := $(shell git show-ref | grep "$(REVISION)" | grep -v HEAD | awk '{print $$2}' | sed 's|refs/remotes/origin/||' | sed 's|refs/heads/||' | sort | head -n 1)
BUILT := $(shell date -u +%Y-%m-%dT%H:%M:%S%z)
PKG = $(shell go list ./cmd/$(NAME)/plugin)

OS_ARCHS ?= darwin/amd64 darwin/arm64 \
			freebsd/amd64 freebsd/arm64 freebsd/386 freebsd/arm \
			linux/amd64 linux/arm64 linux/arm linux/s390x linux/ppc64le linux/386 \
			windows/amd64.exe windows/386.exe
GO_LDFLAGS ?= -X $(PKG).NAME=$(NAME) -X $(PKG).VERSION=$(VERSION) \
              -X $(PKG).REVISION=$(REVISION) -X $(PKG).BUILT=$(BUILT) \
              -X $(PKG).REFERENCE=$(REFERENCE) \
              -w -extldflags '-static'

build:
	@mkdir -p $(OUT_PATH)
	go build -a -ldflags "$(GO_LDFLAGS)" -o $(OUT_PATH)/$(NAME) ./cmd/$(NAME)
.PHONY: build

TARGETS = $(foreach OSARCH,$(OS_ARCHS),${OUT_PATH}/$(NAME)-$(subst /,-,$(OSARCH)))

$(TARGETS):
	@mkdir -p $(OUT_PATH)
	GOOS=$(firstword $(subst -, ,$(subst $(OUT_PATH)/$(NAME)-,,$@))) \
			 GOARCH=$(lastword $(subst .exe,,$(subst -, ,$(subst $(OUT_PATH)/$(NAME)-,,$@)))) \
			 go build -a -ldflags "$(GO_LDFLAGS)" -o $@ ./cmd/$(NAME)

MAKEFLAGS += -j$(shell nproc 2>/dev/null || sysctl -n hw.ncpu)
all: $(TARGETS)
.PHONY: all

.PHONY: test
test: unit-test integration-test

.PHONY: unit-test
unit-test:
	go test -v ./cmd/...

.PHONY: integration-test
integration-test: build
	go test -v $(shell go list ./test/integration) \
		-timeout 30m \
		-plugin-binary-path="$(PWD)/$(OUT_PATH)/$(NAME)" \
		-config-path="$(PWD)/config.json"

.PHONY: lint
lint:
	golangci-lint run -v

.PHONY: checksums
checksums:
	cd $(OUT_PATH) && sha256sum $(NAME)-* > $(CHECKSUMS_FILE_NAME)

.PHONY: clean
clean:
	rm -fr $(OUT_PATH)
