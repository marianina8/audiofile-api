#!/usr/bin/env bash
# Offline check (no AWS): scripts/deploy-mcp.sh refuses rehearsal overrides
# that could touch the real stack or its mcp-host registration. sam and aws
# are stubs that fail loudly, so a broken guard cannot reach AWS.
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir "$tmp/bin"
for c in sam aws; do
  printf '#!/bin/sh\necho "STUB %s reached: guard did not stop the deploy" >&2\nexit 99\n' "$c" >"$tmp/bin/$c"
  chmod +x "$tmp/bin/$c"
done

# refuses <message> <env...>: deploy-mcp.sh exits non-zero before sam/aws and says <message>.
refuses() {
  local msg="$1"; shift
  if env PATH="$tmp/bin:$PATH" "$@" "$root/scripts/deploy-mcp.sh" "$root/servers/generated" >"$tmp/out" 2>&1; then
    echo "FAIL - deploy ran with: $*" >&2; exit 1
  fi
  if grep -q STUB "$tmp/out" || ! grep -q "$msg" "$tmp/out"; then
    echo "FAIL - expected '$msg' with: $*" >&2; cat "$tmp/out" >&2; exit 1
  fi
  echo "ok - refuses $*"
}
refuses "both STACK_NAME and SERVER_NAME" SERVER_NAME=audiofile-generated-rehearsal
refuses "both STACK_NAME and SERVER_NAME" STACK_NAME=mcp-generated-rehearsal
refuses "STACK_NAME must differ" STACK_NAME=mcp-generated SERVER_NAME=audiofile-generated-rehearsal
refuses "SERVER_NAME must differ" STACK_NAME=mcp-generated-rehearsal SERVER_NAME=audiofile-generated

# The valid rehearsal pair gets past the guard (and stops at the sam stub).
if env PATH="$tmp/bin:$PATH" TMPDIR="$tmp" STACK_NAME=mcp-generated-rehearsal SERVER_NAME=audiofile-generated-rehearsal \
  "$root/scripts/deploy-mcp.sh" "$root/servers/generated" >"$tmp/out" 2>&1; then
  echo "FAIL - stub sam succeeded?" >&2; exit 1
fi
grep -q "sam build failed" "$tmp/out" || { echo "FAIL - valid rehearsal pair was refused" >&2; cat "$tmp/out" >&2; exit 1; }
echo "ok - accepts STACK_NAME=mcp-generated-rehearsal SERVER_NAME=audiofile-generated-rehearsal"
echo "deploy-mcp guard test: PASS"
