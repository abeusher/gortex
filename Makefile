BINARY    := gortex
VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS   := -s -w -X main.version=$(VERSION)

.PHONY: build test bench lint fmt clean install

build:
	go build -ldflags '$(LDFLAGS)' -o $(BINARY) ./cmd/gortex/

test:
	go test -race ./...

bench:
	go test -bench=. -benchmem -count=1 -benchtime=1s \
		./internal/parser/languages/ \
		./internal/graph/ \
		./internal/query/ \
		./internal/indexer/ \
		./internal/analysis/

lint:
	golangci-lint run --timeout=5m

fmt:
	gofmt -s -w .

clean:
	rm -f $(BINARY)

install:
	go install -ldflags '$(LDFLAGS)' ./cmd/gortex/

# ---------------------------------------------------------------------------
# Eval framework
# ---------------------------------------------------------------------------

EVAL_DIR     := eval
EVAL_VENV    := $(EVAL_DIR)/.venv
EVAL_PYTHON  := $(EVAL_VENV)/bin/python
EVAL_PIP     := $(EVAL_VENV)/bin/pip
EVAL_CLI     := $(EVAL_VENV)/bin/gortex-eval
EVAL_ANALYZE := $(EVAL_VENV)/bin/gortex-eval-analyze

MODEL  ?= claude-sonnet
MODE   ?= baseline
SLICE  ?= 0:5
SUBSET ?= lite

.PHONY: eval-setup eval-test eval-test-all eval-list \
        eval-single eval-matrix eval-debug eval-summary eval-compare eval-tools

# Setup: create venv and install deps
eval-setup: build
	@test -d $(EVAL_VENV) || python3 -m venv $(EVAL_VENV)
	$(EVAL_PIP) install -q -e "$(EVAL_DIR)[dev]"
	@echo "✓ Eval framework ready. Binary: ./$(BINARY)"

# Build linux/amd64 binary for container injection (requires podman/docker)
eval-build-linux:
	podman run --rm --platform linux/amd64 -v $(CURDIR):/src -w /src golang:1.25 \
		bash -c "apt-get update -qq && apt-get install -y -qq libtree-sitter-dev && go build -ldflags '$(LDFLAGS)' -o gortex-linux ./cmd/gortex/"
	@echo "✓ Built gortex-linux (linux/amd64)"

# Run Python eval tests
eval-test:
	$(EVAL_PYTHON) -m pytest $(EVAL_DIR)/tests/ -q

# Run all tests (Go + Python)
eval-test-all: test eval-test

# List available configs
eval-list: eval-setup
	$(EVAL_CLI) list-configs

# Single (model, mode) run
eval-single: eval-setup
	$(EVAL_CLI) single -m $(MODEL) --mode $(MODE) --subset $(SUBSET) --slice $(SLICE)

# Full A/B matrix
eval-matrix: eval-setup
	$(EVAL_CLI) matrix --models claude-sonnet claude-haiku \
		--modes baseline native native_augment \
		--subset $(SUBSET) --slice $(SLICE)

# Debug a single instance
eval-debug: eval-setup
	$(EVAL_CLI) debug -m $(MODEL) --mode $(MODE) -i $(INSTANCE)

# Analyze results
eval-summary:
	$(EVAL_ANALYZE) summary results/

eval-compare:
	$(EVAL_ANALYZE) compare-modes results/ -m $(MODEL)

eval-tools:
	$(EVAL_ANALYZE) tool-usage results/
