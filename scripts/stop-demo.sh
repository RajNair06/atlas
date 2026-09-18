#!/bin/bash
# Stop all demo services
# Usage: ./scripts/stop-demo.sh

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
LOG_DIR="$PROJECT_DIR/logs"

echo "🛑 Stopping demo services..."

cd "$PROJECT_DIR"

# Stop services by PID files if they exist
if [ -d "$LOG_DIR" ]; then
    for service in payment checkout storefront gateway; do
        PID_FILE="$LOG_DIR/$service.pid"
        if [ -f "$PID_FILE" ]; then
            PID=$(cat "$PID_FILE")
            if kill -0 "$PID" 2>/dev/null; then
                echo "  Stopping $service (PID: $PID)..."
                kill -9 "$PID" 2>/dev/null || true
            fi
            rm -f "$PID_FILE"
        fi
    done
fi

# Also kill by process name as fallback
echo "🧹 Cleaning up any remaining processes..."
pkill -9 payment 2>/dev/null || true
pkill -9 checkout 2>/dev/null || true
pkill -9 storefront 2>/dev/null || true
pkill -9 gateway 2>/dev/null || true

sleep 1

# Verify all stopped
echo "✅ Verifying services are stopped..."
SERVICES_RUNNING=0
for service in payment checkout storefront gateway; do
    if pgrep -f "bin/$service" > /dev/null; then
        echo "  ⚠️  $service is still running"
        SERVICES_RUNNING=$((SERVICES_RUNNING + 1))
    else
        echo "  ✓ $service stopped"
    fi
done

if [ $SERVICES_RUNNING -eq 0 ]; then
    echo ""
    echo "🎉 All services stopped successfully!"
else
    echo ""
    echo "⚠️  Some services are still running. Try: pkill -9 $service"
fi
