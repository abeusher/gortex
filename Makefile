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
