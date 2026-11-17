# Go meeting bot

The whole bot is one file, [`main.go`](main.go). It uses only the standard library and needs Go 1.22+.

```sh
set -a; source ../.env; set +a        # see ../.env.example
go run . https://us02web.zoom.us/j/81234567890
go run . --name "Notetaker" --json <meeting_url>
```

To build a binary instead, run `go build -o meeting-bot .`.

**Signature note:** keep `data` as a `json.RawMessage` and sign those bytes. Re-encoding the parsed events with `json.Marshal` escapes `&`, `<` and `>`, so the signature fails whenever a transcript contains one of them.

See the [main README](../README.md) for setup, the event reference and production notes.
