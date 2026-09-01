-include .env
export

BIN := bin

COMPOSE := $(shell docker compose version >/dev/null 2>&1 && echo "docker compose" || echo "docker-compose")

.PHONY: dev demo build test clean obs-up obs-down

obs-up:
	$(COMPOSE) up -d

obs-down:
	$(COMPOSE) down

build:
	go build -o $(BIN)/payment ./examples/demo-shop/payment
	go build -o $(BIN)/checkout ./examples/demo-shop/checkout
	go build -o $(BIN)/storefront ./examples/demo-shop/storefront

demo: build
	@echo "starting payment   on :$${PAYMENT_PORT:-8082}"
	@echo "starting checkout  on :$${CHECKOUT_PORT:-8081}"
	@echo "starting storefront on :$${STOREFRONT_PORT:-8080}"
	@echo "try: curl http://localhost:$${STOREFRONT_PORT:-8080}/buy   (Ctrl-C to stop)"
	$(BIN)/payment & $(BIN)/checkout & $(BIN)/storefront & wait

dev: demo

test:
	go test ./...

clean:
	rm -rf $(BIN)
