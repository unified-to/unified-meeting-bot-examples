# Python meeting bot

The whole bot is one file, [`meeting_bot.py`](meeting_bot.py). It uses the official SDK, [`Unified-python-sdk`](https://github.com/unified-to/unified-python-sdk), which needs Python 3.10+.

```sh
python3 -m venv .venv && . .venv/bin/activate
pip install -r requirements.txt
set -a; source ../.env; set +a        # see ../.env.example
python3 meeting_bot.py https://us02web.zoom.us/j/81234567890
python3 meeting_bot.py --name "Notetaker" <meeting_url>
```

**Signature note:** Unified signs `JSON.stringify(data)`. `json.dumps(data, separators=(",", ":"), ensure_ascii=False)` produces the same compact JSON. The default `json.dumps` adds spaces and escapes non-ASCII characters, which breaks the signature.

First time? Follow the [setup guide](../SETUP.md). The [main README](../README.md) has the event reference and production notes.
