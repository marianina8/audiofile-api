#!/usr/bin/env bash
# Offline check (no network, no AWS): the routes in internal/api/routes.go, the
# ROUTES list in scripts/smoke.sh (the 401 phase) and the authenticated `step`
# calls in scripts/smoke.sh must all describe the same set of routes.
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

norm() { sed -E 's/\{[^}]*\}/{}/g' | sort -u; }

sed -nE 's/.*Method: "([A-Z]+)", Path: "([^"]+)".*/\1 \2/p' "$root/internal/api/routes.go" | norm >"$tmp/code"
"$root/scripts/smoke.sh" --list-routes | norm >"$tmp/list"
# step <METHOD> <path> ...: strip quotes and query, turn $VAR / ${VAR} into {}.
sed -nE 's/^step ([A-Z]+) "?([^" ]+)"?( .*)?$/\1 \2/p' "$root/scripts/smoke.sh" |
  sed -E 's/\?[^ ]*//; s/\$\{?[A-Za-z_]+\}?/{}/g' | norm >"$tmp/steps"

rc=0
for set in list steps; do
  if ! diff -u "$tmp/code" "$tmp/$set" >"$tmp/diff"; then
    echo "smoke.sh $set differs from internal/api/routes.go:" >&2
    cat "$tmp/diff" >&2
    rc=1
  fi
done
[ "$rc" -eq 0 ] && echo "smoke dry run: $(wc -l <"$tmp/code" | tr -d ' ') routes covered"
exit "$rc"
