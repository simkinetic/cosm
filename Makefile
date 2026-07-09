.PHONY: build extensions test cover vet install clean

VERSION ?= dev
BIN ?= cosm

build:
	go build -ldflags "-X main.version=$(VERSION)" -o $(BIN) .

extensions:
	go build -o cosm-ext-lua ./cmd/cosm-ext-lua
	go build -o cosm-ext-cmake ./cmd/cosm-ext-cmake

test:
	go test ./...

vet:
	go vet ./...

cover:
	bash scripts/coverage.sh

install: build extensions
	@echo "Copy cosm, cosm-ext-lua, cosm-ext-cmake onto your PATH"

clean:
	rm -f $(BIN) cosm-ext-lua cosm-ext-cmake coverage.out
