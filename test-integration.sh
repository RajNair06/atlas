#!/bin/bash
set -e

cd /home/acer/atlas

echo "Starting services..."
STOREFRONT_PORT=8081 CHECKOUT_URL=http://localhost:8082 ./bin/storefront > /tmp/storefront.log 2>&1 &
STOREFRONT_PID=$!

CHECKOUT_PORT=8082 PAYMENT_URL=http://localhost:8083 ./bin/checkout > /tmp/checkout.log 2>&1 &
CHECKOUT_PID=$!

PAYMENT_PORT=8083 ./bin/payment > /tmp/payment.log 2>&1 &
PAYMENT_PID=$!

./bin/gateway > /tmp/gateway.log 2>&1 &
GATEWAY_PID=$!

echo "Waiting for services to start..."
sleep 3

echo ""
echo "=== Test 1: Happy path (GET /buy) ==="
curl -s -m 5 http://localhost:8080/buy || echo "FAILED"

echo ""
echo "=== Test 2: Direct checkout (POST /checkout) ==="
curl -s -m 5 -X POST http://localhost:8080/checkout || echo "FAILED"

echo ""
echo "=== Test 3: Direct payment (POST /pay) ==="
curl -s -m 5 -X POST http://localhost:8080/pay || echo "FAILED"

echo ""
echo "=== Gateway logs ==="
tail -10 /tmp/gateway.log

echo ""
echo "Cleaning up..."
kill $STOREFRONT_PID $CHECKOUT_PID $PAYMENT_PID $GATEWAY_PID 2>/dev/null || true

echo "Done!"
