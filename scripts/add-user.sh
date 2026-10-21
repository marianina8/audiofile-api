#!/usr/bin/env bash
# Usage: scripts/add-user.sh <email>
# Creates a pool user (Cognito emails a temporary password) and adds them to
# the mcp-users group. Safe to re-run: an existing user is only re-added to
# the group. The email is never printed. The pool is a shared library with no
# per-user data, so only add people you trust with the whole library.
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/lib.sh
. "$here/lib.sh"

email="${1:-}"
case "$email" in
  ?*@?*.?*) ;;
  *) echo "usage: scripts/add-user.sh <email>" >&2; exit 2 ;;
esac

pool=$(out UserPoolId)
created=1
err=$(AF_POOL="$pool" AF_U="$email" jq -n '{
    UserPoolId: env.AF_POOL, Username: env.AF_U,
    UserAttributes: [{Name: "email", Value: env.AF_U}, {Name: "email_verified", Value: "true"}],
    DesiredDeliveryMediums: ["EMAIL"]}' |
  af_aws cognito-idp admin-create-user --cli-input-json file:///dev/stdin \
    --no-cli-pager 2>&1 >/dev/null) || {
  if printf '%s' "$err" | grep -q UsernameExistsException; then
    created=0
  else
    echo "admin-create-user failed: $(printf '%s' "$err" | grep -o 'An error occurred ([A-Za-z]*)' || echo unknown error)" >&2
    exit 1
  fi
}

AF_POOL="$pool" AF_U="$email" jq -n '{UserPoolId: env.AF_POOL, Username: env.AF_U, GroupName: "mcp-users"}' |
  af_aws cognito-idp admin-add-user-to-group --cli-input-json file:///dev/stdin --no-cli-pager

if [ "$created" -eq 1 ]; then
  echo "user created and added to mcp-users"
else
  echo "user already exists; ensured membership in mcp-users"
fi
