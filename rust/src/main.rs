//! Unified meeting bot: Rust example
//!
//! Sends a bot into a Zoom, Microsoft Teams, Webex or Google Meet meeting with
//! Unified's Calendar API (calendar_bot), then prints every calendar_botevent
//! webhook it receives.
//!
//! ```text
//! cargo run -- [--name "Meeting Bot"] [--json] <meeting_url>
//! ```
//!
//! How it works:
//!   1. Start a local HTTP server to receive webhooks (PUBLIC_URL forwards to it).
//!   2. Subscribe to calendar_botevent webhooks for the connection.
//!   3. Create the bot for the meeting URL.
//!   4. Verify and print each event as it arrives.
//!   5. On Ctrl+C, remove the bot (it leaves the meeting) and the webhook.
//!
//! Small, synchronous dependencies: tiny_http (server), ureq (client), serde_json,
//! hmac + sha2 + base64 (signatures) and ctrlc.

use std::collections::HashSet;
use std::env;
use std::error::Error;
use std::process;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;

use base64::{engine::general_purpose::STANDARD as BASE64, Engine};
use hmac::{Hmac, Mac};
use serde::Deserialize;
use serde_json::{json, value::RawValue, Value};
use sha2::Sha256;
use tiny_http::{Method, Response, Server};

const WEBHOOK_PATH: &str = "/bot-events";

type Result<T> = std::result::Result<T, Box<dyn Error + Send + Sync>>;

// --- Types: only the fields this example prints ------------------------------------
// Full model: https://docs.unified.to/calendar/botevent/model

#[derive(Deserialize)]
struct Participant {
    id: Option<String>,
    name: Option<String>,
    email: Option<String>,
    is_host: Option<bool>,
}

#[derive(Deserialize)]
struct BotEvent {
    id: Option<String>,
    created_at: Option<String>,
    bot_id: Option<String>,
    #[serde(rename = "type")]
    kind: Option<String>, // BOT_STATUS_CHANGE, PARTICIPANT_JOINED, TRANSCRIPT_RECEIVED, ...
    status_change: Option<StatusChange>,
    participant: Option<Participant>,
    transcript: Option<Transcript>,
    chat_message: Option<ChatMessage>,
}

#[derive(Deserialize)]
struct StatusChange {
    status: Option<String>,
    changed_at: Option<String>,
    sub_code: Option<String>,
    message: Option<String>,
}

#[derive(Deserialize)]
struct Transcript {
    text: Option<String>,
    participant: Option<Participant>,
}

#[derive(Deserialize)]
struct ChatMessage {
    message: Option<String>,
    sender: Option<Participant>,
}

/// The body Unified POSTs to your hook_url.
#[derive(Deserialize)]
struct Delivery<'a> {
    #[serde(borrow)]
    data: &'a RawValue, // kept raw: the signature covers these exact bytes
    nonce: String,
    sig256: String,
}

// --- Configuration -----------------------------------------------------------------

struct App {
    api_url: String,
    api_key: String,
    connection_id: String,
    workspace_secret: String,
    public_url: String,
    port: u16,
    name: String, // shown to people in the meeting
    meeting_url: String,
    print_json: bool, // --json: print each event as raw JSON instead
    http: ureq::Agent,
}

fn usage() -> ! {
    eprintln!("Usage: cargo run -- [--name \"Meeting Bot\"] [--json] <meeting_url>");
    process::exit(2);
}

impl App {
    /// Reads flags and environment variables, exiting with a message if something is missing.
    fn from_args_and_env() -> App {
        let mut name = "Meeting Bot".to_string();
        let mut print_json = false;
        let mut meeting_url = None;
        let mut args = env::args().skip(1);
        while let Some(arg) = args.next() {
            match arg.as_str() {
                "--name" => name = args.next().unwrap_or_else(|| usage()),
                "--json" => print_json = true,
                _ if arg.starts_with('-') => usage(),
                _ => meeting_url = Some(arg),
            }
        }

        let required = [
            "UNIFIED_API_KEY",
            "UNIFIED_CONNECTION_ID",
            "UNIFIED_WORKSPACE_SECRET",
            "PUBLIC_URL",
        ];
        let missing: Vec<&str> = required.into_iter().filter(|n| var(n).is_empty()).collect();
        if !missing.is_empty() {
            eprintln!(
                "Missing environment variables: {} (see .env.example)",
                missing.join(", ")
            );
            process::exit(2);
        }

        // Return HTTP errors as responses so we can show Unified's error message.
        let http = ureq::Agent::config_builder().http_status_as_error(false).build().into();
        App {
            api_url: var_or("UNIFIED_API_URL", "https://api.unified.to"),
            api_key: var("UNIFIED_API_KEY"),
            connection_id: var("UNIFIED_CONNECTION_ID"),
            workspace_secret: var("UNIFIED_WORKSPACE_SECRET"),
            public_url: var("PUBLIC_URL").trim_end_matches('/').to_string(),
            port: var("PORT").parse().unwrap_or(3000),
            name,
            meeting_url: meeting_url.unwrap_or_else(|| usage()),
            print_json,
            http,
        }
    }
}

fn var(name: &str) -> String {
    env::var(name).unwrap_or_default()
}

fn var_or(name: &str, fallback: &str) -> String {
    Some(var(name))
        .filter(|v| !v.is_empty())
        .unwrap_or_else(|| fallback.to_string())
}

// --- Main --------------------------------------------------------------------------

fn main() {
    let app = App::from_args_and_env();
    if let Err(err) = run(&app) {
        eprintln!("Error: {err}");
        process::exit(1);
    }
}

fn run(app: &App) -> Result<()> {
    // 1. Listen for webhooks. Requests queue up until the loop in run_bot() reads them.
    let server = Arc::new(Server::http(("0.0.0.0", app.port))?);
    eprintln!("Listening for webhooks on http://localhost:{}{WEBHOOK_PATH}", app.port);

    // Ctrl+C ends the request loop so we can clean up (a second Ctrl+C quits at once).
    // Caught from the start, so pressing it during setup still cleans up.
    let (server_for_ctrlc, pressed) = (Arc::clone(&server), AtomicBool::new(false));
    ctrlc::set_handler(move || {
        if pressed.swap(true, Ordering::SeqCst) {
            process::exit(130);
        }
        server_for_ctrlc.unblock();
    })?;

    // 2. Subscribe to bot events. One webhook carries events for every bot on the connection.
    let webhook = app.post(
        "/unified/webhook",
        json!({
            "connection_id": app.connection_id,
            "object_type": "calendar_botevent",
            "event": "created",
            "webhook_type": "native",
            "hook_url": format!("{}{WEBHOOK_PATH}", app.public_url),
        }),
    )?;
    let webhook_id = webhook["id"].as_str().unwrap_or_default();
    eprintln!("Webhook {webhook_id} subscribed → {}{WEBHOOK_PATH}", app.public_url);

    // Steps 3–5 live in run_bot(), so the webhook is removed even if they fail.
    let result = run_bot(app, &server);

    eprintln!("Removing webhook {webhook_id}");
    if let Err(err) = app.delete(&format!("/unified/webhook/{webhook_id}")) {
        eprintln!("{err}");
    }
    result
}

fn run_bot(app: &App, server: &Server) -> Result<()> {
    // 3. Send the bot into the meeting.
    let mut body = json!({ "name": app.name, "meeting_url": app.meeting_url });
    if app.meeting_url.contains("zoom.us") {
        // Zoom only: opt in to live captions and chat. Other platforms reject recording_config (501).
        body["recording_config"] = json!({ "record_transcript": true, "record_chat": true });
    }
    let bot = app.post(&format!("/calendar/{}/bot", app.connection_id), body)?;
    let bot_id = bot["id"].as_str().unwrap_or_default();
    let status = bot["status"].as_str().unwrap_or_default();
    eprintln!("Bot {bot_id} created ({status}). Printing events; press Ctrl+C to stop.\n");

    // 4. Handle webhooks until Ctrl+C.
    let mut seen = HashSet::new(); // Unified retries failed deliveries, so skip repeats
    for mut request in server.incoming_requests() {
        if *request.method() != Method::Post || request.url() != WEBHOOK_PATH {
            let _ = request.respond(Response::empty(404));
            continue;
        }
        let mut text = String::new();
        let delivery = match request.as_reader().read_to_string(&mut text) {
            Ok(_) => serde_json::from_str::<Delivery>(&text).ok(),
            Err(_) => None,
        };
        let Some(delivery) = delivery else {
            let _ = request.respond(Response::empty(400));
            continue;
        };

        if !is_signature_valid(&delivery, &app.workspace_secret) {
            eprintln!("Rejected a webhook with an invalid signature");
            let _ = request.respond(Response::empty(401));
            continue;
        }

        // Reply first: Unified waits at most 5 seconds. Do slow work after this.
        let _ = request.respond(Response::from_string("ok"));
        let events: Vec<&RawValue> = serde_json::from_str(delivery.data.get()).unwrap_or_default();
        for raw in events {
            handle_event(raw, bot_id, &mut seen, app.print_json);
        }
    }

    // 5. Remove the bot. It leaves the meeting and stops sending events.
    eprintln!("\nRemoving bot {bot_id}");
    if let Err(err) = app.delete(&format!("/calendar/{}/bot/{bot_id}", app.connection_id)) {
        eprintln!("{err}");
    }
    Ok(())
}

// --- Unified API -------------------------------------------------------------------

impl App {
    fn post(&self, path: &str, body: Value) -> Result<Value> {
        let response = self
            .http
            .post(format!("{}{path}", self.api_url))
            .header("Authorization", format!("Bearer {}", self.api_key))
            .send_json(body)?;
        read_response(response, "POST", path)
    }

    fn delete(&self, path: &str) -> Result<Value> {
        let response = self
            .http
            .delete(format!("{}{path}", self.api_url))
            .header("Authorization", format!("Bearer {}", self.api_key))
            .call()?;
        read_response(response, "DELETE", path)
    }
}

fn read_response(mut response: ureq::http::Response<ureq::Body>, method: &str, path: &str) -> Result<Value> {
    let status = response.status();
    let text = response.body_mut().read_to_string()?;
    if !status.is_success() {
        return Err(format!("{method} {path} → {} {text}", status.as_u16()).into());
    }
    Ok(if text.is_empty() {
        Value::Null
    } else {
        serde_json::from_str(&text)?
    })
}

// --- Webhook handling --------------------------------------------------------------

/// Checks sig256 = base64(HMAC-SHA256(workspace secret, JSON.stringify(data) + nonce)).
fn is_signature_valid(delivery: &Delivery, secret: &str) -> bool {
    let Ok(signature) = BASE64.decode(&delivery.sig256) else {
        return false;
    };
    let mut mac = Hmac::<Sha256>::new_from_slice(secret.as_bytes()).expect("HMAC takes keys of any length");
    mac.update(delivery.data.get().as_bytes()); // the exact bytes Unified sent (and signed)
    mac.update(delivery.nonce.as_bytes());
    mac.verify_slice(&signature).is_ok() // timing-safe
}

fn handle_event(raw: &RawValue, bot_id: &str, seen: &mut HashSet<String>, print_json: bool) {
    let Ok(event) = serde_json::from_str::<BotEvent>(raw.get()) else {
        eprintln!("Could not parse event: {}", raw.get());
        return;
    };

    // The webhook receives events for every bot on this connection. Keep ours.
    if event.bot_id.as_deref() != Some(bot_id) {
        return;
    }

    // Deduplicate on id. Google Meet can reuse ids for status changes, so add changed_at.
    if let Some(id) = &event.id {
        let changed_at = event.status_change.as_ref().and_then(|s| s.changed_at.as_deref());
        if !seen.insert(format!("{id}:{}", changed_at.unwrap_or_default())) {
            return;
        }
    }

    if print_json {
        println!("{}", raw.get());
        return;
    }
    let clock = event
        .created_at
        .as_deref()
        .and_then(|t| t.get(11..19))
        .unwrap_or("--:--:--"); // HH:MM:SS (UTC)
    let kind = event.kind.as_deref().unwrap_or("UNKNOWN");
    println!("{}", format!("{clock}  {kind:<27}  {}", describe(&event)).trim_end());
}

/// One line of detail for each event type.
fn describe(event: &BotEvent) -> String {
    match event.kind.as_deref() {
        Some("BOT_STATUS_CHANGE") => {
            let Some(change) = &event.status_change else {
                return String::new();
            };
            let sub_code = change.sub_code.as_ref().map(|code| format!("({code})"));
            [change.status.clone(), sub_code, change.message.clone()]
                .into_iter()
                .flatten()
                .filter(|part| !part.is_empty())
                .collect::<Vec<_>>()
                .join(" ")
        }
        Some("TRANSCRIPT_RECEIVED" | "TRANSCRIPT_PARTIAL") => match &event.transcript {
            Some(t) => format!(
                "{}: {}",
                name_of(t.participant.as_ref()),
                t.text.as_deref().unwrap_or_default()
            ),
            None => String::new(),
        },
        Some("CHAT_MESSAGE_RECEIVED") => match &event.chat_message {
            Some(c) => format!(
                "{}: {}",
                name_of(c.sender.as_ref()),
                c.message.as_deref().unwrap_or_default()
            ),
            None => String::new(),
        },
        // PARTICIPANT_* events carry a participant; RECORDING_* events carry nothing extra
        _ => match &event.participant {
            Some(p) if p.is_host == Some(true) => format!("{} (host)", name_of(Some(p))),
            Some(p) => name_of(Some(p)).to_string(),
            None => String::new(),
        },
    }
}

fn name_of(participant: Option<&Participant>) -> &str {
    let Some(p) = participant else {
        return "unknown";
    };
    [&p.name, &p.email, &p.id]
        .into_iter()
        .flatten()
        .map(String::as_str)
        .find(|s| !s.is_empty())
        .unwrap_or("unknown")
}
