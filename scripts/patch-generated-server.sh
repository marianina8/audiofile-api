#!/usr/bin/env bash
# Usage: scripts/patch-generated-server.sh <generated-dir>
# Makes openapi-mcp-generator 4.1.0 output (streamable HTTP) safe to host on
# Lambda behind mcp-host. The tool list stays exactly as generated.
#   a. request-body double read (crash on the first request)
#   b. stateless streamable HTTP: new Server + transport per request
#   c. PORT from the environment
#   d. API auth: machine token from Cognito client_credentials
#      (src/machine-auth.ts), never the caller's token
# Idempotent. Every anchor is checked before anything changes; if the
# generated code differs from what is expected, it fails and changes nothing.
# Pins the server's runtime dependencies to the tested versions.
# Edits live in scripts/patch-generated-server/patch.mjs.
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
patcher="$here/patch-generated-server/patch.mjs"

dir="${1:-}"
[ -n "$dir" ] && [ -f "$dir/package.json" ] || {
  echo "usage: scripts/patch-generated-server.sh <generated-dir>" >&2
  exit 2
}

node "$patcher" "$dir" check

# Exact versions this patch was tested with (scripts/test/generated_server_test.sh).
# The generator writes ^ ranges; pinning keeps a new release from changing
# behaviour between rehearsal and filming. machine-auth.ts needs client-ssm.
pins=(
  @modelcontextprotocol/sdk@1.32.1
  hono@4.13.13
  @hono/node-server@1.19.17
  fetch-to-node@2.1.0
  @aws-sdk/client-ssm@3.1147.0
)
# Prints the pins that package.json or node_modules do not match exactly.
unpinned() {
  node -e '
    const fs = require("fs"), path = require("path");
    const [dir, ...pins] = process.argv.slice(1);
    const deps = JSON.parse(fs.readFileSync(path.join(dir, "package.json"), "utf8")).dependencies ?? {};
    for (const pin of pins) {
      const at = pin.lastIndexOf("@"), name = pin.slice(0, at), version = pin.slice(at + 1);
      let installed = "";
      try { installed = JSON.parse(fs.readFileSync(path.join(dir, "node_modules", name, "package.json"), "utf8")).version; } catch {}
      if (deps[name] !== version || installed !== version) console.log(pin);
    }' "$dir" "${pins[@]}"
}
if [ -n "$(unpinned)" ]; then
  (cd "$dir" && npm install --save-exact --no-audit --no-fund --loglevel=error "${pins[@]}") >/dev/null
  [ -z "$(unpinned)" ] || { echo "✗ could not pin: $(unpinned | tr '\n' ' ')" >&2; exit 1; }
  echo "✓ pinned tested versions: ${pins[*]}"
else
  echo "✓ already pinned: ${pins[*]}"
fi

# The stateless patch turns on JSON responses (no SSE stream); the pinned SDK
# has the option. Guard against it disappearing rather than mis-patching.
grep -rqs enableJsonResponse "$dir/node_modules/@modelcontextprotocol/sdk/dist/esm/server/" || {
  echo "✗ @modelcontextprotocol/sdk has no enableJsonResponse option; source files not patched" >&2
  exit 1
}
node "$patcher" "$dir" apply
