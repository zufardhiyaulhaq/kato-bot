.DEFAULT_GOAL := help

.PHONY: help test build image run readme helm.create.releases

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

readme: ## Regenerate README.md from the Helm chart with helm-docs
	helm-docs -c ./charts/kato-bot -d > README.md
	helm-docs -c ./charts/kato-bot

helm.create.releases: ## Package the Helm chart and refresh the chart repo index
	helm package charts/kato-bot --destination charts/releases
	helm repo index charts/releases