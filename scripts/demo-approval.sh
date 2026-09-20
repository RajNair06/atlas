#!/usr/bin/env bash
# demo-approval.sh — end-to-end proof of the human-in-the-loop approval gate.
#
# Flow:
#   1. build + start the demo shop and the gateway (require_approval: true)
#   2. kill payment, fire a POST /checkout in the background (it will hang)
#   3. poll GET /ui/decisions until the pending decision appears
#   4. restart payment, POST approve (a human would click the button in /ui)
#   5. assert the blocked curl completes with X-Healed: true
#   6. show the healed entry from GET /ui/history
#
# Usage: ./scripts/demo-approval.sh
# Requires: .env with GEMINI_API_KEY, gateway.yaml with require_approval: true

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
LOG_DIR="$PROJECT_DIR/logs"
WORK="$LOG_DIR/demo-approval"
GATEWAY="http://localhost:8080"

FAILURES=0
CURL_PID=""

pass() { echo "PASS: $1"; }
fail() { echo "FAIL: $1"; FAILURES=$((FAILURES + 1)); }

cleanup() {
    echo ""
    echo "--- cleanup ---"
    if [ -n "$CURL_PID" ]; then
        pkill -P "$CURL_PID" 2>/dev/null
        kill "$CURL_PID" 2>/dev/null
    fi
    pkill -x payment 2>/dev/null
    pkill -x checkout 2>/dev/null
    pkill -x storefront 2>/dev/null
    pkill -x gateway 2>/dev/null
    rm -rf "$WORK"
    echo "all services stopped"
}
trap cleanup EXIT

cd "$PROJECT_DIR"
mkdir -p "$LOG_DIR"
rm -rf "$WORK" && mkdir -p "$WORK"

echo "atlas — approval gate demo (Day 6)"
echo "==================================="
echo ""

# --- preconditions -----------------------------------------------------------

# Load the API key without ever printing it.
if [ -f .env ]; then
    set -a
    # shellcheck disable=SC1091
    source .env
    set +a
fi
if [ -z "${GEMINI_API_KEY:-}" ]; then
    fail "GEMINI_API_KEY is not set (expected in .env)"
    exit 1
fi
pass "GEMINI_API_KEY loaded from .env"

if grep -q "require_approval: true" gateway.yaml; then
    pass "gateway.yaml has require_approval: true"
else
    fail "gateway.yaml must set server.require_approval: true for this demo"
    exit 1
fi

# --- build -------------------------------------------------------------------

echo ""
echo "--- build ---"
if go build -o bin/payment ./examples/demo-shop/payment &&
   go build -o bin/checkout ./examples/demo-shop/checkout &&
   go build -o bin/storefront ./examples/demo-shop/storefront &&
   go build -o bin/gateway ./gateway; then
    pass "all binaries built"
else
    fail "build failed"
    exit 1
fi

# --- start services ----------------------------------------------------------

start_service() {
    local name="$1"
    shift
    env "$@" setsid nohup "$PROJECT_DIR/bin/$name" > "$LOG_DIR/$name.log" 2>&1 < /dev/null &
}

echo ""
echo "--- start services ---"
pkill -x payment 2>/dev/null
pkill -x checkout 2>/dev/null
pkill -x storefront 2>/dev/null
pkill -x gateway 2>/dev/null
sleep 0.5

start_service payment PAYMENT_PORT=8083
start_service checkout CHECKOUT_PORT=8082 PAYMENT_URL=http://localhost:8083
start_service storefront STOREFRONT_PORT=8081 CHECKOUT_URL=http://localhost:8082
start_service gateway

GATEWAY_READY=0
for _ in $(seq 1 40); do
    if curl -s -o /dev/null -m 1 "$GATEWAY/ui"; then GATEWAY_READY=1; break; fi
    sleep 0.25
done
if [ "$GATEWAY_READY" = "1" ]; then
    pass "gateway up on :8080 (payment :8083, checkout :8082, storefront :8081)"
else
    fail "gateway did not become ready (see $LOG_DIR/gateway.log)"
    exit 1
fi

BASELINE_OK=0
for _ in $(seq 1 20); do
    if curl -s -m 2 -X POST "$GATEWAY/checkout" | grep -q charged; then BASELINE_OK=1; break; fi
    sleep 0.5
done
if [ "$BASELINE_OK" = "1" ]; then
    pass "baseline POST /checkout healthy (payment up)"
else
    fail "baseline POST /checkout did not succeed"
    exit 1
fi

# --- console sanity: HTML + SSE ----------------------------------------------

if curl -s -m 5 "$GATEWAY/ui" | grep -qi "healing console"; then
    pass "GET /ui serves the dashboard HTML"
else
    fail "GET /ui did not serve the dashboard"
fi

curl -sN -m 2 -D "$WORK/sse-headers.txt" -o "$WORK/sse-body.txt" "$GATEWAY/ui/events" > /dev/null 2>&1 || true
if grep -qi "text/event-stream" "$WORK/sse-headers.txt" && grep -q ": connected" "$WORK/sse-body.txt"; then
    pass "GET /ui/events streams SSE (text/event-stream + greeting frame)"
else
    fail "GET /ui/events did not stream SSE"
fi

# --- break payment, fire the request that will hang ---------------------------

echo ""
echo "--- break payment ---"
pkill -x payment
sleep 0.5
if pgrep -x payment > /dev/null; then
    fail "payment is still running"
    exit 1
fi
pass "payment killed — upstream :8083 is now down"

echo ""
echo "--- fire blocked request ---"
START_TS=$SECONDS
(
    curl -s -m 120 -X POST -D "$WORK/headers.txt" -o "$WORK/body.txt" "$GATEWAY/checkout"
    echo $? > "$WORK/curl-exit.txt"
) > /dev/null 2>&1 < /dev/null &
CURL_PID=$!
echo "curl -s -m 120 -X POST $GATEWAY/checkout  (pid $CURL_PID, blocking on the approval gate)"

# --- wait for the pending decision -------------------------------------------

DECISION_ID=""
PENDING_JSON=""
for _ in $(seq 1 60); do
    PENDING_JSON=$(curl -s -m 2 "$GATEWAY/ui/decisions" 2>/dev/null)
    DECISION_ID=$(printf '%s' "$PENDING_JSON" | grep -o '"id":"[^"]*"' | head -n1 | cut -d'"' -f4)
    [ -n "$DECISION_ID" ] && break
    sleep 0.5
done

if [ -z "$DECISION_ID" ]; then
    fail "no pending decision appeared within 30s (see $LOG_DIR/gateway.log)"
    exit 1
fi
REQUEST_ID=$(printf '%s' "$PENDING_JSON" | grep -o '"request_id":"[^"]*"' | head -n1 | cut -d'"' -f4)
ACTION=$(printf '%s' "$PENDING_JSON" | grep -o '"action":"[^"]*"' | head -n1 | cut -d'"' -f4)
pass "pending decision appeared: id=$DECISION_ID request_id=$REQUEST_ID action=$ACTION"

if kill -0 "$CURL_PID" 2>/dev/null; then
    pass "client request is still blocked, waiting for a human"
else
    fail "client request finished before any decision was made"
fi

# In a manual demo this is where you open http://localhost:8080/ui and click
# Approve on the card. Here the script plays the human via the decision API.
echo ""
echo "--- restart payment + approve ---"
start_service payment PAYMENT_PORT=8083
PAYMENT_UP=0
for _ in $(seq 1 20); do
    if curl -s -o /dev/null -m 1 -X POST http://localhost:8083/pay; then PAYMENT_UP=1; break; fi
    sleep 0.25
done
if [ "$PAYMENT_UP" = "1" ]; then
    pass "payment restarted on :8083 (the retry can now succeed)"
else
    fail "payment did not restart"
    exit 1
fi

APPROVE_CODE=$(curl -s -m 5 -o "$WORK/approve.json" -w '%{http_code}' -X POST "$GATEWAY/ui/decisions/$DECISION_ID/approve")
if [ "$APPROVE_CODE" = "200" ] && grep -q '"ok":true' "$WORK/approve.json"; then
    pass "POST /ui/decisions/$DECISION_ID/approve -> 200 {\"ok\":true}"
else
    fail "approve returned HTTP $APPROVE_CODE: $(cat "$WORK/approve.json" 2>/dev/null)"
    exit 1
fi

# --- wait for the healed response ---------------------------------------------

echo ""
echo "--- wait for client ---"
wait "$CURL_PID"
ELAPSED=$((SECONDS - START_TS))
CURL_EXIT=$(cat "$WORK/curl-exit.txt" 2>/dev/null || echo "?")
if [ "$CURL_EXIT" != "0" ]; then
    fail "curl exited with code $CURL_EXIT"
else
    pass "blocked curl completed in ${ELAPSED}s (failure + Gemini + human wait + retry)"
fi

echo ""
echo "--- response headers ---"
cat "$WORK/headers.txt" 2>/dev/null
echo "--- response body ---"
cat "$WORK/body.txt" 2>/dev/null
echo ""

if grep -qi "^X-Healed: true" "$WORK/headers.txt" 2>/dev/null; then
    pass "response carries X-Healed: true"
else
    fail "X-Healed: true missing from response headers"
fi
if grep -qi "^X-Healing-Action:" "$WORK/headers.txt" 2>/dev/null; then
    pass "response carries $(grep -i '^X-Healing-Action:' "$WORK/headers.txt" | tr -d '\r') / $(grep -i '^X-Healing-Attempts:' "$WORK/headers.txt" | tr -d '\r')"
else
    fail "X-Healing-Action header missing"
fi
if grep -q charged "$WORK/body.txt" 2>/dev/null; then
    pass 'healed body is the real payment response ({"status":"charged"})'
else
    fail "healed body unexpected: $(cat "$WORK/body.txt" 2>/dev/null)"
fi

# --- history ------------------------------------------------------------------

echo ""
echo "--- healing history ---"
HISTORY=$(curl -s -m 5 "$GATEWAY/ui/history")
if command -v jq > /dev/null 2>&1; then
    printf '%s\n' "$HISTORY" | jq . 2>/dev/null || printf '%s\n' "$HISTORY"
else
    printf '%s\n' "$HISTORY"
fi

if printf '%s' "$HISTORY" | grep -q "\"outcome\":\"healed\"" && printf '%s' "$HISTORY" | grep -q "\"request_id\":\"$REQUEST_ID\""; then
    pass "history records the healed outcome for request $REQUEST_ID"
else
    fail "history does not show a healed entry for request $REQUEST_ID"
fi

# --- summary -------------------------------------------------------------------

echo ""
echo "==================================="
if [ "$FAILURES" -eq 0 ]; then
    echo "DEMO PASSED — pending -> approve -> healed (X-Healed: true)"
    echo "Manual run: ./scripts/start-demo.sh, break payment, curl -X POST :8080/checkout,"
    echo "then click Approve in the browser at http://localhost:8080/ui"
    exit 0
else
    echo "DEMO FAILED — $FAILURES check(s) did not pass"
    exit 1
fi
