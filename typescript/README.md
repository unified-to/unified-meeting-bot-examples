# TypeScript meeting bot

The whole bot is one file, [`src/meeting-bot.ts`](src/meeting-bot.ts). It has no runtime dependencies: Node.js 22.18+ runs TypeScript directly and has `fetch` built in.

```sh
set -a; source ../.env; set +a        # see ../.env.example
node src/meeting-bot.ts https://us02web.zoom.us/j/81234567890
node src/meeting-bot.ts --name "Notetaker" --json <meeting_url>
```

To type-check the code (optional), run `npm install` and then `npm run typecheck`.

**Signature note:** `JSON.stringify(delivery.data)` reproduces exactly what Unified signed, because Unified signs with `JSON.stringify` too.

See the [main README](../README.md) for setup, the event reference and production notes.
