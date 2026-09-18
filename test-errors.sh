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
echo "=== Test 1: Happy path (payment is up) ==="
curl -s -m 5 http://localhost:8080/buy || echo "FAILED"

echo ""
echo "=== Test 2: Kill payment service ==="
kill $PAYMENT_PID 2>/dev/null || true
sleep 1
echo "Payment service killed"

echo ""
echo "=== Test 3: Request with payment down (should capture error) ==="
curl -s -m 5 http://localhost:8080/buy || echo "FAILED (expected)"

echo ""
echo "=== Gateway logs (showing error capture) ==="
grep -A 5 "request failed" /tmp/gateway.log || echo "No errors captured yet"

echo ""
echo "=== Checking error store (last 20 lines of gateway log) ==="
tail -20 /tmp/gateway.log

echo ""
echo "Cleaning up..."
kill $STOREFRONT_PID $CHECKOUT_PID $GATEWAY_PID 2>/dev/null || true

echo "Done!"
