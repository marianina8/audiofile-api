#!/usr/bin/env bash
# Usage: scripts/smoke.sh   (after: source scripts/token.sh)
# Phase 1: /health is 200, /play/<bogus> is 404 and the 31 protected routes
# return 401 with no token.
# Phase 2: with $TOKEN, exercises all 34 routes using a temporary 12 s click
# track (uploaded two ways) and a temporary playlist. Library tracks and
# playlists are never touched; the temporary ones are deleted even on failure.
# Exits non-zero on the first failure, naming the route.
# SMOKE_API_URL overrides the stack's ApiUrl (used for local testing).
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/lib.sh
. "$here/lib.sh"

# Every route, in OpenAPI form. scripts/test/smoke_dryrun.sh checks this list
# and the step calls below against internal/api/routes.go.
ROUTES=(
  "GET /health"
  "POST /tracks/uploads" "GET /tracks" "GET /tracks/{id}" "PATCH /tracks/{id}"
  "DELETE /tracks/{id}" "GET /tracks/{id}/stream" "GET /tracks/{id}/status"
  "GET /search" "GET /artists" "GET /artists/{id}" "GET /artists/{id}/tracks"
  "GET /artists/{id}/albums" "GET /albums" "GET /albums/{id}"
  "GET /albums/{id}/tracks" "GET /genres" "GET /tags"
  "POST /tracks/{id}/tags" "DELETE /tracks/{id}/tags/{tag}"
  "POST /playlists" "GET /playlists" "GET /playlists/{id}"
  "PATCH /playlists/{id}" "DELETE /playlists/{id}" "GET /playlists/{id}/tracks"
  "POST /playlists/{id}/tracks" "DELETE /playlists/{id}/tracks/{trackId}"
  "PUT /playlists/{id}/tracks/order" "POST /playlists/{id}/player"
  "GET /play/{pid}" "POST /upload" "GET /request" "GET /list"
)

if [ "${1:-}" = "--list-routes" ]; then
  printf '%s\n' "${ROUTES[@]}"
  exit 0
fi

command -v jq >/dev/null || { echo "jq is required" >&2; exit 1; }
API="${SMOKE_API_URL:-$(out ApiUrl)}"
API="${API%/}"

umask 077
WORK=$(mktemp -d)
TRACK_A="" TRACK_B="" PLAYLIST=""
HAVE_AUTH=0

cleanup() {
  local rc=$?
  trap - EXIT
  if [ "$HAVE_AUTH" -eq 1 ]; then
    [ -z "$PLAYLIST" ] || curl -sS -o /dev/null -X DELETE -H "@$WORK/auth" "$API/playlists/$PLAYLIST" || true
    [ -z "$TRACK_A" ] || curl -sS -o /dev/null -X DELETE -H "@$WORK/auth" "$API/tracks/$TRACK_A" || true
    [ -z "$TRACK_B" ] || curl -sS -o /dev/null -X DELETE -H "@$WORK/auth" "$API/tracks/$TRACK_B" || true
  fi
  rm -rf "$WORK"
  exit "$rc"
}
trap cleanup EXIT

row() { printf '%s %-7s %-44s %-5s %s\n' "$1" "$2" "$3" "$4" "$5"; }

fail() { # fail <method> <label> <want> <got>
  row "✗" "$1" "$2" "$3" "$4"
  if [ -s "$WORK/body" ] && jq -e '.error' "$WORK/body" >/dev/null 2>&1; then
    echo "  api error: $(jq -r '.error | "\(.code): \(.message)"' "$WORK/body")" >&2
  fi
  echo "smoke: FAIL $1 $2 (want $3, got $4)" >&2
  exit 1
}

# check <method> <label> <want> <got>: want is an exact code or "2xx".
check() {
  case "$3" in
    2xx) case "$4" in 2??) ;; *) fail "$1" "$2" "$3" "$4" ;; esac ;;
    *) [ "$3" = "$4" ] || fail "$1" "$2" "$3" "$4" ;;
  esac
  row "✓" "$1" "$2" "$3" "$4"
}

# step <method> <path> <want> [curl args...]: one authenticated API call.
# The response body is left in $WORK/body.
step() {
  local method=$1 path=$2 want=$3 code
  shift 3
  code=$(curl -sS -o "$WORK/body" -w '%{http_code}' -X "$method" -H "@$WORK/auth" "$@" "$API$path") ||
    fail "$method" "$path" "$want" "curl-error"
  check "$method" "${path%%\?*}" "$want" "$code"
}

# unauth <method> <path> <want>: a call with no Authorization header.
unauth() {
  local code
  code=$(curl -sS -o "$WORK/body" -w '%{http_code}' -X "$1" "$API$2") ||
    fail "$1" "$2" "$3" "curl-error"
  check "$1" "$2" "$3" "$code"
}

jget() { jq -r "$1 // empty" "$WORK/body"; }

safe_id() { # ids go into URLs: refuse anything surprising
  case "$1" in ''|*[!A-Za-z0-9_-]*) echo "smoke: unexpected id format from $2" >&2; exit 1 ;; esac
}

wait_ready() { # wait_ready <track id>; up to 90 s
  local id=$1 i=0 st
  while [ "$i" -lt 30 ]; do
    curl -sS -o "$WORK/poll" -H "@$WORK/auth" "$API/tracks/$id/status" || true
    st=$(jq -r '.status // empty' "$WORK/poll" 2>/dev/null || true)
    case "$st" in
      ready) return 0 ;;
      failed)
        echo "smoke: FAIL track processing failed: $(jq -r '.error // "no detail"' "$WORK/poll")" >&2
        exit 1 ;;
    esac
    sleep 3
    i=$((i + 1))
  done
  echo "smoke: FAIL track did not become ready within 90 s" >&2
  exit 1
}

# ---------- Phase 1: no token ----------
echo "== no token =="
unauth GET /health 200
unauth GET /play/smoke-none 404
unauth GET /play/AAAAAAAAAAAAAAAAAAAAAA 404
for r in "${ROUTES[@]}"; do
  method=${r%% *}
  tmpl=${r#* }
  [ "$tmpl" = "/health" ] && continue
  [ "$tmpl" = "/play/{pid}" ] && continue
  path=$tmpl
  while [[ "$path" =~ \{[A-Za-z]+\} ]]; do path=${path/${BASH_REMATCH[0]}/smoke-none}; done
  unauth "$method" "$path" 401
done

# ---------- Phase 2: with token ----------
require_token || exit 1
printf 'Authorization: Bearer %s\n' "$TOKEN" >"$WORK/auth"
HAVE_AUTH=1
echo
echo "== with token =="

command -v ffmpeg >/dev/null || { echo "ffmpeg is required to build the temporary track" >&2; exit 1; }
MP3="$WORK/audiofile-smoke.mp3"
ffmpeg -nostdin -loglevel error -f lavfi \
  -i "aevalsrc='if(lt(mod(t,0.5),0.01),0.8*sin(2*PI*2000*t),0)':s=44100:d=12" \
  -metadata title="audiofile-smoke-$$" -metadata artist="audiofile-smoke" \
  -metadata album="audiofile-smoke" -metadata genre="smoke" \
  -c:a libmp3lame -q:a 4 "$MP3"

step GET /health 200

# Track A via the legacy multipart upload (plain-text id in the response).
step POST /upload 2xx -F "file=@$MP3;type=audio/mpeg"
TRACK_A=$(tr -d '[:space:]"' <"$WORK/body")
safe_id "$TRACK_A" "POST /upload"

# Track B via a presigned PUT.
size=$(wc -c <"$MP3" | tr -d ' ')
step POST /tracks/uploads 201 --json "{\"filename\":\"audiofile-smoke-b.mp3\",\"size_bytes\":$size,\"content_type\":\"audio/mpeg\"}"
TRACK_B=$(jget .track_id)
safe_id "$TRACK_B" "POST /tracks/uploads"
# The presigned URL and required headers go through a curl config file so the
# signature never appears in ps or the output.
{
  printf 'url = "%s"\n' "$(jget .upload_url)"
  jq -r '.upload_headers // {} | to_entries[] | "header = \"\(.key): \(.value)\""' "$WORK/body"
} >"$WORK/put.cfg"
code=$(curl -sS -o "$WORK/put.out" -w '%{http_code}' -X PUT -K "$WORK/put.cfg" --data-binary "@$MP3") ||
  fail PUT "(presigned upload)" 200 "curl-error"
check PUT "(presigned upload)" 200 "$code"

wait_ready "$TRACK_A"
wait_ready "$TRACK_B"

step GET /tracks 2xx
step GET "/tracks/$TRACK_A" 2xx
ARTIST=$(jget .artist_id)
ALBUM=$(jget .album_id)
safe_id "$ARTIST" "artist_id (is the click track's artist tag set?)"
safe_id "$ALBUM" "album_id (is the click track's album tag set?)"
step PATCH "/tracks/$TRACK_A" 2xx --json '{"bpm":120}'
step GET "/tracks/$TRACK_A/stream" 2xx
STREAM_URL=$(jget .url)
step GET "/tracks/$TRACK_A/status" 2xx
step GET "/search?q=audiofile-smoke" 2xx
step GET /artists 2xx
step GET "/artists/$ARTIST" 2xx
step GET "/artists/$ARTIST/tracks" 2xx
step GET "/artists/$ARTIST/albums" 2xx
step GET /albums 2xx
step GET "/albums/$ALBUM" 2xx
step GET "/albums/$ALBUM/tracks" 2xx
step GET /genres 2xx
step GET /tags 2xx
TAG=smoke
step POST "/tracks/$TRACK_A/tags" 2xx --json "{\"tags\":[\"$TAG\"]}"
step DELETE "/tracks/$TRACK_A/tags/$TAG" 204

# The stream URL is presigned: fetch only the headers' status, no auth header.
printf 'url = "%s"\n' "$STREAM_URL" >"$WORK/get.cfg"
code=$(curl -sS -o /dev/null -w '%{http_code}' -r 0-99 -K "$WORK/get.cfg") || fail GET "(stream url)" 2xx "curl-error"
check GET "(stream url)" 2xx "$code"

step POST /playlists 201 --json "{\"name\":\"audiofile-smoke\",\"description\":\"temporary\",\"track_ids\":[\"$TRACK_A\"]}"
PLAYLIST=$(jget .id)
safe_id "$PLAYLIST" "POST /playlists"
step GET /playlists 2xx
step GET "/playlists/$PLAYLIST" 2xx
step PATCH "/playlists/$PLAYLIST" 2xx --json '{"description":"temporary, safe to delete"}'
step POST "/playlists/$PLAYLIST/tracks" 2xx --json "{\"track_ids\":[\"$TRACK_B\"]}"
step GET "/playlists/$PLAYLIST/tracks" 2xx
step PUT "/playlists/$PLAYLIST/tracks/order" 2xx --json "{\"track_ids\":[\"$TRACK_B\",\"$TRACK_A\"]}"
step POST "/playlists/$PLAYLIST/player" 201
PLAYER_URL=$(jget .url)
case "$PLAYER_URL" in "$API"/play/*|https://*/play/*) ;; *) echo "smoke: unexpected player url shape" >&2; exit 1 ;; esac
printf 'url = "%s"\n' "$PLAYER_URL" >"$WORK/get.cfg"
code=$(curl -sS -o /dev/null -D "$WORK/player.headers" -w '%{http_code}' -K "$WORK/get.cfg") || fail GET "/play/{pid}" 200 "curl-error"
check GET "/play/{pid}" 200 "$code"
# route: GET /play/{pid}
grep -qi '^content-type: text/html' "$WORK/player.headers" || fail GET "/play/{pid} content-type" text/html "other"
step DELETE "/playlists/$PLAYLIST/tracks/$TRACK_B" 204

step GET "/request?id=$TRACK_A" 2xx
step GET /list 2xx

step DELETE "/playlists/$PLAYLIST" 204
PLAYLIST=""
step DELETE "/tracks/$TRACK_B" 204
TRACK_B=""
step DELETE "/tracks/$TRACK_A" 204
TRACK_A=""

echo
echo "smoke: PASS"
