#!/usr/bin/env bash
# Usage: [STACK_NAME=<stack>] [SERVER_NAME=<name>] scripts/deploy-mcp.sh <server-template-dir>
#        (e.g. servers/mcptest)
# Builds and deploys the MCP server stack in <dir>. Its template.yaml nests
# mcphost/template.yaml and its samconfig.toml names the stack. For a
# rehearsal stack set both STACK_NAME (instead of samconfig's stack_name) and
# SERVER_NAME (the template's ServerName parameter); both must differ from the
# defaults, so a rehearsal can never touch the real stack or its mcp-host
# registration. Set neither for the real stack. The script
# then creates a fresh API Gateway deployment of stage v1 on the nested
# mcp-host REST API. CloudFormation redeploys the stage only when the API's
# shape changes (the hashed ApiDeployment logical id), so this step keeps
# parameter changes such as BridgeUrl or ServerFunctionArn from being served
# stale.
# It prints only "deployed <ServerName>: <McpUrl>". sam output and AWS CLI
# errors contain ARNs with the account id, so they go to a private log that is
# removed on success and kept (path printed) on failure.
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/lib.sh
. "$here/lib.sh"

usage() {
  echo "usage: scripts/deploy-mcp.sh <server-template-dir>" >&2
  echo "       STACK_NAME=<stack> SERVER_NAME=<name> scripts/deploy-mcp.sh <server-template-dir>   (rehearsal: both or neither)" >&2
  exit 2
}

dir="${1:-}"
[ -n "$dir" ] && [ -f "$dir/template.yaml" ] || usage
[ -f "$dir/samconfig.toml" ] || { echo "$dir/samconfig.toml not found (it names the stack)" >&2; exit 1; }
stack=$(sed -n 's/^[[:space:]]*stack_name[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' "$dir/samconfig.toml" | head -n 1)
[ -n "$stack" ] || { echo "no stack_name in $dir/samconfig.toml" >&2; exit 1; }
deploy_args=()
if [ -n "${STACK_NAME:-}" ] || [ -n "${SERVER_NAME:-}" ]; then
  [ -n "${STACK_NAME:-}" ] && [ -n "${SERVER_NAME:-}" ] || {
    echo "set both STACK_NAME and SERVER_NAME for a rehearsal stack, or neither" >&2
    usage
  }
  # Default of the ServerName parameter: the first "Default:" after "ServerName:".
  default_server=$(awk '/^  ServerName:/ {p=1; next} p && /^  [^ ]/ {exit} p && /Default:/ {print $2; exit}' "$dir/template.yaml")
  [ -n "$default_server" ] || { echo "$dir/template.yaml has no ServerName parameter default" >&2; exit 1; }
  [ "$STACK_NAME" != "$stack" ] || { echo "STACK_NAME must differ from samconfig's stack_name ($stack)" >&2; exit 1; }
  [ "$SERVER_NAME" != "$default_server" ] || { echo "SERVER_NAME must differ from the template default ($default_server)" >&2; exit 1; }
  stack="$STACK_NAME"
  deploy_args=(--stack-name "$stack" --parameter-overrides "ServerName=$SERVER_NAME")
fi

export AWS_PROFILE="${AWS_PROFILE:-audiofile-admin}"
export AWS_REGION="${AWS_REGION:-us-west-2}"

logdir=$(umask 077 && mktemp -d)
log="$logdir/sam.log"
keep_log=0
trap '[ "$keep_log" -eq 1 ] || rm -rf "$logdir"' EXIT
fail() { keep_log=1; echo "$1 failed; log: $log" >&2; exit 1; }
lookup_failed() { keep_log=1; echo "lookup failed; see $log" >&2; exit 1; }

(cd "$dir" && sam build) >"$log" 2>&1 || fail "sam build"
(cd "$dir" && sam deploy --region "$AWS_REGION" ${deploy_args[@]+"${deploy_args[@]}"} \
  --no-confirm-changeset --no-fail-on-empty-changeset) >>"$log" 2>&1 || fail "sam deploy"

# The nested mcp-host stack is the one with an ApiId output.
host=""
nested=$(af_aws cloudformation describe-stack-resources --stack-name "$stack" \
  --query "StackResources[?ResourceType=='AWS::CloudFormation::Stack'].PhysicalResourceId" \
  --output text --no-cli-pager 2>>"$log") || lookup_failed
for s in $nested; do
  id=$(out ApiId "$s" 2>>"$log") || lookup_failed
  if [ -n "$id" ] && [ "$id" != "None" ]; then
    [ -z "$host" ] || { echo "more than one nested mcp-host stack in $stack" >&2; exit 1; }
    host="$s"
    api="$id"
  fi
done
[ -n "$host" ] || { echo "no nested mcp-host stack (ApiId output) in $stack" >&2; exit 1; }

af_aws apigateway create-deployment --rest-api-id "$api" --stage-name v1 \
  --description "deploy-mcp $(date -u +%FT%TZ)" --no-cli-pager >/dev/null 2>>"$log" || lookup_failed

name=$(out ServerName "$host" 2>>"$log") || lookup_failed
url=$(out McpUrl "$host" 2>>"$log") || lookup_failed
echo "deployed $name: $url"
