# managents — developer entry points. Run `make help` for the list.

PIO      ?= pio
OPENSCAD ?= openscad
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
PORT     ?=

CPP_SOURCES = $(shell find firmware/src firmware/lib firmware/test -name '*.cpp' -o -name '*.hpp')
STL_PARTS   = body bezel fit_test

# OpenSCAD snapshots render far faster with the Manifold backend; the stable
# release (2021.01) has only CGAL and no --backend option.
OPENSCAD_HAS_BACKEND = $(shell $(OPENSCAD) --help 2>&1 | grep -q -- --backend && echo yes)
OPENSCAD_FLAGS ?= $(if $(OPENSCAD_HAS_BACKEND),--backend=manifold)
OPENSCAD_CGAL   = $(if $(OPENSCAD_HAS_BACKEND),--backend=cgal)
OPENSCAD_PNG    = $(OPENSCAD) $(OPENSCAD_FLAGS) --colorscheme="Nord Light" --projection=p

.DEFAULT_GOAL := help
.PHONY: help test test-helper test-firmware lint lint-helper format format-check \
        build helper firmware flash monitor run enclosure enclosure-check enclosure-images clean

help: ## Show this help
	@awk 'BEGIN {FS = ":.*## "} /^[a-z-]+:.*## / {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

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
	$(foreach part,$(STL_PARTS),$(OPENSCAD) $(OPENSCAD_FLAGS) --export-format=binstl -D 'part="$(part)"' \
		-o enclosure/stl/managents-$(part).stl enclosure/managents_case.scad &&) true

# CGAL counts the volumes of a render: one solid and the space around it make 2.
enclosure-check: ## Check that each printed part renders as one solid
	@tmp=$$(mktemp -d) && trap 'rm -rf "$$tmp"' EXIT && \
	for part in $(STL_PARTS); do \
		$(OPENSCAD) $(OPENSCAD_CGAL) -D "part=\"$$part\"" -o "$$tmp/$$part.stl" enclosure/managents_case.scad \
			>"$$tmp/log" 2>&1 || { cat "$$tmp/log"; exit 1; }; \
		volumes=$$(sed -n 's/^[[:space:]]*Volumes:[[:space:]]*\([0-9][0-9]*\)[[:space:]]*$$/\1/p' "$$tmp/log"); \
		[ "$$volumes" = 2 ] || { echo "enclosure-check: $$part has $${volumes:-no} CGAL volumes, want 2"; exit 1; }; \
		echo "enclosure-check: $$part is one solid"; \
	done

# Cameras are translate_x,y,z,rot_x,y,z,distance, recorded so renders stay comparable.
# The Nord Light colour scheme is newer than OpenSCAD 2021.01.
enclosure-images: ## Re-render enclosure/images (needs an OpenSCAD snapshot with OpenGL)
	$(OPENSCAD_PNG) --camera=56,42,16,62,0,25,340 --imgsize=1600,1000 \
		-o enclosure/images/assembly.png enclosure/managents_case.scad
	$(OPENSCAD_PNG) --camera=64,40,20,72,0,125,320 --imgsize=1600,1000 \
		-o enclosure/images/assembly-side.png enclosure/managents_case.scad
	$(OPENSCAD_PNG) --render -D 'part="body"' --camera=60,44,14,50,0,335,360 --imgsize=1200,900 \
		-o enclosure/images/body.png enclosure/managents_case.scad
	$(OPENSCAD_PNG) --render -D 'part="bezel"' --camera=58,32,-1,45,0,20,320 --imgsize=1200,900 \
		-o enclosure/images/bezel-print.png enclosure/managents_case.scad

clean: ## Remove build outputs
	rm -rf bin firmware/.pio
