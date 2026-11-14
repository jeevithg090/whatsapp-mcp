# Convenience targets for local development. CI runs the same commands.
.PHONY: all build run-bridge run-server test test-bridge test-server lint lint-bridge lint-server fmt

BRIDGE := whatsapp-bridge
SERVER := whatsapp-mcp-server
STATICCHECK := honnef.co/go/tools/cmd/staticcheck@v0.8.2

all: lint test build

build:
	cd $(BRIDGE) && go build -o whatsapp-bridge .

run-bridge:
	cd $(BRIDGE) && go run .

run-server:
	cd $(SERVER) && uv run main.py

test: test-bridge test-server

test-bridge:
	cd $(BRIDGE) && go test -race ./...

test-server:
	cd $(SERVER) && uv run pytest

lint: lint-bridge lint-server

lint-bridge:
	@cd $(BRIDGE) && unformatted="$$(gofmt -l .)" && if [ -n "$$unformatted" ]; then echo "gofmt needed: $$unformatted"; exit 1; fi
	cd $(BRIDGE) && go vet ./...
	cd $(BRIDGE) && go run $(STATICCHECK) ./...

lint-server:
	cd $(SERVER) && uv run ruff check .
	cd $(SERVER) && uv run ruff format --check .
	cd $(SERVER) && uv run mypy

fmt:
	cd $(BRIDGE) && gofmt -w .
	cd $(SERVER) && uv run ruff format . && uv run ruff check --fix .
