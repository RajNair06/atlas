#!/bin/bash
# Demonstrate the self-healing flow
# Usage: ./scripts/demo-healing.sh

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
LOG_DIR="$PROJECT_DIR/logs"

echo "🎭 Self-Healing API Gateway Demo"
echo "================================="
echo ""

cd "$PROJECT_DIR"

# Check if services are running
echo "🔍 Checking if services are running..."
SERVICES_RUNNING=0
for service in payment checkout storefront gateway; do
    if pgrep -f "bin/$service" > /dev/null; then
        SERVICES_RUNNING=$((SERVICES_RUNNING + 1))
    fi
done

if [ $SERVICES_RUNNING -ne 4 ]; then
    echo "⚠️  Not all services are running. Starting them now..."
    "$SCRIPT_DIR/start-demo.sh"
    sleep 2
fi

echo ""
echo "Step 1: Make a successful request"
echo "----------------------------------"
echo "Command: curl -s http://localhost:8080/buy"
echo ""
RESPONSE=$(curl -s -w "\n%{http_code}" http://localhost:8080/buy)
HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
BODY=$(echo "$RESPONSE" | head -n -1)

echo "Response: $BODY"
echo "Status: $HTTP_CODE"
echo ""

if [ "$HTTP_CODE" != "200" ]; then
    echo "⚠️  Expected 200, got $HTTP_CODE. Check logs in $LOG_DIR/"
    exit 1
fi

echo "✅ Request succeeded!"
echo ""
echo "Step 2: Kill the payment service to simulate a failure"
echo "--------------------------------------------------------"
echo "Command: pkill -9 payment"
pkill -9 payment
sleep 1

if ! pgrep -f "bin/payment" > /dev/null; then
    echo "✅ Payment service killed"
else
    echo "❌ Failed to kill payment service"
    exit 1
fi

echo ""
echo "Step 3: Make a request that will fail"
echo "--------------------------------------"
echo "Command: curl -s http://localhost:8080/buy"
echo ""
RESPONSE=$(curl -s -w "\n%{http_code}" http://localhost:8080/buy)
HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
BODY=$(echo "$RESPONSE" | head -n -1)

echo "Response: $BODY"
echo "Status: $HTTP_CODE"
echo ""

if [ "$HTTP_CODE" != "502" ]; then
    echo "⚠️  Expected 502, got $HTTP_CODE"
fi

echo "✅ Request failed as expected (502 Bad Gateway)"
echo ""
echo "Step 4: Extract the request ID from gateway logs"
echo "--------------------------------------------------"
echo "Command: grep 'request failed' $LOG_DIR/gateway.log | tail -1"
echo ""

REQUEST_ID=$(grep "request failed" "$LOG_DIR/gateway.log" | tail -1 | grep -o '"request_id":"[^"]*"' | cut -d'"' -f4)

if [ -z "$REQUEST_ID" ]; then
    echo "❌ Could not find request ID in logs"
    exit 1
fi

echo "Request ID: $REQUEST_ID"
echo ""
echo "Step 5: Call the healing endpoint"
echo "-----------------------------------"
echo "Command: curl -s http://localhost:8080/healing/$REQUEST_ID | jq ."
echo ""

HEALING_RESPONSE=$(curl -s "http://localhost:8080/healing/$REQUEST_ID")

if command -v jq &> /dev/null; then
    echo "$HEALING_RESPONSE" | jq .
else
    echo "$HEALING_RESPONSE" | python3 -m json.tool 2>/dev/null || echo "$HEALING_RESPONSE"
fi

echo ""
echo "🎉 Demo complete!"
echo ""
echo "What happened:"
echo "  1. Gateway received a request to /buy"
echo "  2. Gateway forwarded to storefront → checkout → payment"
echo "  3. Payment service was down, so checkout returned 502"
echo "  4. Gateway captured the error with full context"
echo "  5. Gemini analyzed the error and suggested: $(echo "$HEALING_RESPONSE" | grep -o '"action":"[^"]*"' | cut -d'"' -f4)"
echo ""
echo "Next steps:"
echo "  - Restart payment: ./scripts/start-demo.sh"
echo "  - View logs: tail -f $LOG_DIR/gateway.log"
echo "  - Stop all: ./scripts/stop-demo.sh"
