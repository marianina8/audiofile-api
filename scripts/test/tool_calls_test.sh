#!/usr/bin/env bash
# Offline check (no AWS): the scripts/tool-calls.sh filter, fed canned log
# lines through TOOL_CALLS_STDIN=1. Covers lines with and without the
# CloudWatch timestamp prefix, and that nothing sensitive is ever printed.
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
fail() { echo "FAIL - $1" >&2; cat "$tmp/out" >&2; exit 1; }

cat >"$tmp/in" <<'IN'
2026-10-24T18:47:47 Executing tool "listTracks": GET https://abc.execute-api.us-west-2.amazonaws.com/tracks
tool "find_tracks" {"mood":"upbeat"}
2026-10-24T18:47:49 tool "show_playlist" {"playlist":"1a2b3c4d"}
Executing tool "getTrack": GET https://abc.execute-api.us-west-2.amazonaws.com/tracks/1
2026-10-24T18:47:50 Authorization: Bearer eyJhbGciOiJSUzI1NiJ9.payload.sig
fetching https://abc.execute-api.us-west-2.amazonaws.com/tracks
account 123456789012 deployed
tool "leaky" Bearer eyJabc
START RequestId: 11111111-2222 Version: $LATEST
IN

TOOL_CALLS_STDIN=1 "$root/scripts/tool-calls.sh" generated <"$tmp/in" >"$tmp/out" 2>&1 || fail "script exited non-zero"

grep -qx 'watching generated tool calls (Ctrl-C to stop)' "$tmp/out" || fail "header missing"
grep -qE '^18:47:47  listTracks +\(1\)$' "$tmp/out" || fail "prefixed Server A line"
grep -qE '^[0-9]{2}:[0-9]{2}:[0-9]{2}  find_tracks +\(2\)$' "$tmp/out" || fail "unprefixed Server B line (local clock)"
grep -qE '^18:47:49  show_playlist +\(3\)$' "$tmp/out" || fail "prefixed Server B line"
grep -qE '^[0-9]{2}:[0-9]{2}:[0-9]{2}  getTrack +\(4\)$' "$tmp/out" || fail "unprefixed Server A line"
[ "$(grep -cE '\([0-9]+\)$' "$tmp/out")" -eq 4 ] || fail "expected exactly 4 tool lines"
if grep -Eiq 'http|[0-9]{12}|Bearer|leaky|eyJ' "$tmp/out"; then fail "sensitive text printed"; fi
echo "ok - tool-calls filter"

if TOOL_CALLS_STDIN=1 "$root/scripts/tool-calls.sh" bogus </dev/null >"$tmp/out" 2>&1; then fail "bad server accepted"; fi
echo "ok - rejects unknown server"
