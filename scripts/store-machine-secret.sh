#!/usr/bin/env bash
# Usage: scripts/store-machine-secret.sh
# Copies the machine app client secret into SSM Parameter Store as a
# SecureString (CloudFormation cannot create one). The secret travels
# CLI -> jq -> CLI over pipes only; it is never put in a variable or printed.
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/lib.sh
. "$here/lib.sh"

pool=$(out UserPoolId)
client=$(out MachineClientId)

# jq -j writes the value with no trailing newline; -e fails on null.
af_aws cognito-idp describe-user-pool-client --user-pool-id "$pool" --client-id "$client" \
  --output json --no-cli-pager |
  jq -je '.UserPoolClient.ClientSecret' |
  af_aws ssm put-parameter --type SecureString --name /audiofile/machine-client-secret \
    --overwrite --value file:///dev/stdin --no-cli-pager >/dev/null

echo "machine client secret stored"
