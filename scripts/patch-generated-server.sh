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

# machine-auth.ts reads the secret with the AWS SDK. npm install also
# installs the generated deps, so the SDK check below can look at them.
if ! grep -q '"@aws-sdk/client-ssm"' "$dir/package.json" || [ ! -d "$dir/node_modules" ]; then
  (cd "$dir" && npm install --no-audit --no-fund --loglevel=error @aws-sdk/client-ssm@^3) >/dev/null
  echo "✓ installed dependencies (+ @aws-sdk/client-ssm)"
fi

# JSON responses (no SSE stream) when this MCP SDK supports them.
flag=""
if grep -rqs enableJsonResponse "$dir/node_modules/@modelcontextprotocol/sdk/dist/esm/server/"; then
  flag="--json-response"
fi
node "$patcher" "$dir" apply ${flag:+"$flag"}
