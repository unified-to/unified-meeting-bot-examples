#!/usr/bin/env bash
# Runs every example against the mock Unified server (mock-unified/server.ts) and checks
# that each one prints exactly the expected events and transcript, rejects the tampered
# delivery, and removes its bot and webhook on Ctrl+C.
#
#   ./scripts/test-all.sh                  # all languages
#   ./scripts/test-all.sh python go        # just some
#
# Needs: Node.js 22.18+, Python 3.10+ and Go 1.26.10+ with each example's dependencies
# installed (npm install, pip install -r requirements.txt), and curl.
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
if [ $# -gt 0 ]; then LANGUAGES=("$@"); else LANGUAGES=(typescript python go); fi
TMP=$(mktemp -d)

command_for() {
  case "$1" in
    typescript) echo "node typescript/src/meeting-bot.ts" ;;
    python) echo "${PYTHON:-python3} python/meeting_bot.py" ;;
    go) echo "go/meeting-bot" ;;
  esac
}

for lang in "${LANGUAGES[@]}"; do
  if [ "$lang" = go ]; then (cd go && go build -o meeting-bot .); fi
done

node mock-unified/server.ts >"$TMP/mock.log" 2>&1 &
MOCK_PID=$!
trap 'kill $MOCK_PID 2>/dev/null; rm -rf "$TMP"' EXIT
for _ in $(seq 50); do curl -sf localhost:4000/_mock/state >/dev/null && break; sleep 0.1; done

state() { curl -sf localhost:4000/_mock/state | node -pe "JSON.parse(require('fs').readFileSync(0)).$1"; }

failures=0
for lang in "${LANGUAGES[@]}"; do
  rejected_before=$(state deliveries_rejected)

  $(command_for "$lang") --name "Test Bot" "$MEETING_URL" >"$TMP/out" 2>"$TMP/err" &
  pid=$!
  # The last line printed is the end of the recording's transcript.
  for _ in $(seq 150); do grep -q "Jane Doe: Thanks, everyone." "$TMP/out" 2>/dev/null && break; sleep 0.1; done
  kill -INT "$pid" 2>/dev/null || true
  status=0; wait "$pid" || status=$?

  # Ids are random: replace them so the output can be compared with a fixed file.
  sed -E 's/[0-9a-f]{24}/ID/g' "$TMP/out" >"$TMP/normalized"
  problems=()
  [ "$status" -eq 0 ] || problems+=("exit code $status")
  diff -u scripts/expected-output.txt "$TMP/normalized" >"$TMP/diff" || problems+=("unexpected output")
  [ "$(state deliveries_rejected)" -eq $((rejected_before + 1)) ] || problems+=("tampered delivery was not rejected")
  [ "$(state webhooks)" -eq 0 ] || problems+=("webhook was not removed")
  [ "$(state active_bots)" -eq 0 ] || problems+=("bot was not removed")

  if [ ${#problems[@]} -eq 0 ]; then
    echo "PASS  $lang"
  else
    failures=$((failures + 1))
    echo "FAIL  $lang: ${problems[*]}"
    sed 's/^/      /' "$TMP/diff" "$TMP/err"
  fi
done

if [ "$(state deliveries_failed)" -ne 0 ]; then
  failures=$((failures + 1))
  echo "FAIL  $(state deliveries_failed) webhook deliveries got an unexpected response"
fi
exit $failures
