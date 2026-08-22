COMPOSE = docker compose
DSN = postgres://atlas:atlas@localhost:5433/atlas?sslmode=disable
MIGRATE = goose -dir migrations postgres

.PHONY: up down migrate migrate-down test run

up:
	$(COMPOSE) up -d postgres

down:
	$(COMPOSE) down

migrate:
	$(MIGRATE) "$(DSN)" up

migrate-down:
	$(MIGRATE) "$(DSN)" down

test:
	go test -p 1 ./...

run:
	go run ./cmd/atlas