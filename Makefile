# managents — developer entry points. Run `make help` for the list.

PIO      ?= pio
OPENSCAD ?= openscad
OPENSCAD_FLAGS ?= --backend=manifold
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
PORT     ?=

CPP_SOURCES = $(shell find firmware/src firmware/lib firmware/test -name '*.cpp' -o -name '*.hpp')
STL_PARTS   = body bezel fit_test

.DEFAULT_GOAL := help
.PHONY: help test test-helper test-firmware lint lint-helper format format-check \
        build helper firmware flash monitor run enclosure clean

help: ## Show this help
	@awk 'BEGIN {FS = ":.*## "} /^[a-z-]+:.*## / {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

test: test-helper test-firmware ## Run every test that needs no hardware

test-helper: ## Helper unit and contract tests
	cd helper && go test -race ./...

test-firmware: ## Firmware core unit and contract tests, on the host
	cd firmware && $(PIO) test -e native

lint: lint-helper format-check ## Static checks for helper and firmware

lint-helper:
	cd helper && test -z "$$(gofmt -l .)" || (gofmt -l . && false)
	cd helper && go vet ./...
	cd helper && go tool staticcheck ./...

format: ## Format all sources in place
	cd helper && gofmt -w .
	clang-format -i $(CPP_SOURCES)

format-check:
	clang-format --dry-run --Werror $(CPP_SOURCES)

build: helper firmware ## Build the helper binary and the firmware image

helper: ## Build bin/managents
	cd helper && go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o ../bin/managents ./cmd/managents

firmware: ## Build the firmware image
	cd firmware && $(PIO) run -e e32r40t

flash: ## Build and flash the firmware (PORT=... to choose the serial port)
	cd firmware && $(PIO) run -e e32r40t -t upload $(if $(PORT),--upload-port $(PORT))

monitor: ## Open a serial monitor on the display
	cd firmware && $(PIO) device monitor $(if $(PORT),--port $(PORT))

run: helper ## Build and run the helper
	bin/managents run $(if $(PORT),--port $(PORT))

enclosure: ## Render the enclosure STLs (needs OpenSCAD)
	$(foreach part,$(STL_PARTS),$(OPENSCAD) $(OPENSCAD_FLAGS) -D 'part="$(part)"' \
		-o enclosure/stl/managents-$(part).stl enclosure/managents_case.scad &&) true

clean: ## Remove build outputs
	rm -rf bin firmware/.pio
