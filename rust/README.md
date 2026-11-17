# Rust meeting bot

The whole bot is one file, [`src/main.rs`](src/main.rs). It uses small, synchronous crates, so there's no async runtime to learn first:

| Crate | Used for |
|---|---|
| `tiny_http` | receiving webhooks |
| `ureq` | calling the Unified API |
| `serde`, `serde_json` | JSON (`raw_value` keeps the signed bytes) |
| `hmac`, `sha2`, `base64` | verifying `sig256` |
| `ctrlc` | cleaning up on Ctrl+C |

```sh
set -a; source ../.env; set +a        # see ../.env.example
cargo run --release -- https://us02web.zoom.us/j/81234567890
cargo run --release -- --name "Notetaker" --json <meeting_url>
```

**Signature note:** `data` is deserialized as a borrowed `&RawValue`, so the bytes being verified are exactly the bytes Unified sent. Re-serializing a parsed `serde_json::Value` can change number formatting or key order and break the signature.

See the [main README](../README.md) for setup, the event reference and production notes.
