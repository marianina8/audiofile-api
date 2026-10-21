#!/usr/bin/env bash
# Usage: scripts/set-password.sh
# Completes the first sign-in: signs in with the emailed temporary password
# and answers the NEW_PASSWORD_REQUIRED challenge with a permanent one.
# Set AUDIOFILE_USER to skip the email prompt. Nothing secret is echoed.
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/lib.sh
. "$here/lib.sh"
trap 'stty echo </dev/tty 2>/dev/null || true' EXIT INT

user="${AUDIOFILE_USER:-}"
[ -n "$user" ] || user=$(read_line "Email: ")
temp=$(read_secret "Temporary password: ")
new=$(read_secret "New password (12+ chars, upper, lower, number): ")
again=$(read_secret "Confirm new password: ")
if [ -z "$user" ] || [ -z "$temp" ] || [ -z "$new" ]; then
  echo "email and both passwords are required" >&2; exit 1
fi
if [ "$new" != "$again" ]; then
  echo "new passwords do not match" >&2; exit 1
fi

client=$(out CliClientId)
resp=$(AF_C="$client" AF_U="$user" AF_P="$temp" jq -n '{
    AuthFlow: "USER_PASSWORD_AUTH", ClientId: env.AF_C,
    AuthParameters: {USERNAME: env.AF_U, PASSWORD: env.AF_P}}' |
  af_aws_json cognito-idp initiate-auth \
    --output json --no-cli-pager) || { echo "sign-in with the temporary password failed" >&2; exit 1; }

if [ "$(printf '%s' "$resp" | jq -r '.ChallengeName // empty')" != "NEW_PASSWORD_REQUIRED" ]; then
  echo "no password change is pending for this user" >&2; exit 1
fi

# Cognito wants the internal username from the challenge when one is given.
AF_C="$client" AF_U="$user" AF_P="$new" AF_S="$(printf '%s' "$resp" | jq -r '.Session')" \
  AF_ID="$(printf '%s' "$resp" | jq -r '.ChallengeParameters.USER_ID_FOR_SRP // empty')" \
  jq -n '{
    ClientId: env.AF_C, ChallengeName: "NEW_PASSWORD_REQUIRED", Session: env.AF_S,
    ChallengeResponses: {USERNAME: (if env.AF_ID != "" then env.AF_ID else env.AF_U end),
                         NEW_PASSWORD: env.AF_P}}' |
  af_aws_json cognito-idp respond-to-auth-challenge \
    --output json --no-cli-pager >/dev/null || { echo "setting the new password failed" >&2; exit 1; }

echo "password set. Now run: source scripts/token.sh"
