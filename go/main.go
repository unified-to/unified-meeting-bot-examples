// Command meeting-bot is the Go example of a Unified meeting bot.
//
// It sends a bot into a Zoom, Microsoft Teams, Webex or Google Meet meeting with
// Unified's Calendar API (calendar_bot), then prints every calendar_botevent
// webhook it receives.
//
//	go run . [--name "Meeting Bot"] [--json] <meeting_url>
//
// How it works:
//  1. Start a local HTTP server to receive webhooks (PUBLIC_URL forwards to it).
//  2. Subscribe to calendar_botevent webhooks for the connection.
//  3. Create the bot for the meeting URL.
//  4. Verify and print each event as it arrives.
//  5. On Ctrl+C, remove the bot (it leaves the meeting) and the webhook.
//
// Standard library only: Go 1.22+.
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
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
)

const webhookPath = "/bot-events"

// --- Types: only the fields this example prints ------------------------------------
// Full model: https://docs.unified.to/calendar/botevent/model

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
	apiURL, apiKey, connectionID, workspaceSecret, publicURL, port string
	printJSON                                                      bool

	botID atomic.Value // string, set once our bot exists
	seen  sync.Map     // Unified retries failed deliveries, so skip repeats
}

func main() {
	name := flag.String("name", "Meeting Bot", "bot name shown to people in the meeting")
	printJSON := flag.Bool("json", false, "print each event as raw JSON")
	flag.Parse()
	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, `Usage: go run . [--name "Meeting Bot"] [--json] <meeting_url>`)
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
		apiURL:          envOr("UNIFIED_API_URL", "https://api.unified.to"),
		apiKey:          os.Getenv("UNIFIED_API_KEY"),
		connectionID:    os.Getenv("UNIFIED_CONNECTION_ID"),
		workspaceSecret: os.Getenv("UNIFIED_WORKSPACE_SECRET"),
		publicURL:       strings.TrimRight(os.Getenv("PUBLIC_URL"), "/"),
		port:            envOr("PORT", "3000"),
		printJSON:       *printJSON,
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
	var webhook struct {
		ID string `json:"id"`
	}
	err = a.unified("POST", "/unified/webhook", map[string]any{
		"connection_id": a.connectionID,
		"object_type":   "calendar_botevent",
		"event":         "created",
		"webhook_type":  "native",
		"hook_url":      a.publicURL + webhookPath,
	}, &webhook)
	if err != nil {
		return err
	}
	logf("Webhook %s subscribed → %s%s", webhook.ID, a.publicURL, webhookPath)
	defer func() {
		logf("Removing webhook %s", webhook.ID)
		if err := a.unified("DELETE", "/unified/webhook/"+webhook.ID, nil, nil); err != nil {
			logf("%v", err)
		}
	}()

	// 3. Send the bot into the meeting.
	body := map[string]any{"name": name, "meeting_url": meetingURL}
	if strings.Contains(meetingURL, "zoom.us") {
		// Zoom only: opt in to live captions and chat. Other platforms reject recording_config (501).
		body["recording_config"] = map[string]any{"record_transcript": true, "record_chat": true}
	}
	var bot struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := a.unified("POST", "/calendar/"+a.connectionID+"/bot", body, &bot); err != nil {
		return err
	}
	a.botID.Store(bot.ID)
	logf("Bot %s created (%s). Printing events; press Ctrl+C to stop.\n", bot.ID, bot.Status)

	// 4. The webhook handler prints events until Ctrl+C.
	<-ctx.Done()
	stop() // a second Ctrl+C now quits immediately

	// 5. Remove the bot. It leaves the meeting and stops sending events.
	logf("\nRemoving bot %s", bot.ID)
	if err := a.unified("DELETE", "/calendar/"+a.connectionID+"/bot/"+bot.ID, nil, nil); err != nil {
		logf("%v", err)
	}
	return nil // the deferred function removes the webhook
}

// --- Unified API -------------------------------------------------------------------

// unified calls the Unified API, sending body as JSON and decoding the response into out.
func (a *app) unified(method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, a.apiURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	req.Header.Set("Content-Type", "application/json")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	text, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	if res.StatusCode >= 300 {
		return fmt.Errorf("%s %s → %d %s", method, path, res.StatusCode, text)
	}
	if out == nil || len(text) == 0 {
		return nil
	}
	return json.Unmarshal(text, out)
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
	// 5 seconds: hand slow work (summaries, CRM updates) to a queue or goroutine.
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

	// Deduplicate on id. Google Meet can reuse ids for status changes, so add changed_at.
	if event.ID != "" {
		key := event.ID
		if event.StatusChange != nil {
			key += ":" + event.StatusChange.ChangedAt
		}
		if _, repeat := a.seen.LoadOrStore(key, true); repeat {
			return
		}
	}

	if a.printJSON {
		var line bytes.Buffer
		json.Compact(&line, raw)
		fmt.Println(line.String())
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
