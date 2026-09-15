-include .env
export

BIN := bin

.PHONY: dev demo build test clean gateway gateway-build

build:
	go build -o $(BIN)/payment ./examples/demo-shop/payment
	go build -o $(BIN)/checkout ./examples/demo-shop/checkout
	go build -o $(BIN)/storefront ./examples/demo-shop/storefront

gateway-build:
	go build -o $(BIN)/gateway ./gateway

demo: build
	@echo "starting payment    on :$${PAYMENT_PORT:-8082}"
	@echo "starting checkout   on :$${CHECKOUT_PORT:-8081}"
	@echo "starting storefront on :$${STOREFRONT_PORT:-8080}"
	@echo "try: curl http://localhost:$${STOREFRONT_PORT:-8080}/buy   (Ctrl-C to stop)"
	$(BIN)/payment & $(BIN)/checkout & $(BIN)/storefront & wait

gateway: gateway-build build
	@echo "starting gateway    on :8080"
	@echo "starting storefront on :8081"
	@echo "starting checkout   on :8082"
	@echo "starting payment    on :8083"
	@echo "try: curl http://localhost:8080/buy   (Ctrl-C to stop)"
	STOREFRONT_PORT=8081 CHECKOUT_URL=http://localhost:8082 \
		$(BIN)/storefront & \
	CHECKOUT_PORT=8082 PAYMENT_URL=http://localhost:8083 \
		$(BIN)/checkout & \
	PAYMENT_PORT=8083 $(BIN)/payment & \
	$(BIN)/gateway & wait

dev: demo

test:
	go test ./...

clean:
	rm -rf $(BIN)
