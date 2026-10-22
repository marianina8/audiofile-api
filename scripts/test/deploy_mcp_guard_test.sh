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

# A failing AWS lookup after a successful deploy prints only a pointer to the
# private log, never the CLI's error text (it can carry ARNs and account ids).
mkdir "$tmp/okbin"
printf '#!/bin/sh\nexit 0\n' >"$tmp/okbin/sam"
chmod +x "$tmp/okbin/sam"
for step in describe-stack-resources describe-stacks create-deployment; do
  cat >"$tmp/okbin/aws" <<STUB
#!/bin/sh
case "\$*" in
  *$step*) echo "An error occurred: arn:aws:cloudformation:us-west-2:123456789012:stack/x" >&2; exit 254 ;;
  *describe-stack-resources*) echo "arn:nested-host" ;;
  *ApiId*) echo "api123" ;;
  *) echo "value" ;;
esac
STUB
  chmod +x "$tmp/okbin/aws"
  if env PATH="$tmp/okbin:$PATH" TMPDIR="$tmp" "$root/scripts/deploy-mcp.sh" "$root/servers/generated" >"$tmp/out" 2>&1; then
    echo "FAIL - deploy succeeded although $step failed" >&2; exit 1
  fi
  if grep -q 123456789012 "$tmp/out" || ! grep -q "lookup failed; see " "$tmp/out"; then
    echo "FAIL - $step failure output:" >&2; cat "$tmp/out" >&2; exit 1
  fi
  log=$(sed -n 's/.*lookup failed; see //p' "$tmp/out")
  grep -q 123456789012 "$log" || { echo "FAIL - $step error not in the private log $log" >&2; exit 1; }
  echo "ok - $step failure prints only the log path"
done
echo "deploy-mcp guard test: PASS"
