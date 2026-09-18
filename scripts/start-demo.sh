#!/bin/bash
# Start all demo services
# Usage: ./scripts/start-demo.sh

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
LOG_DIR="$PROJECT_DIR/logs"

# Create logs directory if it doesn't exist
mkdir -p "$LOG_DIR"

echo "🚀 Starting demo services..."
echo "Project directory: $PROJECT_DIR"
echo "Log directory: $LOG_DIR"

cd "$PROJECT_DIR"

# Load .env file if it exists
if [ -f .env ]; then
    echo "🔐 Loading environment variables from .env"
    set -a
    source .env
    set +a
else
    echo "⚠️  Warning: .env file not found. Gemini API calls will fail."
    echo "   Create a .env file with: echo 'GEMINI_API_KEY=your_key_here' > .env"
fi

# Build all services
echo "🔨 Building services..."
go build -o bin/payment ./examples/demo-shop/payment
go build -o bin/checkout ./examples/demo-shop/checkout
go build -o bin/storefront ./examples/demo-shop/storefront
go build -o bin/gateway ./gateway

# Kill any existing processes
echo "🧹 Cleaning up existing processes..."
pkill -9 payment 2>/dev/null || true
pkill -9 checkout 2>/dev/null || true
pkill -9 storefront 2>/dev/null || true
pkill -9 gateway 2>/dev/null || true
sleep 1

# Start services in order
echo "💳 Starting payment service on :8083..."
PAYMENT_PORT=8083 ./bin/payment > "$LOG_DIR/payment.log" 2>&1 &
echo $! > "$LOG_DIR/payment.pid"

echo "🛒 Starting checkout service on :8082..."
CHECKOUT_PORT=8082 PAYMENT_URL=http://localhost:8083 ./bin/checkout > "$LOG_DIR/checkout.log" 2>&1 &
echo $! > "$LOG_DIR/checkout.pid"

echo "🏪 Starting storefront service on :8081..."
STOREFRONT_PORT=8081 CHECKOUT_URL=http://localhost:8082 ./bin/storefront > "$LOG_DIR/storefront.log" 2>&1 &
echo $! > "$LOG_DIR/storefront.pid"

echo "🌐 Starting gateway on :8080..."
./bin/gateway > "$LOG_DIR/gateway.log" 2>&1 &
echo $! > "$LOG_DIR/gateway.pid"

# Wait for services to start
echo "⏳ Waiting for services to start..."
sleep 3

# Check if services are running
echo "✅ Checking service status..."
SERVICES_RUNNING=0
for service in payment checkout storefront gateway; do
    if pgrep -f "bin/$service" > /dev/null; then
        echo "  ✓ $service is running"
        SERVICES_RUNNING=$((SERVICES_RUNNING + 1))
    else
        echo "  ✗ $service failed to start (check $LOG_DIR/$service.log)"
    fi
done

if [ $SERVICES_RUNNING -eq 4 ]; then
    echo ""
    echo "🎉 All services started successfully!"
    echo ""
    echo "Test the gateway:"
    echo "  curl http://localhost:8080/buy"
    echo ""
    echo "View logs:"
    echo "  tail -f $LOG_DIR/gateway.log"
    echo ""
    echo "Stop services:"
    echo "  ./scripts/stop-demo.sh"
else
    echo ""
    echo "⚠️  Some services failed to start. Check logs in $LOG_DIR/"
    exit 1
fi
