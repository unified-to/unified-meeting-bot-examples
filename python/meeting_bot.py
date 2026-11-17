#!/usr/bin/env python3
"""
Unified meeting bot: Python example

Sends a bot into a Zoom, Microsoft Teams, Webex or Google Meet meeting with
Unified's Calendar API (calendar_bot), then prints every calendar_botevent
webhook it receives.

    python3 meeting_bot.py [--name "Meeting Bot"] [--json] <meeting_url>

How it works:
  1. Start a local HTTP server to receive webhooks (PUBLIC_URL forwards to it).
  2. Subscribe to calendar_botevent webhooks for the connection.
  3. Create the bot for the meeting URL.
  4. Verify and print each event as it arrives.
  5. On Ctrl+C, remove the bot (it leaves the meeting) and the webhook.

Standard library only: Python 3.9+.
"""
import argparse
import base64
import hashlib
import hmac
import json
import os
import sys
import urllib.error
import urllib.request
from http.server import BaseHTTPRequestHandler, HTTPServer

WEBHOOK_PATH = "/bot-events"

# --- Configuration ------------------------------------------------------------------

REQUIRED = ["UNIFIED_API_KEY", "UNIFIED_CONNECTION_ID", "UNIFIED_WORKSPACE_SECRET", "PUBLIC_URL"]

API_URL = os.environ.get("UNIFIED_API_URL", "https://api.unified.to")
API_KEY = os.environ.get("UNIFIED_API_KEY", "")
CONNECTION_ID = os.environ.get("UNIFIED_CONNECTION_ID", "")
WORKSPACE_SECRET = os.environ.get("UNIFIED_WORKSPACE_SECRET", "")
PUBLIC_URL = os.environ.get("PUBLIC_URL", "").rstrip("/")
PORT = int(os.environ.get("PORT", "3000"))

bot_id = None  # set once our bot exists
print_json = False  # --json: print each event as raw JSON instead
seen = set()  # Unified retries failed deliveries, so skip repeats


def log(message):
    """Status messages go to stderr, so stdout carries only events."""
    print(message, file=sys.stderr, flush=True)


# --- Unified API --------------------------------------------------------------------


def unified(method, path, body=None):
    request = urllib.request.Request(
        API_URL + path,
        method=method,
        data=None if body is None else json.dumps(body).encode(),
        headers={"Authorization": f"Bearer {API_KEY}", "Content-Type": "application/json"},
    )
    try:
        with urllib.request.urlopen(request) as response:
            text = response.read().decode()
    except urllib.error.HTTPError as err:
        raise RuntimeError(f"{method} {path} → {err.code} {err.read().decode()}") from None
    return json.loads(text) if text else {}


# --- Webhook handling ---------------------------------------------------------------


def is_signature_valid(delivery):
    """sig256 = base64(HMAC-SHA256(workspace secret, JSON.stringify(data) + nonce))"""
    # Serialize `data` the way JavaScript's JSON.stringify does: compact, non-ASCII kept as-is.
    data = json.dumps(delivery.get("data"), separators=(",", ":"), ensure_ascii=False)
    message = (data + str(delivery.get("nonce", ""))).encode()
    digest = hmac.new(WORKSPACE_SECRET.encode(), message, hashlib.sha256).digest()
    expected = base64.b64encode(digest)
    return hmac.compare_digest(expected, str(delivery.get("sig256", "")).encode())  # timing-safe


def handle_event(event):
    # The webhook receives events for every bot on this connection. Keep ours.
    if bot_id and event.get("bot_id") != bot_id:
        return

    # Deduplicate on id. Google Meet can reuse ids for status changes, so add changed_at.
    if event.get("id"):
        key = (event["id"], (event.get("status_change") or {}).get("changed_at"))
        if key in seen:
            return
        seen.add(key)

    if print_json:
        print(json.dumps(event, separators=(",", ":"), ensure_ascii=False), flush=True)
        return
    time = (event.get("created_at") or "")[11:19] or "--:--:--"  # HH:MM:SS (UTC)
    kind = event.get("type") or "UNKNOWN"
    print(f"{time}  {kind:<27}  {describe(event)}".rstrip(), flush=True)


def describe(event):
    """One line of detail for each event type."""
    kind = event.get("type")
    if kind == "BOT_STATUS_CHANGE":
        change = event.get("status_change") or {}
        sub_code = change.get("sub_code") and f"({change['sub_code']})"
        return " ".join(part for part in (change.get("status"), sub_code, change.get("message")) if part)
    if kind in ("TRANSCRIPT_RECEIVED", "TRANSCRIPT_PARTIAL"):
        transcript = event.get("transcript") or {}
        return f"{name_of(transcript.get('participant'))}: {transcript.get('text')}"
    if kind == "CHAT_MESSAGE_RECEIVED":
        chat = event.get("chat_message") or {}
        return f"{name_of(chat.get('sender'))}: {chat.get('message')}"
    # PARTICIPANT_* events carry a participant; RECORDING_* events carry nothing extra
    participant = event.get("participant")
    if not participant:
        return ""
    return name_of(participant) + (" (host)" if participant.get("is_host") else "")


def name_of(participant):
    p = participant or {}
    return p.get("name") or p.get("email") or p.get("id") or "unknown"


class WebhookHandler(BaseHTTPRequestHandler):
    def do_POST(self):
        if self.path != WEBHOOK_PATH:
            return self.reply(404)
        try:
            delivery = json.loads(self.rfile.read(int(self.headers.get("Content-Length", 0))))
        except ValueError:
            return self.reply(400)

        if not is_signature_valid(delivery):
            log("Rejected a webhook with an invalid signature")
            return self.reply(401)

        # Reply first: Unified waits at most 5 seconds. Do slow work after this.
        self.reply(200, b"ok")
        for event in delivery.get("data") or []:
            handle_event(event)

    def reply(self, status, body=b""):
        self.send_response(status)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *args):
        pass  # silence the default access log


# --- Main ---------------------------------------------------------------------------


def main():
    global bot_id, print_json

    parser = argparse.ArgumentParser(description="Join a meeting with a Unified bot and print its events.")
    parser.add_argument("meeting_url")
    parser.add_argument("--name", default="Meeting Bot", help="shown to people in the meeting")
    parser.add_argument("--json", action="store_true", help="print each event as raw JSON")
    args = parser.parse_args()
    print_json = args.json

    missing = [name for name in REQUIRED if not os.environ.get(name)]
    if missing:
        log(f"Missing environment variables: {', '.join(missing)} (see .env.example)")
        sys.exit(2)

    # 1. Listen for webhooks. The socket accepts connections from here on;
    #    requests are handled once serve_forever() runs below.
    server = HTTPServer(("", PORT), WebhookHandler)
    log(f"Listening for webhooks on http://localhost:{PORT}{WEBHOOK_PATH}")

    # 2. Subscribe to bot events. One webhook carries events for every bot on the connection.
    webhook = unified("POST", "/unified/webhook", {
        "connection_id": CONNECTION_ID,
        "object_type": "calendar_botevent",
        "event": "created",
        "webhook_type": "native",
        "hook_url": PUBLIC_URL + WEBHOOK_PATH,
    })
    log(f"Webhook {webhook['id']} subscribed → {PUBLIC_URL}{WEBHOOK_PATH}")

    try:
        # 3. Send the bot into the meeting.
        body = {"name": args.name, "meeting_url": args.meeting_url}
        if "zoom.us" in args.meeting_url:
            # Zoom only: opt in to live captions and chat. Other platforms reject recording_config (501).
            body["recording_config"] = {"record_transcript": True, "record_chat": True}
        bot = unified("POST", f"/calendar/{CONNECTION_ID}/bot", body)
        bot_id = bot["id"]
        log(f"Bot {bot_id} created ({bot.get('status')}). Printing events; press Ctrl+C to stop.\n")

        # 4. Handle webhooks until Ctrl+C.
        try:
            server.serve_forever()
        except KeyboardInterrupt:
            pass

        # 5. Remove the bot. It leaves the meeting and stops sending events.
        log(f"\nRemoving bot {bot_id}")
        try:
            unified("DELETE", f"/calendar/{CONNECTION_ID}/bot/{bot_id}")
        except RuntimeError as err:
            log(str(err))
    finally:
        log(f"Removing webhook {webhook['id']}")
        try:
            unified("DELETE", f"/unified/webhook/{webhook['id']}")
        except RuntimeError as err:
            log(str(err))
        server.server_close()


if __name__ == "__main__":
    try:
        main()
    except KeyboardInterrupt:  # Ctrl+C during setup
        sys.exit(130)
    except (RuntimeError, OSError) as err:
        log(f"Error: {err}")
        sys.exit(1)
