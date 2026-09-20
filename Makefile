.DEFAULT_GOAL := help

BIN := bin/api

.PHONY: help run build test test-integration lint fmt vet check db-up db-down

help:
	@grep -E '^[a-z-]+:.*##' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "%-18s %s\n", $$1, $$2}'

run: ## Run the API using the variables from .env
	@test -f .env || { echo "missing .env: cp .env.example .env"; exit 1; }
	@set -a && . ./.env && set +a && go run ./cmd/api

build: ## Build the API binary into bin/
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $(BIN) ./cmd/api

test: ## Run unit tests
	go test -race ./...

test-integration: ## Run tests that need MongoDB (uses MONGODB_URI from .env)
	@test -f .env || { echo "missing .env: cp .env.example .env"; exit 1; }
	@set -a && . ./.env && set +a && go test -race -tags integration ./...

lint: ## Run golangci-lint
	golangci-lint run

fmt: ## Format the code
	gofmt -w .

vet: ## Run go vet
	go vet ./...

check: ## Full quality gate: format, vet, lint, test, build
	@test -z "$$(gofmt -l .)" || { echo "gofmt needed on:"; gofmt -l .; exit 1; }
	$(MAKE) vet lint test build

db-up: ## Start a local MongoDB with docker compose
	docker compose up -d --wait mongo

db-down: ## Stop the local MongoDB
	docker compose down
