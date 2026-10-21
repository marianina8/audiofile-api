#!/usr/bin/env bash
# Usage: source scripts/token.sh && scripts/try-workout-mix.sh
# Builds an "upbeat workout mix" by hand, the chain an MCP client has to
# discover on its own: search, create playlist, add tracks, reorder by bpm,
# then open a 1-hour player link in the browser. The link is never printed.
# Run scripts/reset-playlists.sh afterwards for a clean slate.
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/lib.sh
. "$here/lib.sh"
require_token

api=$(out ApiUrl)
bpm_min="${BPM_MIN:-120}"
energy_min="${ENERGY_MIN:-0.6}"

call() { # call METHOD PATH [JSON]
  local method="$1" path="$2" body="${3:-}"
  if [ -n "$body" ]; then
    curl -sS --fail-with-body -X "$method" -H "Authorization: Bearer $TOKEN" \
      -H "Content-Type: application/json" -d "$body" "$api$path"
  else
    curl -sS --fail-with-body -X "$method" -H "Authorization: Bearer $TOKEN" "$api$path"
  fi
}

echo "1. search  GET /search?bpm_min=$bpm_min&energy_min=$energy_min"
ids=$(call GET "/search?bpm_min=$bpm_min&energy_min=$energy_min" | jq -c '[.items[].id]')
count=$(jq 'length' <<<"$ids")
echo "   found $count tracks"
[ "$count" -gt 0 ] || { echo "no tracks matched; try lower BPM_MIN/ENERGY_MIN" >&2; exit 1; }

echo "2. create  POST /playlists"
playlist=$(call POST /playlists '{"name":"Workout Mix","description":"Upbeat, high energy"}' | jq -r .id)

echo "3. add     POST /playlists/{id}/tracks"
call POST "/playlists/$playlist/tracks" "{\"track_ids\":$ids}" >/dev/null

echo "4. reorder PUT /playlists/{id}/tracks/order  (slowest first, fastest last)"
order=$(call GET "/playlists/$playlist/tracks" | jq -c '[.items | sort_by(.bpm)[] | .id]')
call PUT "/playlists/$playlist/tracks/order" "{\"track_ids\":$order}" >/dev/null

echo
call GET "/playlists/$playlist/tracks" |
  jq -r '.items | to_entries[] | "   \(.key + 1). \(.value.bpm) bpm  energy \(.value.energy)  \(.value.title) — \(.value.artist)"'
echo

echo "5. player  POST /playlists/{id}/player  (opening in your browser)"
url=$(call POST "/playlists/$playlist/player" | jq -r .url)
open "$url"
echo "done. Run scripts/reset-playlists.sh to remove the playlist."
