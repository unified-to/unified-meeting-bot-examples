/**
 * Unified meeting bot: TypeScript example
 *
 * Sends a bot into a Zoom, Microsoft Teams, Webex or Google Meet meeting with
 * Unified's Calendar API (calendar_bot), then prints every calendar_botevent
 * webhook it receives.
 *
 *   node src/meeting-bot.ts [--name "Meeting Bot"] [--json] <meeting_url>
 *
 * How it works:
 *   1. Start a local HTTP server to receive webhooks (PUBLIC_URL forwards to it).
 *   2. Subscribe to calendar_botevent webhooks for the connection.
 *   3. Create the bot for the meeting URL.
 *   4. Verify and print each event as it arrives.
 *   5. On Ctrl+C, remove the bot (it leaves the meeting) and the webhook.
 *
 * No dependencies: Node.js 22.18+ runs TypeScript directly and has fetch built in.
 */
import { createHmac, timingSafeEqual } from "node:crypto";
import { createServer } from "node:http";
import { parseArgs } from "node:util";

const WEBHOOK_PATH = "/bot-events";

// --- Configuration ---------------------------------------------------------------

const { values: flags, positionals } = parseArgs({
  allowPositionals: true,
  options: {
    name: { type: "string", default: "Meeting Bot" }, // shown to people in the meeting
    json: { type: "boolean", default: false }, // print each event as raw JSON instead
  },
});
const meetingUrl = positionals[0];
if (!meetingUrl) {
  console.error('Usage: node src/meeting-bot.ts [--name "Meeting Bot"] [--json] <meeting_url>');
  process.exit(2);
}

const required = ["UNIFIED_API_KEY", "UNIFIED_CONNECTION_ID", "UNIFIED_WORKSPACE_SECRET", "PUBLIC_URL"];
const missing = required.filter((name) => !process.env[name]);
if (missing.length > 0) {
  console.error(`Missing environment variables: ${missing.join(", ")} (see .env.example)`);
  process.exit(2);
}

const API_URL = process.env.UNIFIED_API_URL ?? "https://api.unified.to";
const API_KEY = process.env.UNIFIED_API_KEY!;
const CONNECTION_ID = process.env.UNIFIED_CONNECTION_ID!;
const WORKSPACE_SECRET = process.env.UNIFIED_WORKSPACE_SECRET!;
const PUBLIC_URL = process.env.PUBLIC_URL!.replace(/\/+$/, "");
const PORT = Number(process.env.PORT ?? 3000);

// --- Types: only the fields this example prints ------------------------------------
// Full model: https://docs.unified.to/calendar/botevent/model

interface Participant {
  id?: string;
  name?: string;
  email?: string;
  is_host?: boolean;
}

interface BotEvent {
  id?: string;
  created_at?: string;
  bot_id?: string;
  type?: string; // BOT_STATUS_CHANGE, PARTICIPANT_JOINED, TRANSCRIPT_RECEIVED, ...
  status_change?: { status?: string; changed_at?: string; sub_code?: string; message?: string };
  participant?: Participant;
  transcript?: { text: string; participant?: Participant };
  chat_message?: { message?: string; sender?: Participant };
}

/** The body Unified POSTs to your hook_url. */
interface WebhookDelivery {
  data: BotEvent[];
  nonce: string;
  sig256: string;
}

// --- Unified API -------------------------------------------------------------------

async function unified<T = any>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(API_URL + path, {
    method,
    headers: { Authorization: `Bearer ${API_KEY}`, "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const text = await res.text();
  if (!res.ok) throw new Error(`${method} ${path} → ${res.status} ${text}`);
  return (text ? JSON.parse(text) : {}) as T;
}

// --- Webhook handling --------------------------------------------------------------

/** sig256 = base64(HMAC-SHA256(workspace secret, JSON.stringify(data) + nonce)) */
function isSignatureValid(delivery: WebhookDelivery): boolean {
  const expected = createHmac("sha256", WORKSPACE_SECRET)
    .update(JSON.stringify(delivery.data) + delivery.nonce)
    .digest("base64");
  const a = Buffer.from(expected);
  const b = Buffer.from(String(delivery.sig256 ?? ""));
  return a.length === b.length && timingSafeEqual(a, b); // timing-safe comparison
}

let botId: string | undefined; // set once our bot exists
const seen = new Set<string>(); // Unified retries failed deliveries, so skip repeats

function handleEvent(event: BotEvent): void {
  // The webhook receives events for every bot on this connection. Keep ours.
  if (botId && event.bot_id !== botId) return;

  // Deduplicate on id. Google Meet can reuse ids for status changes, so add changed_at.
  if (event.id) {
    const key = `${event.id}:${event.status_change?.changed_at ?? ""}`;
    if (seen.has(key)) return;
    seen.add(key);
  }

  if (flags.json) {
    console.log(JSON.stringify(event));
    return;
  }
  const time = event.created_at?.slice(11, 19) ?? "--:--:--"; // HH:MM:SS (UTC)
  console.log(`${time}  ${(event.type ?? "UNKNOWN").padEnd(27)}  ${describe(event)}`.trimEnd());
}

/** One line of detail for each event type. */
function describe(event: BotEvent): string {
  switch (event.type) {
    case "BOT_STATUS_CHANGE": {
      const { status, sub_code, message } = event.status_change ?? {};
      return [status, sub_code && `(${sub_code})`, message].filter(Boolean).join(" ");
    }
    case "TRANSCRIPT_RECEIVED":
    case "TRANSCRIPT_PARTIAL":
      return `${nameOf(event.transcript?.participant)}: ${event.transcript?.text}`;
    case "CHAT_MESSAGE_RECEIVED":
      return `${nameOf(event.chat_message?.sender)}: ${event.chat_message?.message}`;
    default: // PARTICIPANT_* events carry a participant; RECORDING_* events carry nothing extra
      if (!event.participant) return "";
      return nameOf(event.participant) + (event.participant.is_host ? " (host)" : "");
  }
}

function nameOf(p?: Participant): string {
  return p?.name || p?.email || p?.id || "unknown";
}

const server = createServer(async (req, res) => {
  if (req.method !== "POST" || req.url !== WEBHOOK_PATH) {
    res.writeHead(404).end();
    return;
  }

  let delivery: WebhookDelivery;
  try {
    const chunks: Buffer[] = [];
    for await (const chunk of req) chunks.push(chunk);
    delivery = JSON.parse(Buffer.concat(chunks).toString("utf8"));
  } catch {
    res.writeHead(400).end();
    return;
  }

  if (!isSignatureValid(delivery)) {
    console.error("Rejected a webhook with an invalid signature");
    res.writeHead(401).end();
    return;
  }

  // Reply first: Unified waits at most 5 seconds. Do slow work after this.
  res.end("ok");
  for (const event of delivery.data ?? []) handleEvent(event);
});

// --- Main --------------------------------------------------------------------------

async function main(): Promise<void> {
  // Catch Ctrl+C from the start, so pressing it during setup still cleans up.
  const ctrlC = new Promise<void>((resolve) => process.once("SIGINT", () => resolve()));

  // 1. Listen for webhooks.
  await new Promise<void>((resolve) => server.listen(PORT, resolve));
  console.error(`Listening for webhooks on http://localhost:${PORT}${WEBHOOK_PATH}`);

  // 2. Subscribe to bot events. One webhook carries events for every bot on the connection.
  const webhook = await unified<{ id: string }>("POST", "/unified/webhook", {
    connection_id: CONNECTION_ID,
    object_type: "calendar_botevent",
    event: "created",
    webhook_type: "native",
    hook_url: PUBLIC_URL + WEBHOOK_PATH,
  });
  console.error(`Webhook ${webhook.id} subscribed → ${PUBLIC_URL}${WEBHOOK_PATH}`);

  try {
    // 3. Send the bot into the meeting.
    const bot = await unified<{ id: string; status: string }>("POST", `/calendar/${CONNECTION_ID}/bot`, {
      name: flags.name,
      meeting_url: meetingUrl,
      // Zoom only: opt in to live captions and chat. Other platforms reject recording_config (501).
      ...(meetingUrl.includes("zoom.us") && { recording_config: { record_transcript: true, record_chat: true } }),
    });
    botId = bot.id;
    console.error(`Bot ${bot.id} created (${bot.status}). Printing events; press Ctrl+C to stop.\n`);

    // 4. The webhook handler prints events until Ctrl+C.
    await ctrlC;

    // 5. Remove the bot. It leaves the meeting and stops sending events.
    console.error(`\nRemoving bot ${bot.id}`);
    await unified("DELETE", `/calendar/${CONNECTION_ID}/bot/${bot.id}`).catch((err) => console.error(err.message));
  } finally {
    console.error(`Removing webhook ${webhook.id}`);
    await unified("DELETE", `/unified/webhook/${webhook.id}`).catch((err) => console.error(err.message));
    server.close();
  }
}

main().then(
  () => process.exit(0),
  (err) => {
    console.error(`Error: ${err.message}`);
    process.exit(1);
  },
);
