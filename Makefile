GO ?= go

.PHONY: run test test-unit test-integration fmt-check vet verify build docker-up docker-down docker-logs migrate-up migrate-down migrate-version seed-dev-user demo eval

run:
	$(GO) run ./cmd/api

test: test-unit

test-unit:
	@echo "==> unit tests (-short; dependency-backed integration tests are excluded)"
	$(GO) test -short ./...

test-integration:
	@echo "==> mandatory dependency-backed integration tests (verbose; skips are failures)"
	./scripts/test-integration.sh

fmt-check:
	@files="$$(gofmt -l .)"; if [ -n "$$files" ]; then echo "gofmt required for:"; echo "$$files"; exit 1; fi

vet:
	$(GO) vet ./...

verify: fmt-check vet test-unit test-integration

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

demo:
	$(GO) run ./cmd/demo

eval:
	$(GO) run ./cmd/eval
