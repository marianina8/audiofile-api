#!/usr/bin/env bash
# Usage: scripts/deploy-mcp.sh <server-template-dir>   (e.g. servers/mcptest)
# Builds and deploys the MCP server stack in <dir>. Its template.yaml nests
# mcphost/template.yaml and its samconfig.toml names the stack. The script
# then creates a fresh API Gateway deployment of stage v1 on the nested
# mcp-host REST API. CloudFormation redeploys the stage only when the API's
# shape changes (the hashed ApiDeployment logical id), so this step keeps
# parameter changes such as BridgeUrl or ServerFunctionArn from being served
# stale.
# It prints only "deployed <ServerName>: <McpUrl>". sam output contains ARNs
# with the account id, so it goes to a private log that is removed on success
# and kept (path printed) on failure.
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/lib.sh
. "$here/lib.sh"

usage() { echo "usage: scripts/deploy-mcp.sh <server-template-dir>" >&2; exit 2; }

dir="${1:-}"
[ -n "$dir" ] && [ -f "$dir/template.yaml" ] || usage
[ -f "$dir/samconfig.toml" ] || { echo "$dir/samconfig.toml not found (it names the stack)" >&2; exit 1; }
stack=$(sed -n 's/^[[:space:]]*stack_name[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' "$dir/samconfig.toml" | head -n 1)
[ -n "$stack" ] || { echo "no stack_name in $dir/samconfig.toml" >&2; exit 1; }

export AWS_PROFILE="${AWS_PROFILE:-audiofile-admin}"
export AWS_REGION="${AWS_REGION:-us-west-2}"

logdir=$(umask 077 && mktemp -d)
log="$logdir/sam.log"
keep_log=0
trap '[ "$keep_log" -eq 1 ] || rm -rf "$logdir"' EXIT
fail() { keep_log=1; echo "$1 failed; log: $log" >&2; exit 1; }

(cd "$dir" && sam build) >"$log" 2>&1 || fail "sam build"
(cd "$dir" && sam deploy --region "$AWS_REGION" \
  --no-confirm-changeset --no-fail-on-empty-changeset) >>"$log" 2>&1 || fail "sam deploy"

# The nested mcp-host stack is the one with an ApiId output.
host=""
nested=$(af_aws cloudformation describe-stack-resources --stack-name "$stack" \
  --query "StackResources[?ResourceType=='AWS::CloudFormation::Stack'].PhysicalResourceId" \
  --output text --no-cli-pager)
for s in $nested; do
  id=$(out ApiId "$s")
  if [ -n "$id" ] && [ "$id" != "None" ]; then
    [ -z "$host" ] || { echo "more than one nested mcp-host stack in $stack" >&2; exit 1; }
    host="$s"
    api="$id"
  fi
done
[ -n "$host" ] || { echo "no nested mcp-host stack (ApiId output) in $stack" >&2; exit 1; }

af_aws apigateway create-deployment --rest-api-id "$api" --stage-name v1 \
  --description "deploy-mcp $(date -u +%FT%TZ)" --no-cli-pager >/dev/null

echo "deployed $(out ServerName "$host"): $(out McpUrl "$host")"
