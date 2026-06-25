.PHONY: up down test test-integration exercise build

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

exercise:
	chmod +x scripts/exercise.sh
	./scripts/exercise.sh
