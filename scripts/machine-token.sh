#!/usr/bin/env bash
# Usage: source scripts/machine-token.sh
# Exchanges the machine client credentials (secret read from SSM) for an
# access token via client_credentials and exports MACHINE_TOKEN.
# Prints only "MACHINE_TOKEN set (expires in N min)".

_af_sourced=0
if [ -n "${ZSH_VERSION:-}" ]; then
  case "${ZSH_EVAL_CONTEXT:-}" in *:file*) _af_sourced=1 ;; esac
  _af_self=$(eval 'printf %s "${(%):-%x}"')
else
  [[ "${BASH_SOURCE[0]}" != "$0" ]] && _af_sourced=1
  _af_self="${BASH_SOURCE[0]}"
fi
if [ "$_af_sourced" -ne 1 ]; then
  echo "machine-token.sh must be sourced, not executed:  source scripts/machine-token.sh" >&2
  exit 1
fi

# shellcheck source=scripts/lib.sh
. "$(cd "$(dirname "$_af_self")" && pwd)/lib.sh"

_af_machine_token() {
  local client domain secret resp tok expires
  client=$(out MachineClientId) || return 1
  domain=$(out CognitoDomain) || return 1   # full https://... base URL
  secret=$(af_aws ssm get-parameter --name /audiofile/machine-client-secret \
    --with-decryption --query Parameter.Value --output text --no-cli-pager) || {
    echo "could not read the machine client secret; run scripts/store-machine-secret.sh" >&2
    return 1
  }

  # Basic auth goes to curl through a config on stdin (printf is a builtin),
  # so the secret never appears in ps.
  resp=$(printf 'user = "%s:%s"\n' "$client" "$secret" |
    curl -sS -K - -X POST "${domain%/}/oauth2/token" \
      -H 'Content-Type: application/x-www-form-urlencoded' \
      --data-urlencode 'grant_type=client_credentials' \
      --data-urlencode 'scope=audiofile/read audiofile/write') || {
    echo "token request failed" >&2
    return 1
  }
  tok=$(printf '%s' "$resp" | jq -r '.access_token // empty')
  if [ -z "$tok" ]; then
    echo "no access token returned: $(printf '%s' "$resp" | jq -r '.error // "unknown error"')" >&2
    return 1
  fi
  expires=$(printf '%s' "$resp" | jq -r '.expires_in // 3600')
  MACHINE_TOKEN="$tok"
  export MACHINE_TOKEN
  echo "MACHINE_TOKEN set (expires in $((expires / 60)) min)"
}

_af_machine_token
_af_rc=$?
unset -f _af_machine_token
unset _af_sourced _af_self
return "$_af_rc"
