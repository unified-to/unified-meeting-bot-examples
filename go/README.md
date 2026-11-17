# Go meeting bot

The whole bot is one file, [`main.go`](main.go). It uses the official SDK, [`github.com/unified-to/unified-go-sdk`](https://github.com/unified-to/unified-go-sdk), which needs Go 1.26.10+.

```sh
set -a; source ../.env; set +a        # see ../.env.example
go run . https://us02web.zoom.us/j/81234567890
go run . --name "Notetaker" <meeting_url>
```

To build a binary instead, run `go build -o meeting-bot .`.

**Signature note:** keep `data` as a `json.RawMessage` and sign those bytes. Re-encoding the parsed events with `json.Marshal` escapes `&`, `<` and `>`, so the signature fails whenever a transcript contains one of them.

First time? Follow the [setup guide](../SETUP.md). The [main README](../README.md) has the event reference and production notes.
