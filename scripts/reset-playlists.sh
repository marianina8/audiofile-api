#!/usr/bin/env bash
# Usage: scripts/reset-playlists.sh   (after: source scripts/token.sh)
# Deletes every playlist and prints how many. Tracks are not touched.
# SEED_API_URL overrides the stack's ApiUrl (local testing).
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/lib.sh
. "$here/lib.sh"
command -v jq >/dev/null || { echo "jq is required" >&2; exit 1; }
require_token || exit 1
API="${SEED_API_URL:-$(out ApiUrl)}"
API="${API%/}"

umask 077
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
printf 'Authorization: Bearer %s\n' "$TOKEN" >"$WORK/auth"

curl -sS -f -H "@$WORK/auth" "$API/playlists" >"$WORK/list.json" ||
  { echo "reset-playlists: GET /playlists failed" >&2; exit 1; }
n=0
for id in $(jq -r '.items[]?.id' "$WORK/list.json"); do
  case "$id" in '' | *[!A-Za-z0-9_-]*) echo "reset-playlists: unexpected playlist id" >&2; exit 1 ;; esac
  curl -sS -f -o /dev/null -X DELETE -H "@$WORK/auth" "$API/playlists/$id" ||
    { echo "reset-playlists: DELETE failed for a playlist" >&2; exit 1; }
  n=$((n + 1))
done
echo "deleted $n playlists"
