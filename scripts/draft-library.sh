#!/usr/bin/env bash
# Usage: scripts/draft-library.sh [dir]     (default dir: ~/audio)
# Writes <dir>/library.csv (filename,title,artist,album,genre,genre_source,year)
# drafted from the Pixabay filenames. Never overwrites an existing CSV. The
# audio files are only listed, never read or changed. No network, no AWS.
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
dir="${1:-$HOME/audio}"
csv="$dir/library.csv"

if [ -e "$csv" ]; then
  echo "draft-library: $csv already exists; not overwriting (delete it to regenerate)" >&2
  exit 1
fi

tmp=$(mktemp "$dir/.library-draft.XXXXXX")
trap 'rm -f "$tmp"' EXIT
(cd "$root" && go run ./cmd/draftlibrary --dir "$dir") >"$tmp"
# ln fails if the CSV appeared meanwhile, so a concurrent file is never replaced.
ln "$tmp" "$csv"
echo "wrote $csv ($(($(wc -l <"$csv") - 1)) rows)"
