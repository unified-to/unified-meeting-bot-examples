# Mock Unified API

[`server.ts`](server.ts) is a local stand-in for the parts of the Unified API the examples use. Use it to try an example, or to teach with one, without a real meeting, accounts or a tunnel. It needs Node.js 22.18+ and has no dependencies.

When an example creates a bot, the mock plays back a short scripted Zoom meeting as signed `calendar_botevent` webhooks. The script includes three deliveries the examples should not print:

- a **retried delivery** with the same event id, which should be printed once
- an event for **another bot** on the same connection, which should be skipped
- a **tampered delivery** whose signature doesn't match, which should be rejected with `401`

```sh
UNIFIED_WORKSPACE_SECRET=test node mock-unified/server.ts      # listens on :4000
```

Then run any example with these settings:

```sh
export UNIFIED_API_KEY=test UNIFIED_CONNECTION_ID=test UNIFIED_WORKSPACE_SECRET=test \
       UNIFIED_API_URL=http://localhost:4000 PUBLIC_URL=http://localhost:3000
```

Optional settings: `MOCK_PORT` (default `4000`) and `MOCK_DELAY_MS` (time between deliveries, default `400`).

The mock follows the documented request and response shapes, but it's a simulation: always test against real meetings before you ship.
