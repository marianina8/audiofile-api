#!/usr/bin/env bash
# Usage: scripts/seed.sh [--dry-run] [--include-nc] [--yes]   (after: source scripts/token.sh)
# Seeds the library from ~/audio (override with AUDIO_DIR; CSV with LIBRARY_CSV):
#  1. ffprobe every *.mp3 and print file/title/artist/genre/TBPM/copyright. A
#     library.csv row fills tags that are empty; "*" marks CSV-sourced values.
#  2. Flag NonCommercial files (NonCommercial|BY-NC|CC-NC in copyright, comment
#     or license) and skip them unless --include-nc.
#  3. SHA-256 each file; drop in-folder duplicates.
#  4. With $TOKEN, skip files whose hash is already on a track (GET /tracks).
#  5. Ask "Upload N files? [y/N]" (stdin; --yes skips the question).
#  6. Upload a tagged temp copy of each file (originals are never modified; the
#     hash sent is the original's), wait for ready/failed (120 s per track).
#  7. Print title/artist/genre/bpm/source/energy and the bpm/energy spread.
# --dry-run stops after step 4 and needs no token or network (the
# already-uploaded check is skipped, and says so, when $TOKEN is unset).
# Exits non-zero if any upload fails. Needs ffmpeg, ffprobe, jq, curl, go.
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/lib.sh
. "$here/lib.sh"

DRY=0 NC=0 YES=0
for a in "$@"; do
  case "$a" in
    --dry-run) DRY=1 ;;
    --include-nc) NC=1 ;;
    --yes) YES=1 ;;
    *) echo "usage: seed.sh [--dry-run] [--include-nc] [--yes]" >&2; exit 2 ;;
  esac
done

AUDIO_DIR="${AUDIO_DIR:-$HOME/audio}"
LIBRARY_CSV="${LIBRARY_CSV:-$AUDIO_DIR/library.csv}"
SEP=$'\x1f' # field separator that survives empty fields under `read`

for c in ffmpeg ffprobe jq curl shasum; do
  command -v "$c" >/dev/null || { echo "seed: $c is required" >&2; exit 1; }
done

umask 077
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

# ---------- 1. probe ----------
echo '{}' >"$WORK/csv.json"
if [ -f "$LIBRARY_CSV" ]; then
  command -v go >/dev/null || { echo "seed: go is required to read $LIBRARY_CSV" >&2; exit 1; }
  (cd "$here/.." && go run ./cmd/draftlibrary --to-json "$LIBRARY_CSV") |
    jq 'map({key: .filename, value: .}) | from_entries' >"$WORK/csv.json"
else
  echo "note: $LIBRARY_CSV not found; using file tags only"
fi

files=()
while IFS= read -r f; do files+=("$f"); done < <(find "$AUDIO_DIR" -maxdepth 1 -type f -name '*.mp3' | LC_ALL=C sort)
[ "${#files[@]}" -gt 0 ] || { echo "seed: no mp3 files in $AUDIO_DIR" >&2; exit 1; }

# probe.tsv: path SEP name SEP title SEP artist SEP album SEP genre SEP year SEP tbpm SEP copyright SEP comment SEP license SEP src
for f in "${files[@]}"; do
  name=$(basename "$f")
  ffprobe -v error -show_entries format_tags -of json "$f" |
    jq -r --arg path "$f" --arg name "$name" --slurpfile c "$WORK/csv.json" --arg sep "$SEP" '
      ($c[0][$name] // {}) as $r
      | ((.format.tags // {}) | with_entries(.key |= ascii_downcase)) as $t
      | def clean: tostring | gsub("[\u001f\n\r\t]"; " ");
        def pick($tag; $csv): ($t[$tag] // "") as $v
          | if $v != "" then [$v, "t"] elif ($r[$csv] // "") != "" then [$r[$csv], "c"] else ["", "-"] end;
        pick("title"; "title") as $ti | pick("artist"; "artist") as $ar | pick("album"; "album") as $al
        | pick("genre"; "genre") as $ge | pick("date"; "year") as $ye
        | [$path, $name, ($ti[0]|clean), ($ar[0]|clean), ($al[0]|clean), ($ge[0]|clean),
           ($ye[0]|clean|.[0:4]), (($t.tbpm // $t.bpm // "")|clean), (($t.copyright // $t.tcop // "")|clean),
           (($t.comment // "")|clean), (($t.license // "")|clean),
           ($ti[1] + $ar[1] + $ge[1])] | join($sep)' >>"$WORK/probe.tsv"
done

mark() { # mark <value> <src char>: value, with * when it came from the CSV, - when empty
  if [ -z "$1" ]; then printf -- '-'; elif [ "$2" = c ]; then printf '%s*' "$1"; else printf '%s' "$1"; fi
}
trunc() { # trunc <width> <string>
  if [ "${#2}" -gt "$1" ]; then printf '%s~' "${2:0:$(($1 - 1))}"; else printf '%s' "$2"; fi
}
mid_trunc() { # mid_trunc <width>: keep head and end (uploader, id, "(1)" suffix)
  if [ "${#2}" -gt "$1" ]; then printf '%s~%s' "${2:0:14}" "${2:$((${#2} - $1 + 15))}"; else printf '%s' "$2"; fi
}

echo "== library: ${#files[@]} files in $AUDIO_DIR  (* = from library.csv) =="
printf '%-34s %-26s %-17s %-11s %-5s %s\n' file title artist genre TBPM copyright
: >"$WORK/cand.tsv" # path SEP sha SEP name SEP title SEP artist SEP album SEP genre SEP year
while IFS="$SEP" read -r path name title artist album genre year tbpm copyr comment license src; do
  printf '%-34s %-26s %-17s %-11s %-5s %s\n' "$(mid_trunc 34 "$name")" \
    "$(trunc 26 "$(mark "$title" "${src:0:1}")")" "$(trunc 17 "$(mark "$artist" "${src:1:1}")")" \
    "$(trunc 11 "$(mark "$genre" "${src:2:1}")")" "${tbpm:--}" "$(trunc 40 "${copyr:--}")"
  # ---------- 2. NonCommercial ----------
  if printf '%s\n%s\n%s\n' "$copyr" "$comment" "$license" | grep -Eiq 'NonCommercial|BY-NC|CC-NC'; then
    if [ "$NC" -ne 1 ]; then
      echo "  ! NonCommercial: skipping $name (use --include-nc to override)"
      continue
    fi
    echo "  ! NonCommercial: including $name (--include-nc)"
  fi
  printf '%s\n' "$path$SEP$name$SEP$title$SEP$artist$SEP$album$SEP$genre$SEP$year" >>"$WORK/cand.tsv"
done <"$WORK/probe.tsv"

# ---------- 3. in-folder duplicates ----------
# Shortest filename first, so "x.mp3" is kept over "x (1).mp3".
: >"$WORK/plan.tsv" # path SEP sha SEP name SEP title SEP artist SEP album SEP genre SEP year
: >"$WORK/seen.txt"
echo
while IFS= read -r line; do
  path=${line%%"$SEP"*}
  sha=$(shasum -a 256 "$path" | cut -d' ' -f1)
  name=$(basename "$path")
  if first=$(grep -m1 "^$sha " "$WORK/seen.txt"); then
    echo "duplicate: $name is identical to ${first#* }; skipping"
    continue
  fi
  printf '%s %s\n' "$sha" "$name" >>"$WORK/seen.txt"
  printf '%s\n' "${line%%"$SEP"*}$SEP$sha$SEP${line#*"$SEP"}" >>"$WORK/plan.tsv"
done < <(while IFS= read -r l; do p=${l%%"$SEP"*}; printf '%s\t%s\n' "${#p}" "$l"; done <"$WORK/cand.tsv" | sort -s -n -k1,1 | cut -f2-)

# ---------- 4. already uploaded ----------
API=""
if [ -n "${TOKEN:-}" ]; then
  API="${SEED_API_URL:-$(out ApiUrl)}"
  API="${API%/}"
  printf 'Authorization: Bearer %s\n' "$TOKEN" >"$WORK/auth"
  cursor=""
  : >"$WORK/uploaded.txt"
  while :; do
    curl -sS -f -G -H "@$WORK/auth" --data-urlencode "limit=100" \
      ${cursor:+--data-urlencode "cursor=$cursor"} "$API/tracks" >"$WORK/page.json" ||
      { echo "seed: GET /tracks failed" >&2; exit 1; }
    jq -r '.items[]? | .content_sha256 // empty' "$WORK/page.json" >>"$WORK/uploaded.txt"
    cursor=$(jq -r '.next_cursor // empty' "$WORK/page.json")
    [ -n "$cursor" ] || break
  done
  : >"$WORK/plan2.tsv"
  while IFS= read -r line; do
    rest=${line#*"$SEP"}
    sha=${rest%%"$SEP"*}
    if grep -qx "$sha" "$WORK/uploaded.txt"; then
      rest=${rest#*"$SEP"}
      echo "already uploaded: ${rest%%"$SEP"*}; skipping"
    else
      printf '%s\n' "$line" >>"$WORK/plan2.tsv"
    fi
  done <"$WORK/plan.tsv"
  mv "$WORK/plan2.tsv" "$WORK/plan.tsv"
else
  echo "note: TOKEN is not set; skipping the already-uploaded check"
fi

N=$(wc -l <"$WORK/plan.tsv" | tr -d ' ')
if [ "$DRY" -eq 1 ]; then
  echo
  echo "dry run: $N files would be uploaded"
  exit 0
fi
require_token || exit 1
if [ -z "$API" ]; then
  API="${SEED_API_URL:-$(out ApiUrl)}"
  API="${API%/}"
  printf 'Authorization: Bearer %s\n' "$TOKEN" >"$WORK/auth"
fi
[ "$N" -gt 0 ] || { echo "nothing to upload"; exit 0; }

# ---------- 5. confirm ----------
if [ "$YES" -ne 1 ]; then
  printf 'Upload %s files? [y/N] ' "$N"
  ans=""
  IFS= read -r ans || true
  case "$ans" in y | Y | yes | YES) ;; *) echo "aborted"; exit 1 ;; esac
fi

# ---------- 6. upload ----------
: >"$WORK/ok.ndjson"
: >"$WORK/failed.txt"

wait_ready() { # wait_ready <id>: 0 ready, 1 failed/timeout (message in $WORK/why)
  local id=$1 i=0 st
  while [ "$i" -lt 40 ]; do
    curl -sS -o "$WORK/poll" -H "@$WORK/auth" "$API/tracks/$id/status" || true
    st=$(jq -r '.status // empty' "$WORK/poll" 2>/dev/null || true)
    case "$st" in
      ready) return 0 ;;
      failed) jq -r '.error // "processing failed"' "$WORK/poll" >"$WORK/why"; return 1 ;;
    esac
    sleep 3
    i=$((i + 1))
  done
  echo "not ready after 120 s" >"$WORK/why"
  return 1
}

upload_one() { # upload_one <path> <sha> <name> <title> <artist> <album> <genre> <year>
  local path=$1 sha=$2 name=$3 title=$4 artist=$5 album=$6 genre=$7 year=$8
  local tagged="$WORK/tagged.mp3" id size code url hdrs
  local meta=()
  [ -z "$title" ] || meta+=(-metadata "title=$title")
  [ -z "$artist" ] || meta+=(-metadata "artist=$artist")
  [ -z "$album" ] || meta+=(-metadata "album=$album")
  [ -z "$genre" ] || meta+=(-metadata "genre=$genre")
  [ -z "$year" ] || meta+=(-metadata "date=$year")
  rm -f "$tagged"
  ffmpeg -nostdin -loglevel error -i "$path" -map 0:a -c copy -id3v2_version 3 \
    ${meta[@]+"${meta[@]}"} "$tagged" || { echo "ffmpeg tagging failed" >"$WORK/why"; return 1; }
  size=$(wc -c <"$tagged" | tr -d ' ') || { echo "could not size the tagged copy" >"$WORK/why"; return 1; }
  jq -n --arg f "$name" --argjson s "$size" --arg h "$sha" \
    '{filename: $f, size_bytes: $s, content_sha256: $h, content_type: "audio/mpeg"}' >"$WORK/req.json" ||
    { echo "could not build the upload request" >"$WORK/why"; return 1; }
  code=$(curl -sS -o "$WORK/resp.json" -w '%{http_code}' -X POST -H "@$WORK/auth" \
    --json "@$WORK/req.json" "$API/tracks/uploads") || { echo "POST /tracks/uploads: curl error" >"$WORK/why"; return 1; }
  if [ "$code" != 201 ]; then
    echo "POST /tracks/uploads returned $code: $(jq -r '.error.message // empty' "$WORK/resp.json" 2>/dev/null)" >"$WORK/why"
    return 1
  fi
  id=$(jq -r '.track_id' "$WORK/resp.json") || { echo "unreadable upload response" >"$WORK/why"; return 1; }
  case "$id" in '' | null | *[!A-Za-z0-9_-]*) echo "unexpected track id" >"$WORK/why"; return 1 ;; esac
  # URL and headers go through a curl config file so the signature stays off argv.
  url=$(jq -r '.upload_url // empty' "$WORK/resp.json") || url=""
  [ -n "$url" ] || { echo "upload response has no upload_url" >"$WORK/why"; return 1; }
  hdrs=$(jq -r '.upload_headers // {} | to_entries[] | "header = \"\(.key): \(.value)\""' "$WORK/resp.json") ||
    { echo "unreadable upload_headers" >"$WORK/why"; return 1; }
  printf 'url = "%s"\n' "$url" >"$WORK/put.cfg" || { echo "could not write curl config" >"$WORK/why"; return 1; }
  if [ -n "$hdrs" ]; then printf '%s\n' "$hdrs" >>"$WORK/put.cfg" || { echo "could not write curl config" >"$WORK/why"; return 1; }; fi
  code=$(curl -sS -o /dev/null -w '%{http_code}' -X PUT -K "$WORK/put.cfg" --data-binary "@$tagged") ||
    { echo "PUT: curl error" >"$WORK/why"; return 1; }
  [ "$code" = 200 ] || { echo "PUT returned $code" >"$WORK/why"; return 1; }
  wait_ready "$id" || return 1
  curl -sS -f -H "@$WORK/auth" "$API/tracks/$id" | jq -c . >>"$WORK/ok.ndjson" ||
    { echo "GET /tracks/$id failed" >"$WORK/why"; return 1; }
}

i=0
while IFS="$SEP" read -r path sha name title artist album genre year; do
  i=$((i + 1))
  printf '[%s/%s] %s ... ' "$i" "$N" "$name"
  : >"$WORK/why"
  if upload_one "$path" "$sha" "$name" "$title" "$artist" "$album" "$genre" "$year"; then
    echo ready
  else
    echo "FAILED: $(cat "$WORK/why")"
    printf '%s: %s\n' "$name" "$(cat "$WORK/why")" >>"$WORK/failed.txt"
  fi
done <"$WORK/plan.tsv"

# ---------- 7. report ----------
echo
printf '%-30s %-17s %-11s %6s %-9s %6s\n' title artist genre bpm source energy
jq -r '[.title, .artist, (.genre // "-"), (.bpm // "-"), (.bpm_source // "-"), (.energy // "-")] | @tsv' "$WORK/ok.ndjson" |
  while IFS=$'\t' read -r t a g b s e; do
    printf '%-30s %-17s %-11s %6s %-9s %6s\n' "$(trunc 30 "$t")" "$(trunc 17 "$a")" "$(trunc 11 "$g")" "$b" "$s" "$e"
  done
echo
jq -rs '
  def med: sort | if length == 0 then null else (.[(length - 1) / 2 | floor] + .[length / 2 | floor]) / 2 end;
  def spread($k): [.[] | .[$k] // empty] as $v
    | if ($v | length) == 0 then "none" else "min \($v | min)  median \($v | med)  max \($v | max)" end;
  "bpm:    \(spread("bpm"))",
  "energy: \(spread("energy"))",
  "bpm >= 120 and energy >= 0.6: \([.[] | select((.bpm // 0) >= 120 and (.energy // 0) >= 0.6)] | length) of \(length) tracks"' "$WORK/ok.ndjson"

ok=$(wc -l <"$WORK/ok.ndjson" | tr -d ' ')
bad=$(wc -l <"$WORK/failed.txt" | tr -d ' ')
echo
echo "seed: $ok ready, $bad failed"
if [ "$bad" -gt 0 ]; then
  sed 's/^/  failed: /' "$WORK/failed.txt"
  exit 1
fi
