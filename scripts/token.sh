#!/usr/bin/env bash
# Usage: source scripts/token.sh
# Signs in as the pool user against the no-secret CLI app client and exports
# TOKEN (access token). Prints only "TOKEN set (expires in N min)".
# Set AUDIOFILE_USER to skip the email prompt. Nothing secret is echoed.

_af_sourced=0
if [ -n "${ZSH_VERSION:-}" ]; then
  case "${ZSH_EVAL_CONTEXT:-}" in *:file*) _af_sourced=1 ;; esac
  _af_self=$(eval 'printf %s "${(%):-%x}"')
else
  [[ "${BASH_SOURCE[0]}" != "$0" ]] && _af_sourced=1
  _af_self="${BASH_SOURCE[0]}"
fi
if [ "$_af_sourced" -ne 1 ]; then
  echo "token.sh must be sourced, not executed:  source scripts/token.sh" >&2
  exit 1
fi

# shellcheck source=scripts/lib.sh
. "$(cd "$(dirname "$_af_self")" && pwd)/lib.sh"

_af_token() {
  local user pw client resp challenge expires
  user="${AUDIOFILE_USER:-}"
  [ -n "$user" ] || user=$(read_line "Email: ")
  pw=$(read_secret "Password: ")
  [ -n "$user" ] && [ -n "$pw" ] || { echo "email and password are required" >&2; return 1; }

  client=$(out CliClientId) || return 1

  # Credentials go to the AWS CLI on stdin, not argv, so they never appear in ps.
  resp=$(AF_C="$client" AF_U="$user" AF_P="$pw" jq -n '{
      AuthFlow: "USER_PASSWORD_AUTH", ClientId: env.AF_C,
      AuthParameters: {USERNAME: env.AF_U, PASSWORD: env.AF_P}}' |
    af_aws_json cognito-idp initiate-auth \
      --output json --no-cli-pager) || { echo "sign-in failed" >&2; return 1; }

  challenge=$(printf '%s' "$resp" | jq -r '.ChallengeName // empty')
  if [ "$challenge" = "NEW_PASSWORD_REQUIRED" ]; then
    echo "A new password is required. Run: scripts/set-password.sh" >&2
    return 1
  elif [ -n "$challenge" ]; then
    echo "unsupported sign-in challenge: $challenge" >&2
    return 1
  fi

  TOKEN=$(printf '%s' "$resp" | jq -r '.AuthenticationResult.AccessToken // empty')
  expires=$(printf '%s' "$resp" | jq -r '.AuthenticationResult.ExpiresIn // 3600')
  [ -n "$TOKEN" ] || { echo "no access token returned" >&2; return 1; }
  export TOKEN
  echo "TOKEN set (expires in $((expires / 60)) min)"
}

_af_token
_af_rc=$?
unset -f _af_token
unset _af_sourced _af_self
# Return (not exit) so a failure never closes the caller's shell.
return "$_af_rc"
