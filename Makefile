.PHONY: up down test test-integration test-kill9 test-all exercise build

up:
	docker compose up --build -d

down:
	docker compose down

build:
	go build -o bin/server ./cmd/server

test:
	go test ./internal/... -v -count=1

test-integration:
	docker compose up -d db
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
