# Setup guide

This guide takes you from nothing to a bot following a real meeting. Most of the time goes into step 2, where you create an app in your meeting platform's developer console. Each step links to the full guide on [docs.unified.to](https://docs.unified.to), which has the details. For how the pieces fit together, see [How to build a meeting bot with Unified](https://docs.unified.to/guides/how_to_build_a_meeting_bot_with_unified_zoom_microsoft_teams_webex_google_meet).

**You'll need:**

- A [Unified](https://app.unified.to) workspace
- Admin access to your meeting platform's developer console: the Zoom App Marketplace, the Microsoft Entra admin center, the Webex Developer Portal or the Google Cloud console
- An account on that platform that can host meetings, with the plan features listed for your platform in step 2
- Node.js 22.18+, Python 3.10+ or Go 1.26.10+, for whichever example you'll run
- A tunnel tool, such as [ngrok](https://ngrok.com) or [cloudflared](https://github.com/cloudflare/cloudflared), so Unified can reach your machine

Contents:

1. [Collect your Unified credentials](#1-collect-your-unified-credentials)
2. [Set up your meeting platform](#2-set-up-your-meeting-platform): [Zoom](#zoom), [Microsoft Teams](#microsoft-teams), [Webex](#webex), [Google Meet](#google-meet)
3. [Connect an account](#3-connect-an-account)
4. [Start a tunnel](#4-start-a-tunnel)
5. [Configure and run](#5-configure-and-run)
6. [Test with a real meeting](#6-test-with-a-real-meeting)
7. [Troubleshooting](#troubleshooting)

## 1. Collect your Unified credentials

In the [Unified app](https://app.unified.to), open **Settings → API Keys** and copy:

- **API key**: this goes in `UNIFIED_API_KEY`.
- **Workspace secret**: this goes in `UNIFIED_WORKSPACE_SECRET`. The examples use it to check that webhooks really come from Unified.
- **Workspace ID**: you need it for the Zoom webhook URL in step 2 and the authorization link in step 3.

If your workspace is in the EU or Australia, swap `api.unified.to` for `api-eu.unified.to` or `api-au.unified.to` in every URL below (and `app.unified.to` for `app-eu.unified.to` or `app-au.unified.to`), and set `UNIFIED_API_URL` to match.

## 2. Set up your meeting platform

Each platform has its own meeting-bot integration in Unified: `zoommeetingbot`, `microsoftteamsmeetingbot`, `webexmeetingbot` and `googlemeetmeetingbot`. They're separate from the regular Zoom, Teams, Webex and Google Meet integrations, and a connection to a regular integration can't create bots.

Unified has no shared credentials for meeting bots, so you create your own app on the platform and enter its credentials in Unified. Follow the section for your platform. Each one is a checklist; the linked guide has the full instructions.

### Zoom

Full guide: [How to set up a Zoom meeting bot with Unified](https://docs.unified.to/guides/how_to_set_up_a_zoom_meeting_bot_with_unified)

In the [Zoom App Marketplace](https://marketplace.zoom.us/develop/applications):

1. Create a **user-managed General app**. Your account needs an active **Zoom Developer Pack with credits**, because the live stream (RTMS) uses them.
2. Add the redirect URL `https://api.unified.to/oauth/code`, then copy the **Client ID** and **Client Secret**.
3. Add these scopes: `user:read:user`, `meeting:read:meeting`, `meeting:read:meeting_transcript` (the only RTMS scope you need), `cloud_recording:read:list_user_recordings`, `cloud_recording:read:list_recording_files` and `meeting:read:list_meetings`.
4. Copy the **Secret Token** from **Manage → your app → Features → Access**. This is not the Client Secret.
5. In Unified, open **Integrations**, activate **Zoom (meeting bot)**, and enter the Client ID, Client Secret and Secret Token.
6. Back in Zoom, under **Event Subscription**, set the endpoint URL to `https://api.unified.to/webhook/workspace/zoommeetingbot?workspace_id=<YOUR_WORKSPACE_ID>` and subscribe to **RTMS started in Meeting** and **All Recordings have completed**. You can also add **Recording transcript files have completed**, which needs a Business, Education or Enterprise plan. Do step 5 first, because Zoom checks the endpoint when you save it.

For the meeting itself:

- The host needs **cloud recording**, which requires a Pro or higher account.
- **RTMS auto-start** must be on. Either an account admin turns on sharing realtime meeting content with apps under **Account Settings → Zoom Apps**, or the host adds your app under **Settings → Zoom Apps → auto-start**. Without this, the bot stays `READY`.

### Microsoft Teams

Full guide: [How to set up a Microsoft Teams meeting bot with Unified](https://docs.unified.to/guides/how_to_set_up_a_microsoft_teams_meeting_bot_with_unified)

In the [Microsoft Entra admin center](https://entra.microsoft.com):

1. Go to **Entra ID → App registrations → New registration**. Set supported account types to **Multiple Entra ID tenants**; a single-tenant app fails with `AADSTS50194`. Add a **Web** redirect URI: `https://api.unified.to/oauth/code`.
2. Create a client secret and copy its **Value** (not the Secret ID), along with the **Application (client) ID**. Secrets expire, so note the date.
3. Add Microsoft Graph permissions:
   - **Delegated:** `User.Read`, `openid`, `email`, `profile`, `offline_access`, `OnlineMeetings.Read`, `Calendars.Read`, `OnlineMeetingRecording.Read.All`, `OnlineMeetingTranscript.Read.All` and `OnlineMeetingAiInsight.Read.All`
   - **Application:** `CallRecords.Read.All`
4. Select **Grant admin consent** for all of them. Every other tenant that connects needs a Privileged Role Administrator or Global Administrator to consent to the app as well.
5. In Unified, open **Integrations**, activate **Microsoft Teams (meeting bot)**, and enter the client ID and client secret.

For the meeting itself:

- It must be a **scheduled meeting that the connected user organizes**. Ad hoc calls and private-channel meetings aren't covered.
- **Recording and transcription** must be turned on in the meeting.
- Recordings and transcripts can take **up to 4 hours** to arrive after the meeting.

### Webex

Full guide: [How to set up a Webex meeting bot with Unified](https://docs.unified.to/guides/how_to_set_up_a_webex_meeting_bot_with_unified)

In the [Webex Developer Portal](https://developer.webex.com):

1. Under **My Webex Apps**, create an **Integration**, and add the redirect URI `https://api.unified.to/oauth/code`.
2. Select these scopes: `meeting:schedules_read`, `meeting:participants_read`, `meeting:recordings_read` and `meeting:transcripts_read`.
3. Copy the **Client ID** and **Client Secret**. Webex shows the secret only once.
4. In Unified, open **Integrations**, activate **Webex (meeting bot)**, and enter the Client ID and Client Secret.

For the meeting itself:

- The **connected user must host** the meeting, or the recording must be shared with them.
- Recordings need **cloud recording**, which the Webex Free plan doesn't include. Transcripts also need Webex Assistant, Closed Captions or the Cisco AI Assistant.
- Processing can take **up to 24 hours** after the meeting.

### Google Meet

Full guide: [How to set up a Google Meet meeting bot with Unified](https://docs.unified.to/guides/how_to_set_up_a_google_meet_meeting_bot_with_unified)

In the [Google Cloud console](https://console.cloud.google.com), using one project you administer for everything:

1. Enable the **Google Meet REST API**, **Google Workspace Events API** and **Cloud Pub/Sub API**.
2. Configure the **OAuth consent screen**. The `meetings.space.readonly` scope is sensitive, so Google must verify your app before people outside your organization can connect.
3. Create an **OAuth client** of type Web application with the redirect URI `https://api.unified.to/oauth/code`, then copy the **Client ID** and **Client Secret**.
4. Under **IAM & Admin**, create a **service account**, grant it **Pub/Sub Admin** (`roles/pubsub.admin`) in the same project, and create a JSON key. Note the key's `client_email` and `private_key`.
5. In Unified, open **Integrations**, activate **Google Meet (meeting bot)**, and enter the Client ID and Client Secret. The remaining fields appear after that: enter the **project ID**, the **service account email**, and the **private key** in the PEM field, including its `BEGIN` and `END` lines.

For the meeting itself:

- The **connected user must own the meeting**, usually as its organizer. A bot for anyone else's meeting stays `READY`.
- Recordings and transcripts exist only if someone starts them, or auto-recording is on. Google saves them to the organizer's Drive.
- No bot appears in the call, and the bot's status ends at `CALL_ENDED`.
- Each bot follows only the first conference in its meeting space; later meetings need a new bot.

## 3. Connect an account

A connection is one user's authorized account on the meeting platform. The bot can only follow meetings that account can see, so connect the account that will host your test meeting.

Open this link in a browser, replacing `<YOUR_WORKSPACE_ID>` and `<INTEGRATION>` (one of `zoommeetingbot`, `microsoftteamsmeetingbot`, `webexmeetingbot` or `googlemeetmeetingbot`):

```
https://api.unified.to/unified/integration/auth/<YOUR_WORKSPACE_ID>/<INTEGRATION>?redirect=1&env=Production&scopes=calendar_bot_read,calendar_bot_write,calendar_botevent_read,calendar_recording_read&success_redirect=https://app.unified.to/connected&failure_redirect=https://app.unified.to/connected
```

Sign in and approve the permissions. The page you land on shows the **connection ID**, which goes in `UNIFIED_CONNECTION_ID`. You can also find it under **Connections** in the Unified app. If you activated the integration in an environment other than Production, change `env` to match.

The permissions requested are:

| Permission | Used for |
|---|---|
| `calendar_bot_read`, `calendar_bot_write` | Creating, reading and removing the bot |
| `calendar_botevent_read` | Receiving bot events by webhook |
| `calendar_recording_read` | Lesson 2: fetching the recording and transcript |

In your own product, your users connect through Unified's [embedded Authorization component](https://docs.unified.to/concepts/embedded-components) or an authorization link like this one, and you store each connection ID.

## 4. Start a tunnel

Unified delivers bot events as webhooks, so it needs a public HTTPS address that reaches the example on your machine. Start a tunnel to port 3000 and leave it running:

```sh
ngrok http 3000
# or
cloudflared tunnel --url http://localhost:3000
```

Copy the `https://…` address it prints. That's your `PUBLIC_URL`. Quick-tunnel addresses can change when you restart the tunnel, so update `PUBLIC_URL` when that happens.

## 5. Configure and run

From the repo's root folder:

```sh
cp .env.example .env      # then fill in the values from steps 1, 3 and 4
set -a; source .env; set +a
```

Then install and run the example for your language. Run it in the same terminal, so it sees the variables:

| Language | Install (in the folder) | Run (in the folder) |
|---|---|---|
| [TypeScript](typescript) | `npm install` | `node src/meeting-bot.ts <meeting_url>` |
| [Python](python) | `python3 -m venv .venv && . .venv/bin/activate && pip install -r requirements.txt` | `python3 meeting_bot.py <meeting_url>` |
| [Go](go) | (nothing: `go run` downloads the SDK) | `go run . <meeting_url>` |

Before you try a real meeting, you can check your install against the mock server, which needs no accounts. See [Try it without a meeting](README.md#try-it-without-a-meeting).

## 6. Test with a real meeting

1. **Schedule a meeting** that the connected account hosts, organizes or owns, and copy its join link.
2. **Start the example before the meeting starts:** for example, `node src/meeting-bot.ts "<join link>"`. Teams, Webex and Google Meet only send events to webhooks that existed when the meeting began.
3. **Start the meeting.** Turn on recording and transcription if your platform needs it (see step 2).
4. **Watch the events.** Zoom sends live captions and chat during the meeting. The other platforms send status and participant changes live, and transcripts after the meeting.
5. **Wait for `RECORDING_READY`** before you press Ctrl+C. It can arrive hours after the meeting: up to 4 hours on Teams and up to 24 hours on Webex. Ctrl+C removes the bot and the webhook, so nothing arrives after that.

## Troubleshooting

The platform guides list more problems and fixes. These are the most common:

| What you see | Likely cause | Fix |
|---|---|---|
| `Missing environment variables` | `.env` isn't loaded in this terminal | Run `set -a; source .env; set +a` in the same terminal, from the repo root |
| `Rejected a webhook with an invalid signature` | `UNIFIED_WORKSPACE_SECRET` is wrong, for example the API key was pasted instead | Copy the workspace secret again from **Settings → API Keys** |
| No events at all | The tunnel isn't running or `PUBLIC_URL` is out of date, or the example started after the meeting did | Check the tunnel's request log, update `PUBLIC_URL`, and restart the example before the meeting starts |
| `409` when creating the webhook (Google Meet) | Google Meet allows one `calendar_botevent` webhook per connection, and one already exists, for example from an interrupted run | Delete the old webhook under **Webhooks** in the Unified app, or use another connection |
| `409` when creating the bot | An active bot already exists for this meeting link on this connection | Wait for it to end, remove it, or use another meeting |
| `400` when creating the bot (Zoom) | The Secret Token is missing in Unified | Add it to the Zoom (meeting bot) integration |
| `501` when creating the bot | `recording_config` was sent to Teams, Webex or Google Meet, which happens if the link contains `zoom.us` but the connection isn't Zoom | Use a meeting link from the connection's own platform |
| Bot stays `READY` | Zoom: RTMS didn't start (auto-start, scope or credits). Google Meet: the connected user doesn't own the meeting. Teams: the meeting ended before the bot was created | See the platform's notes in [step 2](#2-set-up-your-meeting-platform) |
| Bot stays `CALL_ENDED` (Zoom) | The meeting wasn't cloud-recorded, or the **All Recordings have completed** event subscription is missing | Turn on cloud recording and check the Zoom app's event subscriptions |
| No transcript after the meeting | Recording or transcription wasn't on, or it's still processing | Check the meeting's settings, and wait: up to 4 hours on Teams, 24 hours on Webex |
| `AADSTS50194`, `AADSTS90094` or `AADSTS7000215` (Teams) | Single-tenant app, missing admin consent, or the secret's ID was used instead of its Value | See steps 1, 2 and 4 under [Microsoft Teams](#microsoft-teams) |
| `400` when creating the webhook (Google Meet) | The project ID, service account email or private key is missing in Unified | Fill them in on the Google Meet (meeting bot) integration |

Still stuck? Read the platform guide, check the API call log in the Unified app, or email hello@unified.to.
