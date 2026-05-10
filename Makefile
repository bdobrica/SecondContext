GO ?= go
MIGRATE ?= migrate

.PHONY: run test build docker-up docker-down docker-logs migrate-up migrate-down

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
	$(MIGRATE) -path migrations -database "$$POSTGRES_DSN" up

migrate-down:
	$(MIGRATE) -path migrations -database "$$POSTGRES_DSN" down 1
