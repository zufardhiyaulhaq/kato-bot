.DEFAULT_GOAL := help

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

test: ## Run all unit tests (race detector on)
	go test -race ./...

build: ## Build the kato-bot binary into ./bin
	CGO_ENABLED=0 go build -o bin/kato-bot ./cmd/kato-bot

image: ## Build the container image locally (tag kato-bot:dev)
	docker build -t kato-bot:dev .

run: ## Run kato-bot locally (loads .env if present)
	@set -a; [ -f .env ] && . ./.env; set +a; go run ./cmd/kato-bot
