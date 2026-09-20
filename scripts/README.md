# Demo Scripts

These scripts help you run and demonstrate the Self-Healing API Gateway.

## Prerequisites

1. **Go 1.21+** installed
2. **Gemini API Key** - Create a `.env` file in the project root:
   ```bash
   echo "GEMINI_API_KEY=your_key_here" > .env
   ```

## Scripts

### start-demo.sh
Starts all services (payment, checkout, storefront, gateway) with proper configuration.

```bash
./scripts/start-demo.sh
```

Services will be available at:
- Gateway: http://localhost:8080
- Storefront: http://localhost:8081
- Checkout: http://localhost:8082
- Payment: http://localhost:8083

### stop-demo.sh
Stops all running services.

```bash
./scripts/stop-demo.sh
```

### demo-healing.sh
Runs a complete demonstration of the self-healing flow:
1. Makes a successful request
2. Kills the payment service
3. Makes a failing request
4. Calls the healing endpoint
5. Shows Gemini's analysis and suggestion

```bash
./scripts/demo-healing.sh
```

### demo-approval.sh
Runs the human-in-the-loop approval gate end-to-end (requires
`require_approval: true` in gateway.yaml):
1. Starts the demo shop + gateway, checks the console (`/ui`) and SSE stream
2. Kills payment and fires a `POST /checkout` that blocks on the gate
3. Waits for the pending decision to appear in `GET /ui/decisions`
4. Restarts payment, approves the decision via the API (a human would click Approve in the browser at http://localhost:8080/ui)
5. Asserts the blocked curl completes with `X-Healed: true` and prints the healed history entry

```bash
./scripts/demo-approval.sh
```

To experience the manual flow, use `start-demo.sh`, kill payment
(`pkill -x payment`), run `curl -X POST http://localhost:8080/checkout`
(it hangs), open http://localhost:8080/ui, and click Approve.

## Quick Start

```bash
# Start all services
./scripts/start-demo.sh

# Run the healing demo
./scripts/demo-healing.sh

# Stop all services
./scripts/stop-demo.sh
```

## Logs

All service logs are stored in the `logs/` directory:
- `logs/gateway.log`
- `logs/storefront.log`
- `logs/checkout.log`
- `logs/payment.log`

View logs in real-time:
```bash
tail -f logs/gateway.log
```

## Troubleshooting

### Services won't start
Check if ports are already in use:
```bash
lsof -i :8080-8083
```

### Gemini API errors
Make sure your `.env` file exists and contains a valid API key:
```bash
cat .env
```

### Services crashed
Check the logs in the `logs/` directory for error messages.
