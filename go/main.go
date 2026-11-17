// Command meeting-bot is the Go example of a Unified meeting bot.
//
// It sends a bot into a Zoom, Microsoft Teams, Webex or Google Meet meeting with
// Unified's Calendar API (calendar_bot), prints every calendar_botevent webhook it
// receives, and fetches the recording and transcript when they're ready.
//
//	go run . [--name "Meeting Bot"] <meeting_url>
//
// How it works:
//  1. Start a local HTTP server to receive webhooks (PUBLIC_URL forwards to it).
//  2. Subscribe to calendar_botevent webhooks for the connection.
//  3. Create the bot for the meeting URL.
//  4. Verify and print each event as it arrives. On RECORDING_READY, fetch the
//     recording and print its transcript.
//  5. On Ctrl+C, remove the bot (it leaves the meeting) and the webhook.
//
// Uses the official SDK (github.com/unified-to/unified-go-sdk), which needs Go 1.26.10+.
package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"

	unified "github.com/unified-to/unified-go-sdk"
	"github.com/unified-to/unified-go-sdk/pkg/models/operations"
	"github.com/unified-to/unified-go-sdk/pkg/models/shared"
)

const webhookPath = "/bot-events"

// --- Webhook payload types ---------------------------------------------------------
// Bot events arrive only as webhooks, so the SDK has no type for them. These are the
// fields this example prints. Full model: https://docs.unified.to/calendar/botevent/model

type Participant struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Email  string `json:"email"`
	IsHost bool   `json:"is_host"`
}

type BotEvent struct {
	ID           string `json:"id"`
	CreatedAt    string `json:"created_at"`
	BotID        string `json:"bot_id"`
	Type         string `json:"type"` // BOT_STATUS_CHANGE, PARTICIPANT_JOINED, TRANSCRIPT_RECEIVED, ...
	StatusChange *struct {
		Status    string `json:"status"`
		ChangedAt string `json:"changed_at"`
		SubCode   string `json:"sub_code"`
		Message   string `json:"message"`
	} `json:"status_change"`
	Participant *Participant `json:"participant"`
	Transcript  *struct {
		Text        string       `json:"text"`
		Participant *Participant `json:"participant"`
	} `json:"transcript"`
	ChatMessage *struct {
		Message string       `json:"message"`
		Sender  *Participant `json:"sender"`
	} `json:"chat_message"`
}

// delivery is the body Unified POSTs to your hook_url.
type delivery struct {
	Data   json.RawMessage `json:"data"` // kept raw: the signature covers these exact bytes
	Nonce  string          `json:"nonce"`
	Sig256 string          `json:"sig256"`
}

// app holds the configuration and the state shared with the webhook handler.
type app struct {
	unified                                        *unified.UnifiedTo
	connectionID, workspaceSecret, publicURL, port string

	botID atomic.Value // string, set once our bot exists
	seen  sync.Map     // events already printed
}

func main() {
	name := flag.String("name", "Meeting Bot", "bot name shown to people in the meeting")
	flag.Parse()
	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, `Usage: go run . [--name "Meeting Bot"] <meeting_url>`)
		os.Exit(2)
	}
	meetingURL := flag.Arg(0)
	flag.CommandLine.Parse(flag.Args()[1:]) // also accept flags after the URL

	var missing []string
	for _, name := range []string{"UNIFIED_API_KEY", "UNIFIED_CONNECTION_ID", "UNIFIED_WORKSPACE_SECRET", "PUBLIC_URL"} {
		if os.Getenv(name) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		fmt.Fprintf(os.Stderr, "Missing environment variables: %s (see .env.example)\n", strings.Join(missing, ", "))
		os.Exit(2)
	}

	a := &app{
		unified: unified.New(
			unified.WithSecurity(os.Getenv("UNIFIED_API_KEY")),
			unified.WithServerURL(envOr("UNIFIED_API_URL", "https://api.unified.to")), // EU: api-eu, AU: api-au
		),
		connectionID:    os.Getenv("UNIFIED_CONNECTION_ID"),
		workspaceSecret: os.Getenv("UNIFIED_WORKSPACE_SECRET"),
		publicURL:       strings.TrimRight(os.Getenv("PUBLIC_URL"), "/"),
		port:            envOr("PORT", "3000"),
	}
	if err := a.run(*name, meetingURL); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func (a *app) run(name, meetingURL string) error {
	// Catch Ctrl+C from the start, so pressing it during setup still cleans up.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// 1. Listen for webhooks.
	listener, err := net.Listen("tcp", ":"+a.port)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+webhookPath, a.handleWebhook)
	go http.Serve(listener, mux)
	logf("Listening for webhooks on http://localhost:%s%s", a.port, webhookPath)

	// 2. Subscribe to bot events. One webhook carries events for every bot on the connection.
	created, err := a.unified.Webhook.CreateUnifiedWebhook(ctx, operations.CreateUnifiedWebhookRequest{
		Webhook: shared.Webhook{
			ConnectionID: a.connectionID,
			ObjectType:   shared.WebhookObjectTypeCalendarBotevent,
			Event:        shared.WebhookEventCreated,
			WebhookType:  shared.WebhookTypeNative.ToPointer(),
			HookURL:      unified.String(a.publicURL + webhookPath),
		},
	})
	if err != nil {
		return err
	}
	webhookID := *created.Webhook.ID
	logf("Webhook %s subscribed → %s%s", webhookID, a.publicURL, webhookPath)
	defer func() {
		logf("Removing webhook %s", webhookID)
		_, err := a.unified.Webhook.RemoveUnifiedWebhook(context.Background(), operations.RemoveUnifiedWebhookRequest{ID: webhookID})
		if err != nil {
			logf("%v", err)
		}
	}()

	// 3. Send the bot into the meeting.
	bot := shared.CalendarBot{Name: unified.String(name), MeetingURL: unified.String(meetingURL)}
	if strings.Contains(meetingURL, "zoom.us") {
		// Zoom only: opt in to live captions and chat. Other platforms reject RecordingConfig (501).
		bot.RecordingConfig = &shared.PropertyCalendarBotRecordingConfig{
			RecordTranscript: unified.Bool(true),
			RecordChat:       unified.Bool(true),
		}
	}
	res, err := a.unified.Calendar.CreateCalendarBot(ctx, operations.CreateCalendarBotRequest{
		ConnectionID: a.connectionID,
		CalendarBot:  bot,
	})
	if err != nil {
		return err
	}
	botID := *res.CalendarBot.ID
	a.botID.Store(botID)
	logf("Bot %s created (%s). Printing events; press Ctrl+C to stop.\n", botID, *res.CalendarBot.Status)

	// 4. The webhook handler prints events until Ctrl+C.
	<-ctx.Done()
	stop() // a second Ctrl+C now quits immediately

	// 5. Remove the bot. It leaves the meeting and stops sending events.
	logf("\nRemoving bot %s", botID)
	_, err = a.unified.Calendar.RemoveCalendarBot(context.Background(), operations.RemoveCalendarBotRequest{
		ConnectionID: a.connectionID,
		ID:           botID,
	})
	if err != nil {
		logf("%v", err)
	}
	return nil // the deferred function removes the webhook
}

// --- Webhook handling --------------------------------------------------------------

func (a *app) handleWebhook(w http.ResponseWriter, r *http.Request) {
	var d delivery
	if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if !a.isSignatureValid(d) {
		logf("Rejected a webhook with an invalid signature")
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	var events []json.RawMessage
	if err := json.Unmarshal(d.Data, &events); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	// Printing is fast, so we handle events before replying. Unified waits at most
	// 5 seconds, so slow work (like fetching the recording) runs in a goroutine.
	for _, raw := range events {
		a.handleEvent(raw)
	}
	w.Write([]byte("ok"))
}

// isSignatureValid checks sig256 = base64(HMAC-SHA256(workspace secret, JSON.stringify(data) + nonce)).
func (a *app) isSignatureValid(d delivery) bool {
	// Sign the bytes of "data" exactly as received. json.Compact only strips whitespace;
	// re-encoding with json.Marshal would escape characters like & < > and break the signature.
	var data bytes.Buffer
	if err := json.Compact(&data, d.Data); err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(a.workspaceSecret))
	mac.Write(data.Bytes())
	mac.Write([]byte(d.Nonce))
	expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(d.Sig256)) // timing-safe
}

func (a *app) handleEvent(raw json.RawMessage) {
	var event BotEvent
	if err := json.Unmarshal(raw, &event); err != nil {
		logf("Could not parse event: %v", err)
		return
	}

	// The webhook receives events for every bot on this connection. Keep ours.
	if botID, _ := a.botID.Load().(string); botID != "" && event.BotID != botID {
		return
	}

	// Unified retries failed deliveries, so skip events we've already handled.
	// Event ids alone can repeat, so compare whole events.
	var key bytes.Buffer
	json.Compact(&key, raw)
	if _, repeat := a.seen.LoadOrStore(key.String(), true); repeat {
		return
	}

	clock := "--:--:--"
	if len(event.CreatedAt) >= 19 {
		clock = event.CreatedAt[11:19] // HH:MM:SS (UTC)
	}
	kind := event.Type
	if kind == "" {
		kind = "UNKNOWN"
	}
	fmt.Println(strings.TrimRight(fmt.Sprintf("%s  %-27s  %s", clock, kind, describe(event)), " "))

	if event.Type == "RECORDING_READY" {
		go a.printRecording(event.BotID)
	}
}

// describe returns one line of detail for each event type.
func describe(e BotEvent) string {
	switch e.Type {
	case "BOT_STATUS_CHANGE":
		if e.StatusChange == nil {
			return ""
		}
		parts := []string{e.StatusChange.Status}
		if e.StatusChange.SubCode != "" {
			parts = append(parts, "("+e.StatusChange.SubCode+")")
		}
		if e.StatusChange.Message != "" {
			parts = append(parts, e.StatusChange.Message)
		}
		return strings.Join(parts, " ")
	case "TRANSCRIPT_RECEIVED", "TRANSCRIPT_PARTIAL":
		if e.Transcript == nil {
			return ""
		}
		return nameOf(e.Transcript.Participant) + ": " + e.Transcript.Text
	case "CHAT_MESSAGE_RECEIVED":
		if e.ChatMessage == nil {
			return ""
		}
		return nameOf(e.ChatMessage.Sender) + ": " + e.ChatMessage.Message
	default: // PARTICIPANT_* events carry a participant; RECORDING_* events carry nothing extra
		if e.Participant == nil {
			return ""
		}
		if e.Participant.IsHost {
			return nameOf(e.Participant) + " (host)"
		}
		return nameOf(e.Participant)
	}
}

func nameOf(p *Participant) string {
	switch {
	case p == nil:
		return "unknown"
	case p.Name != "":
		return p.Name
	case p.Email != "":
		return p.Email
	case p.ID != "":
		return p.ID
	}
	return "unknown"
}

// --- Lesson 2: the recording and transcript ----------------------------------------

// printRecording runs once RECORDING_READY fires: the bot now links to its recording.
// It fetches the recording and prints the transcript.
func (a *app) printRecording(botID string) {
	const indent = "          "
	ctx := context.Background()
	res, err := a.unified.Calendar.GetCalendarBot(ctx, operations.GetCalendarBotRequest{ConnectionID: a.connectionID, ID: botID})
	if err != nil {
		logf("Could not fetch the recording: %v", err)
		return
	}
	recordingID := res.CalendarBot.GetRecordingID()
	if recordingID == nil {
		fmt.Println(indent + "No recording_id on the bot yet")
		return
	}

	rec, err := a.unified.Calendar.GetCalendarRecording(ctx, operations.GetCalendarRecordingRequest{
		ConnectionID: a.connectionID,
		ID:           *recordingID,
	})
	if err != nil {
		logf("Could not fetch the recording: %v", err)
		return
	}
	recording := rec.CalendarRecording
	webURL := "(no web_url)"
	if recording.WebURL != nil {
		webURL = *recording.WebURL
	}
	// Build the whole block first, so concurrent prints can't split it.
	var out strings.Builder
	fmt.Fprintf(&out, "%sRecording %s: %s\n", indent, *recording.ID, webURL)
	for _, media := range recording.Media {
		for _, line := range media.Transcripts {
			speaker := "unknown"
			if line.Attendee != nil && line.Attendee.Name != nil {
				speaker = *line.Attendee.Name
			} else if line.Attendee != nil && line.Attendee.Email != nil {
				speaker = *line.Attendee.Email
			}
			fmt.Fprintf(&out, "%s  %s: %s\n", indent, speaker, line.Text)
		}
		if len(media.Transcripts) == 0 && media.TranscriptDownloadURL != nil {
			fmt.Fprintf(&out, "%s  Transcript file: %s\n", indent, *media.TranscriptDownloadURL)
		}
	}
	fmt.Print(out.String())
}

// logf writes status messages to stderr, so stdout carries only events.
func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
