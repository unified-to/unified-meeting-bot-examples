#!/usr/bin/env bash
# Runs every example against the mock Unified server (mock-unified/server.ts) and checks
# that all four print exactly the expected events, reject the tampered delivery, and
# remove their bot and webhook on Ctrl+C.
#
#   ./scripts/test-all.sh            # all languages
#   ./scripts/test-all.sh go rust    # just some
#
# Needs: Node.js 22.18+, Python 3.9+, Go 1.22+, Rust (cargo), curl.
set -euo pipefail
set -m # job control: background jobs get SIGINT (Ctrl+C) like they would in a terminal
cd "$(dirname "$0")/.."

export UNIFIED_API_KEY=test-key
export UNIFIED_CONNECTION_ID=conn_test
export UNIFIED_WORKSPACE_SECRET=test-secret
export UNIFIED_API_URL=http://localhost:4000
export PUBLIC_URL=http://localhost:3000
export PORT=3000
export MOCK_DELAY_MS=100
unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy # keep localhost calls local

MEETING_URL="https://us02web.zoom.us/j/81234567890"
if [ $# -gt 0 ]; then LANGUAGES=("$@"); else LANGUAGES=(typescript python go rust); fi
TMP=$(mktemp -d)

command_for() {
  case "$1" in
    typescript) echo "node typescript/src/meeting-bot.ts" ;;
    python) echo "python3 python/meeting_bot.py" ;;
    go) echo "go/meeting-bot" ;;
    rust) echo "rust/target/release/meeting-bot" ;;
  esac
}

for lang in "${LANGUAGES[@]}"; do
  case "$lang" in
    go) (cd go && go build -o meeting-bot .) ;;
    rust) (cd rust && cargo build --release --quiet) ;;
  esac
done

node mock-unified/server.ts >"$TMP/mock.log" 2>&1 &
MOCK_PID=$!
trap 'kill $MOCK_PID 2>/dev/null; rm -rf "$TMP"' EXIT
for _ in $(seq 50); do curl -sf localhost:4000/_mock/state >/dev/null && break; sleep 0.1; done

state() { curl -sf localhost:4000/_mock/state | node -pe "JSON.parse(require('fs').readFileSync(0)).$1"; }

failures=0
for lang in "${LANGUAGES[@]}"; do
  for mode in text json; do
    flag=""; [ "$mode" = json ] && flag="--json"
    rejected_before=$(state deliveries_rejected)

    $(command_for "$lang") --name "Test Bot" $flag "$MEETING_URL" >"$TMP/out" 2>"$TMP/err" &
    pid=$!
    for _ in $(seq 150); do grep -q DONE "$TMP/out" 2>/dev/null && break; sleep 0.1; done
    kill -INT "$pid"
    status=0; wait "$pid" || status=$?

    # Bot ids are random: replace them so the output can be compared with a fixed file.
    sed -E 's/[0-9a-f]{24}/BOT_ID/g' "$TMP/out" >"$TMP/normalized"
    problems=()
    [ "$status" -eq 0 ] || problems+=("exit code $status")
    diff -u "scripts/expected-$mode.txt" "$TMP/normalized" >"$TMP/diff" || problems+=("unexpected output")
    [ "$(state deliveries_rejected)" -eq $((rejected_before + 1)) ] || problems+=("tampered delivery was not rejected")
    [ "$(state webhooks)" -eq 0 ] || problems+=("webhook was not removed")
    [ "$(state active_bots)" -eq 0 ] || problems+=("bot was not removed")

    if [ ${#problems[@]} -eq 0 ]; then
      echo "PASS  $lang ($mode)"
    else
      failures=$((failures + 1))
      echo "FAIL  $lang ($mode): ${problems[*]}"
      sed 's/^/      /' "$TMP/diff" "$TMP/err"
    fi
  done
done

if [ "$(state deliveries_failed)" -ne 0 ]; then
  failures=$((failures + 1))
  echo "FAIL  $(state deliveries_failed) webhook deliveries got an unexpected response"
fi
exit $failures
