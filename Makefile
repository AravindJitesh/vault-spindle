.PHONY: up down test test-integration test-kill9 test-all exercise build

COMPOSE := $(shell { docker compose version >/dev/null 2>&1 && echo "docker compose"; } || { command -v docker-compose >/dev/null 2>&1 && echo "docker-compose"; })

up:
	$(COMPOSE) up --build -d

down:
	$(COMPOSE) down

build:
	go build -o bin/server ./cmd/server

test:
	go test ./internal/... -v -count=1

test-integration:
	$(COMPOSE) up -d db
	@echo "Waiting for Postgres..."
	@sleep 3
	DATABASE_URL=postgres://vault:vault@localhost:5432/vault?sslmode=disable go test ./tests/ -v -count=1

test-kill9:
	chmod +x scripts/test-kill9.sh
	./scripts/test-kill9.sh

test-all: test test-integration test-kill9

exercise:
	chmod +x scripts/exercise.sh
	./scripts/exercise.sh
