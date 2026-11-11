#!/bin/sh
# Renders the web UI on the demo files with headless Chrome at 2x.
# Usage: scripts/screenshot.sh OUT.png [WIDTH HEIGHT] [extra hash params, e.g. reveal=1]
set -eu
cd "$(dirname "$0")/.."
CHROME="${CHROME:-/Applications/Google Chrome.app/Contents/MacOS/Google Chrome}"
OUT="$(cd "$(dirname "$1")" && pwd)/$(basename "$1")"
W="${2:-1440}" H="${3:-1000}" EXTRA="${4:-}"
TMP="$(mktemp -d)"
go build -o "$TMP/envdiff" .
cd testdata/demo
"$TMP/envdiff" serve --no-open .env.staging staging-secrets.yaml compose.yml#api >"$TMP/url" &
PID=$!
trap 'kill $PID 2>/dev/null; rm -rf "$TMP"' EXIT
while [ ! -s "$TMP/url" ]; do sleep 0.1; done
"$CHROME" --headless=new --disable-gpu --hide-scrollbars --user-data-dir="$TMP/profile" \
  --virtual-time-budget=4000 --window-size="$W,$H" --force-device-scale-factor=2 \
  --screenshot="$OUT" "$(cat "$TMP/url")&$EXTRA" >/dev/null 2>&1
echo "$OUT"
