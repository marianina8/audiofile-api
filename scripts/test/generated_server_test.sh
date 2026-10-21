#!/usr/bin/env bash
# Offline end-to-end check of "generate -> patch -> run" for the generated MCP
# server (Server A). No AWS: the token endpoint and the API are local fakes
# (scripts/test/fake-upstream.mjs). Needs network for npx/npm only.
#
# Checks: generator output (33 tools); the patch script fails on a changed
# anchor without touching files; patching is idempotent and leaves the tool
# list byte-identical; the patched server builds, reads PORT, is stateless
# (two separate Inspector CLI sessions, no "Already connected", no session
# header, JSON responses, GET/DELETE /mcp -> 405); tools/list has 33 tools and
# no annotations; the API sees only the machine token (fetched once, cached),
# never the client's Authorization header; no token or secret in server logs.
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
INSPECTOR="@modelcontextprotocol/inspector@2.10.1"
CLIENT_TOKEN="client-user-token-must-not-leak"
export FAKE_CLIENT_ID="fake-machine-client"
export FAKE_CLIENT_SECRET="fake-machine-secret-must-not-log"
export FAKE_MACHINE_TOKEN="fake-machine-token-must-not-log"

tmp=$(mktemp -d)
pids=()
cleanup() {
  for p in ${pids[@]+"${pids[@]}"}; do kill "$p" 2>/dev/null || true; wait "$p" 2>/dev/null || true; done
  rm -rf "$tmp"
}
trap cleanup EXIT

ok() { echo "ok - $1"; }
fail() {
  echo "FAIL - $1" >&2
  [ -z "${2:-}" ] || { echo "--- $2" >&2; cat "$2" >&2; }
  exit 1
}
# Content hash of a generated dir, ignoring node_modules and build output.
tree_sum() {
  (cd "$1" && find . \( -name node_modules -o -name build \) -prune -o -type f -print |
    LC_ALL=C sort | xargs cksum)
}
tool_map() { sed -n '/^const toolDefinitionMap/,/^\]);/p' "$1/src/index.ts"; }
free_port() {
  node -e 'const s=require("net").createServer().listen(0,"127.0.0.1",()=>{console.log(s.address().port);s.close()})'
}
wait_http() { # wait_http <url>: up to ~20 s for any HTTP answer
  for _ in $(seq 1 100); do
    curl -s -o /dev/null "$1" && return 0
    sleep 0.2
  done
  return 1
}

app="$tmp/app"

# --- generate -----------------------------------------------------------
"$root/scripts/generate-server.sh" --out "$app" >"$tmp/gen.out" 2>&1 || fail "generate-server.sh" "$tmp/gen.out"
grep -q "33 tools" "$tmp/gen.out" || fail "generator reports 33 tools" "$tmp/gen.out"
ok "generated 33 tools"
if "$root/scripts/generate-server.sh" --out "$app" >"$tmp/gen2.out" 2>&1; then
  fail "generate-server.sh refuses to overwrite without --force" "$tmp/gen2.out"
fi
ok "generate-server.sh refuses to overwrite without --force"
cp -R "$app" "$tmp/pristine"

# --- a changed anchor fails loudly and changes nothing ------------------
cp -R "$tmp/pristine" "$tmp/mismatch"
perl -pi -e 's/toReqRes\(c\.req\.raw\)/toReqRes(c.req.raw as any)/' "$tmp/mismatch/src/streamable-http.ts"
before=$(tree_sum "$tmp/mismatch")
if "$root/scripts/patch-generated-server.sh" "$tmp/mismatch" >"$tmp/mismatch.out" 2>&1; then
  fail "patch fails on a changed anchor" "$tmp/mismatch.out"
fi
grep -q "anchor not found" "$tmp/mismatch.out" || fail "anchor failure names the anchor" "$tmp/mismatch.out"
[ "$before" = "$(tree_sum "$tmp/mismatch")" ] || fail "failed patch left files untouched"
[ ! -d "$tmp/mismatch/node_modules" ] || fail "failed patch ran npm install"
ok "patch fails on a changed anchor and changes nothing"

# --- patch, twice -------------------------------------------------------
"$root/scripts/patch-generated-server.sh" "$app" >"$tmp/patch1.out" 2>&1 || fail "first patch run" "$tmp/patch1.out"
cat "$tmp/patch1.out"
first=$(tree_sum "$app")
"$root/scripts/patch-generated-server.sh" "$app" >"$tmp/patch2.out" 2>&1 || fail "second patch run" "$tmp/patch2.out"
[ "$first" = "$(tree_sum "$app")" ] || fail "second patch run changed files" "$tmp/patch2.out"
grep -q "already" "$tmp/patch2.out" || fail "second run reports already patched" "$tmp/patch2.out"
ok "patch is idempotent"
diff <(tool_map "$tmp/pristine") <(tool_map "$app") >"$tmp/toolmap.diff" || fail "tool definitions unchanged" "$tmp/toolmap.diff"
ok "tool definitions byte-identical to the generated ones"

# --- build and run ------------------------------------------------------
(cd "$app" && npm run build) >"$tmp/build.out" 2>&1 || fail "npm run build" "$tmp/build.out"
ok "patched server builds"

FAKE_LOG="$tmp/upstream.log" node "$root/scripts/test/fake-upstream.mjs" >"$tmp/upstream.port" &
pids+=("$!")
for _ in $(seq 1 50); do [ -s "$tmp/upstream.port" ] && break; sleep 0.1; done
up=$(head -n 1 "$tmp/upstream.port")
[ -n "$up" ] || fail "fake upstream started"

port=$(free_port)
(cd "$tmp" && exec env -i PATH="$PATH" HOME="$tmp" PORT="$port" \
  API_BASE_URL="http://127.0.0.1:$up" TOKEN_URL="http://127.0.0.1:$up/oauth2/token" \
  MACHINE_CLIENT_ID="$FAKE_CLIENT_ID" MACHINE_CLIENT_SECRET="$FAKE_CLIENT_SECRET" \
  node "$app/build/index.js" --transport=streamable-http) >"$tmp/server.log" 2>&1 &
pids+=("$!")
mcp="http://127.0.0.1:$port/mcp"
wait_http "http://127.0.0.1:$port/health" || fail "server listens on \$PORT ($port)" "$tmp/server.log"
ok "server listens on PORT=$port"

status() { curl -s -o /dev/null -w '%{http_code}' "$@"; }
[ "$(status "$mcp")" = 405 ] || fail "GET /mcp -> 405"
[ "$(status -X DELETE "$mcp")" = 405 ] || fail "DELETE /mcp -> 405"
ok "GET and DELETE /mcp -> 405"

curl -s -D "$tmp/init.headers" -o "$tmp/init.body" "$mcp" \
  -H 'content-type: application/json' -H 'accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}'
grep -qi '^HTTP/[0-9.]* 200' "$tmp/init.headers" || fail "initialize -> 200" "$tmp/init.headers"
! grep -qi '^mcp-session-id:' "$tmp/init.headers" || fail "no mcp-session-id header (stateless)" "$tmp/init.headers"
grep -qi '^content-type: application/json' "$tmp/init.headers" || fail "JSON response (not SSE)" "$tmp/init.headers"
jq -e '.result.serverInfo.name' "$tmp/init.body" >/dev/null || fail "initialize result" "$tmp/init.body"
ok "initialize: 200, JSON, no session id"

insp() {
  npx --yes "$INSPECTOR" --cli "$mcp" --transport http \
    --header "Authorization: Bearer $CLIENT_TOKEN" "$@"
}
insp --method tools/list >"$tmp/tools.json" 2>"$tmp/tools.err" || fail "inspector tools/list" "$tmp/tools.err"
n=$(jq '.tools | length' "$tmp/tools.json")
[ "$n" = 33 ] || fail "tools/list returns 33 tools (got $n)" "$tmp/tools.json"
jq -e '[.tools[] | has("annotations")] | any | not' "$tmp/tools.json" >/dev/null ||
  fail "no tool has annotations" "$tmp/tools.json"
! grep -Eq 'readOnlyHint|destructiveHint|idempotentHint|openWorldHint' "$tmp/tools.json" ||
  fail "no hint keys" "$tmp/tools.json"
ok "tools/list: 33 tools, no annotations"

for i in 1 2; do
  insp --method tools/call --tool-name listTracks --tool-arg limit=5 >"$tmp/call$i.out" 2>&1 ||
    fail "listTracks call $i" "$tmp/call$i.out"
  ! grep -q "Already connected" "$tmp/call$i.out" || fail "call $i: no 'Already connected'" "$tmp/call$i.out"
  grep -q "API Response (Status: 200)" "$tmp/call$i.out" || fail "call $i: API 200" "$tmp/call$i.out"
  grep -q "Fake Upbeat Song" "$tmp/call$i.out" || fail "call $i: fake track returned" "$tmp/call$i.out"
done
! grep -q "Already connected" "$tmp/server.log" || fail "server log: no 'Already connected'" "$tmp/server.log"
ok "listTracks twice from two separate inspector sessions (stateless)"

api=$(jq -s '[.[] | select(.kind == "api" and .path == "/tracks")]' "$tmp/upstream.log")
[ "$(jq length <<<"$api")" = 2 ] || fail "API saw two /tracks calls" "$tmp/upstream.log"
jq -e --arg t "Bearer $FAKE_MACHINE_TOKEN" 'all(.authorization == $t)' <<<"$api" >/dev/null ||
  fail "API saw only the machine token" "$tmp/upstream.log"
! grep -q "$CLIENT_TOKEN" "$tmp/upstream.log" || fail "client token never reached the API" "$tmp/upstream.log"
tokens=$(jq -s '[.[] | select(.kind == "token")]' "$tmp/upstream.log")
[ "$(jq length <<<"$tokens")" = 1 ] || fail "one token request (cached)" "$tmp/upstream.log"
jq -e 'all(.ok)' <<<"$tokens" >/dev/null || fail "token request used client id/secret, grant and scopes" "$tmp/upstream.log"
ok "API got 'Bearer <machine token>' only; token fetched once and cached"

for s in "$FAKE_MACHINE_TOKEN" "$FAKE_CLIENT_SECRET" "$CLIENT_TOKEN"; do
  ! grep -q "$s" "$tmp/server.log" || fail "server log contains a token or secret" "$tmp/server.log"
done
ok "no token or secret in server logs"
echo "generated server test: PASS"
