# Python meeting bot

The whole bot is one file, [`meeting_bot.py`](meeting_bot.py). It uses only the standard library and needs Python 3.9+.

```sh
set -a; source ../.env; set +a        # see ../.env.example
python3 meeting_bot.py https://us02web.zoom.us/j/81234567890
python3 meeting_bot.py --name "Notetaker" --json <meeting_url>
```

**Signature note:** Unified signs `JSON.stringify(data)`. `json.dumps(data, separators=(",", ":"), ensure_ascii=False)` produces the same compact JSON. The default `json.dumps` adds spaces and escapes non-ASCII characters, which breaks the signature.

See the [main README](../README.md) for setup, the event reference and production notes.
