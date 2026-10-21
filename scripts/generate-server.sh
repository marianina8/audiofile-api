#!/usr/bin/env bash
# Usage: scripts/generate-server.sh [--patch] [--force] [--out <dir>]
# Generates "Server A" from openapi.yaml with openapi-mcp-generator 4.1.0
# (one MCP tool per endpoint, TypeScript, streamable HTTP) into
# servers/generated/app (or --out <dir>), and prints the tool count.
#   --patch  then run scripts/patch-generated-server.sh on it
#   --force  replace an existing output directory
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$here/.." && pwd)"

usage() { echo "usage: scripts/generate-server.sh [--patch] [--force] [--out <dir>]" >&2; exit 2; }

out="$root/servers/generated/app"
patch=0
force=0
while [ $# -gt 0 ]; do
  case "$1" in
    --patch) patch=1 ;;
    --force) force=1 ;;
    --out) [ $# -ge 2 ] || usage; out="$2"; shift ;;
    *) usage ;;
  esac
  shift
done

if [ -e "$out" ] && [ -n "$(ls -A "$out" 2>/dev/null)" ]; then
  [ "$force" -eq 1 ] || { echo "$out already exists; re-run with --force to replace it" >&2; exit 1; }
  # Only ever delete generator output.
  if [ ! -f "$out/src/index.ts" ] || [ ! -f "$out/package.json" ]; then
    echo "refusing to replace $out: it does not look like generator output (src/index.ts + package.json)" >&2
    exit 1
  fi
  rm -rf "$out"
fi

log=$(mktemp)
trap 'rm -f "$log"' EXIT
npx --yes openapi-mcp-generator@4.1.0 \
  --input "$root/openapi.yaml" --output "$out" \
  --transport=streamable-http --port=3000 >"$log" 2>&1 || { cat "$log" >&2; exit 1; }

# One map entry per tool:   ["listTracks", {
tools=$(grep -cE '^  \["[A-Za-z0-9_]+", \{$' "$out/src/index.ts" || true)
[ "$tools" -gt 0 ] || { echo "no tools found in $out/src/index.ts (generator output changed?)" >&2; exit 1; }
echo "generated $tools tools into $out"

if [ "$patch" -eq 1 ]; then
  "$here/patch-generated-server.sh" "$out"
fi
