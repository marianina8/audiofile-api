#!/usr/bin/env bash
# Usage: scripts/tool-calls.sh generated|curated [--since 10m]
# Live view of the MCP tools a server actually called, safe to show on camera.
# It tails the Lambda's CloudWatch log group and keeps only the tool lines:
#   Server A: Executing tool "<name>": <METHOD> <url>
#   Server B: tool "<name>" <json>
# and prints only "HH:MM:SS  <name>   (<running count>)". URLs, inputs and
# everything else are dropped. A tool line that mentions a bearer token is
# dropped too. AWS CLI errors can carry ARNs with the account id, so they go
# to a private log that is removed on exit and kept (path printed) on failure.
# TOOL_CALLS_STDIN=1 reads log lines from stdin instead of AWS (for the test).
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/lib.sh
. "$here/lib.sh"

usage() {
  echo "usage: scripts/tool-calls.sh generated|curated [--since 10m]" >&2
  exit 2
}

server="${1:-}"
case "$server" in
  generated) stack=mcp-generated; logical=GeneratedServerFunction ;;
  curated) stack=mcp-curated; logical=CuratedServerFunction ;;
  *) usage ;;
esac
shift
since=10m
if [ "${1:-}" = "--since" ]; then
  since="${2:-}"
  [[ $since =~ ^[0-9]+[smhdw]$ ]] || { echo "--since needs a value like 30s, 10m, 2h, 1d" >&2; usage; }
  shift 2
fi
[ "$#" -eq 0 ] || usage

# local_time <YYYY-MM-DDTHH:MM:SS>: the CloudWatch prefix is UTC; print it as
# HH:MM:SS in the local zone (BSD date on macOS, GNU date as a fallback).
local_time() {
  local e
  if e=$(TZ=UTC date -j -f '%Y-%m-%dT%H:%M:%S' "$1" +%s 2>/dev/null); then
    date -r "$e" +%H:%M:%S
  else
    date -d "$1 UTC" +%H:%M:%S 2>/dev/null || echo "${1#*T}"
  fi
}

# filter: log lines on stdin, tool lines out. The optional CloudWatch prefix
# (2026-10-24T18:47:47 , UTC) supplies the time, shown in local time; otherwise the local clock does.
filter() {
  local line t name n=0
  local re_pre='^([0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2})[^ ]* (.*)$'
  local re_tool='^(Executing )?tool "([A-Za-z0-9_.-]+)"'
  while IFS= read -r line || [ -n "$line" ]; do
    t=""
    if [[ $line =~ $re_pre ]]; then
      t=$(local_time "${BASH_REMATCH[1]}")
      line="${BASH_REMATCH[2]}"
    fi
    [[ $line =~ $re_tool ]] || continue
    case "$line" in *[Bb]earer*) continue ;; esac
    name="${BASH_REMATCH[2]}"
    [ -n "$t" ] || t=$(date +%H:%M:%S)
    n=$((n + 1))
    printf '%s  %s   (%d)\n' "$t" "$name" "$n"
  done
}

echo "watching $server tool calls (Ctrl-C to stop)"

if [ "${TOOL_CALLS_STDIN:-}" = "1" ]; then
  filter
  exit 0
fi

export AWS_PROFILE="${AWS_PROFILE:-audiofile-admin}"
export AWS_REGION="${AWS_REGION:-us-west-2}"
logdir=$(umask 077 && mktemp -d)
log="$logdir/aws.log"
keep_log=0
trap '[ "$keep_log" -eq 1 ] || rm -rf "$logdir"' EXIT
fail() { keep_log=1; echo "$1 failed; see $log" >&2; exit 1; }

fn=$(af_aws cloudformation describe-stack-resource --stack-name "$stack" \
  --logical-resource-id "$logical" --query 'StackResourceDetail.PhysicalResourceId' \
  --output text --no-cli-pager 2>>"$log") || fail "function lookup"
[ -n "$fn" ] && [ "$fn" != "None" ] || fail "function lookup"

# Ctrl-C ends the tail (0, 130, 143): a normal stop. Anything else is a real
# failure (expired SSO, missing permission or log group), so keep the log.
set +e
af_aws logs tail "/aws/lambda/$fn" --follow --since "$since" --format short \
  --no-cli-pager 2>>"$log" | filter
rc=${PIPESTATUS[0]}
set -e
case "$rc" in 0 | 130 | 143) ;; *) fail "log tail" ;; esac
