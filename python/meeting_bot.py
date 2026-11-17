#!/usr/bin/env python3
"""
Unified meeting bot: Python example

Sends a bot into a Zoom, Microsoft Teams, Webex or Google Meet meeting with
Unified's Calendar API (calendar_bot), prints every calendar_botevent webhook
it receives, and fetches the recording and transcript when they're ready.

    python3 meeting_bot.py [--name "Meeting Bot"] <meeting_url>

How it works:
  1. Start a local HTTP server to receive webhooks (PUBLIC_URL forwards to it).
  2. Subscribe to calendar_botevent webhooks for the connection.
  3. Create the bot for the meeting URL.
  4. Verify and print each event as it arrives. On RECORDING_READY, fetch the
     recording and print its transcript.
  5. On Ctrl+C, remove the bot (it leaves the meeting) and the webhook.

Uses the official SDK (pip install -r requirements.txt). Python 3.10+.
"""
import argparse
import base64
import hashlib
import hmac
import json
import os
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer

from unified_python_sdk import UnifiedTo
from unified_python_sdk.models import shared

WEBHOOK_PATH = "/bot-events"

# --- Configuration ------------------------------------------------------------------

REQUIRED = ["UNIFIED_API_KEY", "UNIFIED_CONNECTION_ID", "UNIFIED_WORKSPACE_SECRET", "PUBLIC_URL"]

CONNECTION_ID = os.environ.get("UNIFIED_CONNECTION_ID", "")
WORKSPACE_SECRET = os.environ.get("UNIFIED_WORKSPACE_SECRET", "")
PUBLIC_URL = os.environ.get("PUBLIC_URL", "").rstrip("/")
PORT = int(os.environ.get("PORT", "3000"))

unified = UnifiedTo(
    security=shared.Security(jwt=os.environ.get("UNIFIED_API_KEY", "")),
    server_url=os.environ.get("UNIFIED_API_URL", "https://api.unified.to"),  # EU: api-eu, AU: api-au
)

bot_id = None  # set once our bot exists
seen = set()  # events already printed


def log(message):
    """Status messages go to stderr, so stdout carries only events."""
    print(message, file=sys.stderr, flush=True)


# --- Webhook handling ---------------------------------------------------------------
# Bot events arrive only as webhooks, so the SDK has no model for them: they're plain
# dicts. Full model: https://docs.unified.to/calendar/botevent/model


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

    # Unified retries failed deliveries, so skip events we've already handled.
    # Event ids alone can repeat, so compare whole events.
    key = json.dumps(event, sort_keys=True)
    if key in seen:
        return
    seen.add(key)

    time = (event.get("created_at") or "")[11:19] or "--:--:--"  # HH:MM:SS (UTC)
    kind = event.get("type") or "UNKNOWN"
    print(f"{time}  {kind:<27}  {describe(event)}".rstrip(), flush=True)

    if kind == "RECORDING_READY":
        try:
            print_recording(event["bot_id"])
        except Exception as err:
            log(f"Could not fetch the recording: {err}")


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


# --- Lesson 2: the recording and transcript -----------------------------------------


def print_recording(recording_bot_id):
    """The bot links to its recording once RECORDING_READY fires. Fetch it and print the transcript."""
    indent = " " * 10
    bot = unified.calendar.get_calendar_bot(
        request={"connection_id": CONNECTION_ID, "id": recording_bot_id}
    ).calendar_bot
    if not bot.recording_id:
        print(f"{indent}No recording_id on the bot yet", flush=True)
        return

    recording = unified.calendar.get_calendar_recording(
        request={"connection_id": CONNECTION_ID, "id": bot.recording_id}
    ).calendar_recording
    print(f"{indent}Recording {recording.id}: {recording.web_url or '(no web_url)'}", flush=True)
    for media in recording.media or []:
        for line in media.transcripts or []:
            speaker = line.attendee and (line.attendee.name or line.attendee.email)
            print(f"{indent}  {speaker or 'unknown'}: {line.text}", flush=True)
        if not media.transcripts and media.transcript_download_url:
            print(f"{indent}  Transcript file: {media.transcript_download_url}", flush=True)


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
    global bot_id

    parser = argparse.ArgumentParser(description="Join a meeting with a Unified bot and print its events.")
    parser.add_argument("meeting_url")
    parser.add_argument("--name", default="Meeting Bot", help="shown to people in the meeting")
    args = parser.parse_args()

    missing = [name for name in REQUIRED if not os.environ.get(name)]
    if missing:
        log(f"Missing environment variables: {', '.join(missing)} (see .env.example)")
        sys.exit(2)

    # 1. Listen for webhooks. The socket accepts connections from here on;
    #    requests are handled once serve_forever() runs below.
    server = HTTPServer(("", PORT), WebhookHandler)
    log(f"Listening for webhooks on http://localhost:{PORT}{WEBHOOK_PATH}")

    # 2. Subscribe to bot events. One webhook carries events for every bot on the connection.
    webhook = unified.webhook.create_unified_webhook(request={
        "webhook": {
            "connection_id": CONNECTION_ID,
            "object_type": "calendar_botevent",
            "event": "created",
            "webhook_type": "native",
            "hook_url": PUBLIC_URL + WEBHOOK_PATH,
        },
    }).webhook
    log(f"Webhook {webhook.id} subscribed → {PUBLIC_URL}{WEBHOOK_PATH}")

    try:
        # 3. Send the bot into the meeting.
        calendar_bot = {"name": args.name, "meeting_url": args.meeting_url}
        if "zoom.us" in args.meeting_url:
            # Zoom only: opt in to live captions and chat. Other platforms reject recording_config (501).
            calendar_bot["recording_config"] = {"record_transcript": True, "record_chat": True}
        bot = unified.calendar.create_calendar_bot(
            request={"connection_id": CONNECTION_ID, "calendar_bot": calendar_bot}
        ).calendar_bot
        bot_id = bot.id
        log(f"Bot {bot.id} created ({bot.status}). Printing events; press Ctrl+C to stop.\n")

        # 4. Handle webhooks until Ctrl+C.
        try:
            server.serve_forever()
        except KeyboardInterrupt:
            pass

        # 5. Remove the bot. It leaves the meeting and stops sending events.
        log(f"\nRemoving bot {bot.id}")
        try:
            unified.calendar.remove_calendar_bot(request={"connection_id": CONNECTION_ID, "id": bot.id})
        except Exception as err:
            log(str(err))
    finally:
        log(f"Removing webhook {webhook.id}")
        try:
            unified.webhook.remove_unified_webhook(request={"id": webhook.id})
        except Exception as err:
            log(str(err))
        server.server_close()


if __name__ == "__main__":
    try:
        main()
    except KeyboardInterrupt:  # Ctrl+C during setup
        sys.exit(130)
    except Exception as err:
        log(f"Error: {err}")
        sys.exit(1)
