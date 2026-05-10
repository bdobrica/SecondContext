GO ?= go

.PHONY: run test build docker-up docker-down docker-logs migrate-up migrate-down migrate-version seed-dev-user

run:
	$(GO) run ./cmd/api

test:
	$(GO) test ./...

build:
	$(GO) build ./cmd/api

docker-up:
	docker compose up --build

docker-down:
	docker compose down --remove-orphans

docker-logs:
	docker compose logs -f api

migrate-up:
	$(GO) run ./cmd/migrate up

migrate-down:
	$(GO) run ./cmd/migrate down 1

migrate-version:
	$(GO) run ./cmd/migrate version

seed-dev-user:
	$(GO) run ./cmd/devseed
