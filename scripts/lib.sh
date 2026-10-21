#!/usr/bin/env bash
# Shared helpers for the audiofile scripts. Source it; do not execute it.
# It sets no shell options (token.sh and machine-token.sh are sourced into the
# caller's interactive shell) and works in bash and zsh.

# af_aws runs the AWS CLI with the project profile and region, without
# exporting either into the caller's shell.
af_aws() {
  local region="${AWS_REGION:-us-west-2}"
  AWS_PROFILE="${AWS_PROFILE:-audiofile-admin}" AWS_REGION="$region" \
    AWS_DEFAULT_REGION="$region" command aws "$@"
}

# af_aws_json <service> <operation> [args...]: read the request JSON from
# stdin and pass it to the AWS CLI as --cli-input-json file://<private temp
# file> (the CLI rejects file:///dev/stdin). The file lives in a mode-700 temp
# dir that a subshell-local EXIT trap removes, so nothing persists in the
# caller's shell and the exit status is preserved.
af_aws_json() {
  (
    umask 077
    d=$(mktemp -d) || exit 1
    trap 'rm -rf "$d"' EXIT
    cat >"$d/input.json"
    af_aws "$1" "$2" --cli-input-json "file://$d/input.json" "${@:3}"
  )
}

# out <OutputKey> [stack]: print one CloudFormation stack output value.
out() {
  local key="$1" stack="${2:-${AUDIOFILE_STACK:-audiofile-api}}"
  af_aws cloudformation describe-stacks --stack-name "$stack" \
    --query "Stacks[0].Outputs[?OutputKey=='${key}'].OutputValue | [0]" \
    --output text --no-cli-pager
}

# require_token: fail with a hint unless $TOKEN is set.
require_token() {
  if [ -z "${TOKEN:-}" ]; then
    echo "TOKEN is not set. Run: source scripts/token.sh" >&2
    return 1
  fi
}

# read_secret <prompt>: silent read from the terminal; prints the value to
# stdout (capture it with $(...)). Echo is restored even on interrupt.
read_secret() {
  local v=""
  printf '%s' "$1" >&2
  stty -echo </dev/tty
  IFS= read -r v </dev/tty || true
  stty echo </dev/tty
  printf '\n' >&2
  printf '%s' "$v"
}

# read_line <prompt>: visible read from the terminal.
read_line() {
  local v=""
  printf '%s' "$1" >&2
  IFS= read -r v </dev/tty || true
  printf '%s' "$v"
}
