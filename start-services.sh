#!/bin/bash
# Start all services for testing

cd /home/acer/atlas

# Load environment variables from .env file
if [ -f .env ]; then
    set -a
    source .env
    set +a
    echo "Loaded environment variables from .env"
fi

# Kill any existing processes
pkill -9 payment 2>/dev/null
pkill -9 checkout 2>/dev/null
pkill -9 storefront 2>/dev/null
pkill -9 gateway 2>/dev/null
sleep 1

# Start services
echo "Starting payment on :8083..."
PAYMENT_PORT=8083 ./bin/payment > /tmp/payment.log 2>&1 &
echo "Payment PID: $!"

echo "Starting checkout on :8082..."
CHECKOUT_PORT=8082 PAYMENT_URL=http://localhost:8083 ./bin/checkout > /tmp/checkout.log 2>&1 &
echo "Checkout PID: $!"

echo "Starting storefront on :8081..."
STOREFRONT_PORT=8081 CHECKOUT_URL=http://localhost:8082 ./bin/storefront > /tmp/storefront.log 2>&1 &
echo "Storefront PID: $!"

echo "Starting gateway on :8080..."
./bin/gateway > /tmp/gateway.log 2>&1 &
echo "Gateway PID: $!"

echo ""
echo "All services started. Wait 2 seconds for them to initialize..."
sleep 2

echo ""
echo "=== Service Status ==="
echo "Payment: $(pgrep -f 'bin/payment' > /dev/null && echo 'Running' || echo 'Not running')"
echo "Checkout: $(pgrep -f 'bin/checkout' > /dev/null && echo 'Running' || echo 'Not running')"
echo "Storefront: $(pgrep -f 'bin/storefront' > /dev/null && echo 'Running' || echo 'Not running')"
echo "Gateway: $(pgrep -f 'bin/gateway' > /dev/null && echo 'Running' || echo 'Not running')"

echo ""
echo "=== Testing successful request ==="
curl -s -m 5 http://localhost:8080/buy
echo ""

echo ""
echo "Done! Services are running in the background."
echo "To stop them: pkill -9 payment; pkill -9 checkout; pkill -9 storefront; pkill -9 gateway"
