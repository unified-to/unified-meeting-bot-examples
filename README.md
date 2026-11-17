# Unified meeting bot examples

These are small command-line meeting bots built on [Unified](https://unified.to)'s Calendar API with the official SDKs for **TypeScript, Python and Go**. Each one sends a bot into a Zoom, Microsoft Teams, Webex or Google Meet meeting and prints what it hears about: status changes, people joining and leaving, transcripts and chat. When the recording is ready, it fetches it and prints the transcript.

All three programs follow the same steps in the same order and print the same output, so you can read the language you know and compare it with the others.

```
$ node src/meeting-bot.ts https://us02web.zoom.us/j/81234567890
Listening for webhooks on http://localhost:3000/bot-events
Webhook 6735f0c2a9e4b5d1c0a1b2c4 subscribed → https://f00d.ngrok.app/bot-events
Bot 6730f1c2a9e4b5d1c0a1b2c3 created (READY). Printing events; press Ctrl+C to stop.

15:00:01  BOT_STATUS_CHANGE            IN_CALL_RECORDING
15:00:02  PARTICIPANT_JOINED           Jane Doe (host)
15:00:03  PARTICIPANT_JOINED           Sam Lee
15:00:06  TRANSCRIPT_RECEIVED          Jane Doe: Welcome, everyone. Let's start with the roadmap.
15:00:13  CHAT_MESSAGE_RECEIVED        Sam Lee: Slides: https://example.com/deck
15:00:17  PARTICIPANT_LEFT             Sam Lee
15:00:19  BOT_STATUS_CHANGE            CALL_ENDED
15:00:21  BOT_STATUS_CHANGE            DONE
15:00:22  RECORDING_READY
          Recording 6735f1d0a9e4b5d1c0a1b2c5: https://zoom.us/rec/share/abc123
            Jane Doe: Welcome, everyone. Let's start with the roadmap.
            Sam Lee: Sounds good. Q&A at the end?
            Jane Doe: Thanks, everyone.
^C
Removing bot 6730f1c2a9e4b5d1c0a1b2c3
Removing webhook 6735f0c2a9e4b5d1c0a1b2c4
```

## How it works

Unified only delivers bot events as webhooks, so each example runs a small web server and asks Unified to send events to it.

**Lesson 1: follow a meeting**

1. **Listen.** Start an HTTP server on `PORT` (default 3000) at `/bot-events`. Unified must be able to reach it, so you expose it with a tunnel and set `PUBLIC_URL`.
2. **Subscribe.** Create a webhook (`createUnifiedWebhook`, `POST /unified/webhook`) with `object_type: "calendar_botevent"`, `event: "created"`, `webhook_type: "native"` and `hook_url: PUBLIC_URL + "/bot-events"`.
3. **Create the bot.** Call `createCalendarBot` (`POST /calendar/{connection_id}/bot`) with `meeting_url` and `name`. For Zoom, also send `recording_config: { record_transcript: true, record_chat: true }` (see [Things to know](#things-to-know)).
4. **Handle deliveries.** Check the `sig256` signature and reply `200` quickly. Then, for each event in `data`, skip events from other bots, skip repeats, and print the rest.
5. **Clean up.** On Ctrl+C, call `removeCalendarBot` (the bot leaves the meeting) and `removeUnifiedWebhook`.

**Lesson 2: get the recording and transcript**

6. **On `RECORDING_READY`,** read the bot again (`getCalendarBot`) to get its `recording_id`, then fetch the recording (`getCalendarRecording`, `GET /calendar/{connection_id}/recording/{id}`). Print its link and, for each `media` item, its `transcripts` (speaker and text) or the `transcript_download_url`. This runs after the webhook reply, because it calls the API.

## Before you start

1. **Set up a meeting-bot integration in Unified.** There are four: `zoommeetingbot`, `microsoftteamsmeetingbot`, `webexmeetingbot` and `googlemeetmeetingbot`. They're separate from the regular Zoom, Teams, Webex and Meet integrations, and each one needs its own app on the meeting platform. Follow the setup guide for yours: [Zoom](https://docs.unified.to/guides/how_to_set_up_a_zoom_meeting_bot_with_unified), [Microsoft Teams](https://docs.unified.to/guides/how_to_set_up_a_microsoft_teams_meeting_bot_with_unified), [Webex](https://docs.unified.to/guides/how_to_set_up_a_webex_meeting_bot_with_unified) or [Google Meet](https://docs.unified.to/guides/how_to_set_up_a_google_meet_meeting_bot_with_unified).
2. **Authorize a connection** with the `calendar_bot_read`, `calendar_bot_write` and `calendar_botevent_read` permissions, plus `calendar_recording_read` for lesson 2. Copy its connection ID.
3. **Copy your API key and workspace secret** from **Settings → API Keys** in the Unified app.
4. **Start a tunnel** to port 3000, for example `ngrok http 3000` or `cloudflared tunnel --url http://localhost:3000`, and copy its HTTPS URL.
5. **Configure.** Run `cp .env.example .env`, fill in the values, then load them into your shell with `set -a; source .env; set +a`.

## Run

| Language | Folder | Needs | Install | Run (in the folder) |
|---|---|---|---|---|
| TypeScript | [`typescript/`](typescript) | Node.js 22.18+ | `npm install` | `node src/meeting-bot.ts <meeting_url>` |
| Python | [`python/`](python) | Python 3.10+ | `pip install -r requirements.txt` | `python3 meeting_bot.py <meeting_url>` |
| Go | [`go/`](go) | Go 1.26.10+ | (automatic) | `go run . <meeting_url>` |

Each example uses its language's official Unified SDK for API calls. The webhook side (the server and signature check) uses the standard library, because bot events only arrive as webhooks and the SDKs don't include a type for them.

Options: `--name "Meeting Bot"` sets the name people see in the meeting.

Status messages go to stderr and events go to stdout, so `> events.log` captures only events.

## Try it without a meeting

[`mock-unified/`](mock-unified) is a stand-in for the Unified API. When a bot is created, it plays back a short scripted meeting as signed webhooks, then serves the recording. The script includes a retried delivery, an event from another bot and a tampered delivery, so you can watch each example ignore or reject them. You don't need a tunnel or any accounts.

```sh
# Terminal 1
UNIFIED_WORKSPACE_SECRET=test node mock-unified/server.ts

# Terminal 2
export UNIFIED_API_KEY=test UNIFIED_CONNECTION_ID=test UNIFIED_WORKSPACE_SECRET=test \
       UNIFIED_API_URL=http://localhost:4000 PUBLIC_URL=http://localhost:3000
cd python && python3 meeting_bot.py https://us02web.zoom.us/j/123
```

`./scripts/test-all.sh` runs all three examples this way. It checks that they print identical output, reject the tampered delivery and clean up after themselves. CI runs it on every push.

## Events

| Event | Zoom | Teams | Webex | Google Meet |
|---|---|---|---|---|
| `BOT_STATUS_CHANGE` | ✓ | ✓ | ✓ | ✓ |
| `PARTICIPANT_JOINED`, `PARTICIPANT_LEFT` | ✓ | ✓ | ✓ | ✓ |
| `PARTICIPANT_SPEECH_ON`, `_WEBCAM_ON/OFF`, `_SCREENSHARE_ON/OFF` | ✓ | | | |
| `TRANSCRIPT_RECEIVED` | live captions | after the meeting | after the meeting | after the meeting |
| `CHAT_MESSAGE_RECEIVED` | ✓ (needs `record_chat`) | | | |
| `RECORDING_STARTED`, `RECORDING_STOPPED` | | | | ✓ |
| `RECORDING_READY` | ✓ | ✓ | ✓ | ✓ |

Bot statuses move differently on each platform:

- **Zoom:** `READY` → `IN_CALL_RECORDING` → `CALL_ENDED` → `DONE`
- **Teams:** `READY` → `CALL_ENDED` → `DONE`
- **Webex:** `READY` → `IN_CALL_NOT_RECORDING` → `CALL_ENDED`
- **Google Meet:** `READY` → `IN_CALL_NOT_RECORDING` ⇄ `IN_CALL_RECORDING` → `CALL_ENDED`

`FATAL` means the bot stopped because of an error, or because it was deleted. Check `sub_code` for the reason.

## Things to know

- **Create one webhook per connection in production.** To stay self-contained, these examples create a webhook when they start and remove it when they exit. In an app, create the `calendar_botevent` webhook once, when the connection is made, and route events by `bot_id`. A single webhook receives events for every bot on its connection, and events you miss can't be fetched later.
- **Verify every delivery.** `sig256` is `base64(HMAC-SHA256(workspace_secret, JSON.stringify(data) + nonce))`. The signature covers `data` exactly as it was sent, and each example shows how to get those bytes right in its language.
- **Reply within 5 seconds.** Unified retries a delivery that fails, and it marks a webhook unhealthy if deliveries keep failing. Do slow work, like summaries or CRM updates, after replying. Make handlers idempotent: event ids can repeat, so don't deduplicate on `id` alone. The examples compare whole events.
- **Zoom needs `recording_config`** to send live captions and chat, and RTMS has to start in the meeting, either through the host or an auto-start setting. The other platforms reject `recording_config` with `501`. The examples check for `zoom.us` in the meeting URL; in an app, check the connection's `integration_type` instead.
- **Teams and Google Meet only follow meetings the connected user organizes.** On Teams, create the bot before the meeting ends.
- **Keep listening after `CALL_ENDED`.** Teams, Webex and Meet send transcripts after the meeting, Webex and Meet bots never reach `DONE`, and Zoom sends a second `RECORDING_READY` once its transcript file is complete. Watch for `RECORDING_READY` rather than a final status.
- **One active bot per meeting URL per connection.** Creating a second one returns `409`.
- **Regions:** set `UNIFIED_API_URL` to `https://api-eu.unified.to` or `https://api-au.unified.to` for EU or AU workspaces.

## Learn more

- [How to build a meeting bot with Unified](https://docs.unified.to/guides/how_to_build_a_meeting_bot_with_unified_zoom_microsoft_teams_webex_google_meet)
- [Create a bot](https://docs.unified.to/calendar/bot/Create_a_bot), the [bot event model](https://docs.unified.to/calendar/botevent/model) and the [recording model](https://docs.unified.to/calendar/recording/model)
- [Webhooks](https://docs.unified.to/reference/webhooks) and [SDKs](https://docs.unified.to/reference/sdks)

## License

[MIT](LICENSE)
