/**
 * Mock Unified API for trying the meeting-bot examples without a real meeting.
 *
 * It implements just the endpoints the examples call, and when a bot is created it
 * replays a short scripted meeting as signed calendar_botevent webhooks:
 *
 *   POST   /unified/webhook                    subscribe to bot events
 *   DELETE /unified/webhook/{id}               unsubscribe
 *   POST   /calendar/{connection_id}/bot       create a bot  → starts the replay
 *   GET    /calendar/{connection_id}/bot/{id}  read a bot (recording_id appears at RECORDING_READY)
 *   DELETE /calendar/{connection_id}/bot/{id}  remove a bot  → stops the replay
 *   GET    /calendar/{connection_id}/recording/{id}  read a recording and its transcript
 *   GET    /_mock/state                        counters used by scripts/test-all.sh
 *
 * The replay also sends a duplicate delivery, an event for another bot and a delivery
 * with a bad signature, so you can see each example ignore or reject them.
 *
 * Run:  UNIFIED_WORKSPACE_SECRET=any-secret node mock-unified/server.ts
 * Then point an example at it with UNIFIED_API_URL=http://localhost:4000 and
 * PUBLIC_URL=http://localhost:3000 (no tunnel needed).
 */
import { createHmac, randomBytes } from "node:crypto";
import { createServer, type IncomingMessage, type ServerResponse } from "node:http";

const PORT = Number(process.env.MOCK_PORT ?? 4000);
const SECRET = process.env.UNIFIED_WORKSPACE_SECRET;
const DELAY_MS = Number(process.env.MOCK_DELAY_MS ?? 400);
if (!SECRET) {
  console.error("Set UNIFIED_WORKSPACE_SECRET (any value; use the same one for the example)");
  process.exit(2);
}

type Json = Record<string, any>;
const webhooks = new Map<string, Json>();
const bots = new Map<string, Json>();
const recordings = new Map<string, Json>();
const state = { deliveries_ok: 0, deliveries_rejected: 0, deliveries_failed: 0 };

const newId = () => randomBytes(12).toString("hex"); // looks like a Unified id

// --- HTTP plumbing -----------------------------------------------------------------

async function readJson(req: IncomingMessage): Promise<Json> {
  const chunks: Buffer[] = [];
  for await (const chunk of req) chunks.push(chunk);
  const text = Buffer.concat(chunks).toString("utf8");
  return text ? JSON.parse(text) : {};
}

function send(res: ServerResponse, status: number, body: unknown): void {
  res.writeHead(status, { "Content-Type": "application/json" }).end(JSON.stringify(body));
}

function error(res: ServerResponse, status: number, message: string): void {
  send(res, status, { statusCode: status, error: "Error", message });
}

createServer(async (req, res) => {
  const { pathname } = new URL(req.url ?? "/", "http://localhost");
  const parts = pathname.split("/").filter(Boolean); // e.g. ["calendar", "conn", "bot", "id"]
  console.log(`${req.method} ${pathname}`);

  if (pathname === "/_mock/state") {
    return send(res, 200, { ...state, webhooks: webhooks.size, active_bots: bots.size });
  }
  // The SDKs send the API key as-is in the Authorization header; raw HTTP calls add "Bearer ".
  if (!req.headers.authorization) {
    return error(res, 401, "Missing Authorization header");
  }

  // POST /unified/webhook
  if (req.method === "POST" && pathname === "/unified/webhook") {
    const body = await readJson(req);
    if (!body.connection_id || !body.hook_url || body.object_type !== "calendar_botevent") {
      return error(res, 400, "connection_id, hook_url and object_type=calendar_botevent are required");
    }
    const webhook = { id: newId(), ...body, is_healthy: true, created_at: new Date().toISOString() };
    webhooks.set(webhook.id, webhook);
    return send(res, 200, webhook);
  }

  // DELETE /unified/webhook/{id}
  if (req.method === "DELETE" && parts[0] === "unified" && parts[1] === "webhook" && parts[2]) {
    if (!webhooks.delete(parts[2])) return error(res, 404, "Webhook not found");
    return send(res, 200, {});
  }

  // /calendar/{connection_id}/bot[/{id}]
  if (parts[0] === "calendar" && parts[2] === "bot") {
    const connectionId = parts[1];
    const botId = parts[3];

    if (req.method === "POST" && !botId) {
      const body = await readJson(req);
      if (!body.meeting_url) return error(res, 400, "meeting_url is required");
      for (const bot of bots.values()) {
        if (bot.connection_id === connectionId && bot.meeting_url === body.meeting_url) {
          return error(res, 409, "An active bot already exists for this meeting_url");
        }
      }
      const now = new Date().toISOString();
      const bot = {
        id: newId(),
        connection_id: connectionId,
        created_at: now,
        updated_at: now,
        name: body.name,
        meeting_url: body.meeting_url,
        status: "READY",
        status_changes: [{ status: "READY", changed_at: now }],
        participants: [],
        ...(body.recording_config && { recording_config: body.recording_config }),
      };
      bots.set(bot.id, bot);
      send(res, 200, withoutConnectionId(bot));
      replayMeeting(connectionId, bot.id);
      return;
    }

    const bot = botId && bots.get(botId);
    if (!bot) return error(res, 404, "Bot not found");
    if (req.method === "GET") return send(res, 200, withoutConnectionId(bot));
    if (req.method === "DELETE") {
      bots.delete(botId);
      return send(res, 200, {});
    }
  }

  // GET /calendar/{connection_id}/recording/{id}
  if (req.method === "GET" && parts[0] === "calendar" && parts[2] === "recording" && parts[3]) {
    const recording = recordings.get(parts[3]);
    if (!recording) return error(res, 404, "Recording not found");
    return send(res, 200, recording);
  }

  error(res, 404, `No mock for ${req.method} ${pathname}`);
}).listen(PORT, () => console.log(`Mock Unified API on http://localhost:${PORT}`));

function withoutConnectionId({ connection_id, ...bot }: Json): Json {
  return bot;
}

// --- The scripted meeting --------------------------------------------------------

async function replayMeeting(connectionId: string, botId: string): Promise<void> {
  // Fixed timestamps keep the examples' output identical from run to run.
  let second = 0;
  const at = () => `2026-11-16T15:00:${String(second++).padStart(2, "0")}.000Z`;
  let n = 0;
  const event = (type: string, fields: Json = {}) => ({
    id: `${botId}_evt_${++n}`,
    created_at: at(),
    bot_id: botId,
    type,
    ...fields,
  });
  const status = (s: string) => event("BOT_STATUS_CHANGE", { status_change: { status: s, changed_at: at() } });
  const jane = { id: "16778240", name: "Jane Doe", email: "jane@example.com", is_host: true, platform: "zoom" };
  const sam = { id: "16779264", name: "Sam Lee", email: "sam@example.com", is_host: false, platform: "zoom" };
  const said = (who: Json, text: string) =>
    event("TRANSCRIPT_RECEIVED", {
      transcript: { text, participant: who, start_at: at(), end_at: at(), language: "en", is_final: true },
    });

  const deliveries: { data: Json[]; tamper?: boolean; before?: () => void }[] = [];
  const add = (data: Json[], options: { tamper?: boolean; before?: () => void } = {}) =>
    deliveries.push({ data, ...options });

  add([status("IN_CALL_RECORDING")]);
  add([event("PARTICIPANT_JOINED", { participant: jane })]);
  add([event("PARTICIPANT_JOINED", { participant: sam })]);
  add([said(jane, "Welcome, everyone. Let's start with the roadmap.")]);
  // Special characters check that each example verifies the signature over the exact bytes.
  const tricky = said(sam, `Sounds good — Q&A at the end? <3 "café" ✓ 🎉`);
  add([tricky]);
  add([tricky]); // a retry with the same event id: should print once
  add([{ ...said(jane, "This belongs to another bot."), bot_id: "some-other-bot" }]); // should be skipped
  const chat = { message: "Slides: https://example.com/deck?a=1&b=2", sender: sam, is_private: false };
  add([event("CHAT_MESSAGE_RECEIVED", { chat_message: chat })]);
  add([said(jane, "This delivery was tampered with.")], { tamper: true }); // should be rejected with 401
  add([event("PARTICIPANT_LEFT", { participant: sam })]);
  add([status("CALL_ENDED"), status("DONE")]); // one delivery can carry several events
  add([event("RECORDING_READY")], { before: () => saveRecording(botId, [jane, sam]) });

  for (const delivery of deliveries) {
    await new Promise((resolve) => setTimeout(resolve, DELAY_MS));
    if (!bots.has(botId)) return; // bot was removed: stop sending
    delivery.before?.();
    const bot = bots.get(botId)!;
    for (const e of delivery.data) {
      if (e.bot_id === botId && e.status_change) bot.status = e.status_change.status;
    }
    for (const webhook of webhooks.values()) {
      if (webhook.connection_id === connectionId) await deliver(webhook, delivery.data, delivery.tamper);
    }
  }
}

/** Stores the meeting's recording and points the bot at it, as happens before RECORDING_READY. */
function saveRecording(botId: string, [jane, sam]: Json[]): void {
  const line = (who: Json, text: string, second: number) => ({
    text,
    attendee: { name: who.name, email: who.email },
    start_at: `2026-11-16T15:00:${String(second).padStart(2, "0")}.000Z`,
    end_at: `2026-11-16T15:00:${String(second + 2).padStart(2, "0")}.000Z`,
    language: "en",
  });
  const recording = {
    id: newId(),
    created_at: "2026-11-16T15:00:23.000Z",
    start_at: "2026-11-16T15:00:00.000Z",
    end_at: "2026-11-16T15:00:20.000Z",
    web_url: "https://zoom.us/rec/share/mock-recording",
    media: [
      {
        recording_download_url: "https://zoom.us/rec/download/mock-recording.mp4",
        transcript_download_url: "https://zoom.us/rec/download/mock-transcript.vtt",
        transcripts: [
          line(jane, "Welcome, everyone. Let's start with the roadmap.", 4),
          line(sam, `Sounds good — Q&A at the end? <3 "café" ✓ 🎉`, 7),
          line(jane, "Thanks, everyone.", 16),
        ],
      },
    ],
  };
  recordings.set(recording.id, recording);
  bots.get(botId)!.recording_id = recording.id;
}

/** POST one signed delivery, the way Unified does: sig256 = base64(HMAC-SHA256(secret, JSON.stringify(data) + nonce)) */
async function deliver(webhook: Json, data: Json[], tamper = false): Promise<void> {
  const nonce = newId();
  const sig256 = createHmac("sha256", SECRET!).update(JSON.stringify(data) + nonce).digest("base64");
  if (tamper) data = [{ ...data[0], transcript: { ...data[0].transcript, text: "Tampered!" } }];
  const body = {
    data,
    webhook: { id: webhook.id, object_type: "calendar_botevent", event: "created" },
    type: "NATIVE",
    nonce,
    sig256,
  };
  try {
    const res = await fetch(webhook.hook_url, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
      signal: AbortSignal.timeout(5000), // Unified also gives up after 5 seconds
    });
    if (tamper) res.status === 401 ? state.deliveries_rejected++ : state.deliveries_failed++;
    else res.ok ? state.deliveries_ok++ : state.deliveries_failed++;
    console.log(`  → delivered ${data.map((e) => e.type).join(", ")}${tamper ? " (tampered)" : ""}: HTTP ${res.status}`);
  } catch (err) {
    state.deliveries_failed++;
    console.log(`  → delivery failed: ${(err as Error).message}`);
  }
}
