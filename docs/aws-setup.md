# AWS setup: the `audiofile-admin` identity

All AWS work for this project runs as a dedicated, narrowly scoped identity, never the root user and never a broad admin.

What was set up (2026-10-21), using IAM Identity Center:

1. Permission set **`AudiofileAdmin`**: 8-hour sessions, inline policy [`audiofile-admin-policy.json`](audiofile-admin-policy.json). It allows only what these stacks need (CloudFormation stacks `audiofile-*` / `mcp-*`, their roles, Lambda, API Gateway, Cognito, the project's DynamoDB tables, S3 buckets, SSM parameters under `/audiofile/`, log groups, Budgets) plus one Bedrock model for the local genre guesser.
2. Identity Center user **`audiofile-admin`** (its own email address, password and MFA), assigned `AudiofileAdmin` on the project account only.
3. CLI profile **`audiofile-admin`** in `~/.aws/config`, with its own `sso-session`.

Sign in before working:

```sh
aws sso login --profile audiofile-admin --use-device-code   # open the URL in a private window
aws sts get-caller-identity --profile audiofile-admin --query Arn --output text
```

Notes:
- Users created from the CLI get no invite email. Send one from IAM Identity Center → Users → Reset password → "Send an email to the user with instructions".
- If the browser reuses another portal session, `GetRoleCredentials` fails with "No access". Sign out (`aws sso logout --profile audiofile-admin`) and sign in again in a private window.
- The first call to the Mistral model on Bedrock triggers a one-time AWS Marketplace subscription, which the policy allows.
- To reproduce in another account: create the permission set with the policy file, create a user, assign it to the account, and add a matching profile.

## SAM CLI and SSO profiles

If `sam deploy --profile audiofile-admin` fails with `GetRoleCredentials ... No access` while `aws sts get-caller-identity --profile audiofile-admin` works, SAM is picking up another cached SSO session (both profiles share one start URL). Work around it by exporting the profile's short-lived credentials into the shell for that command:

```sh
eval "$(aws configure export-credentials --profile audiofile-admin --format env)"
export AWS_REGION=us-west-2
sam deploy ...            # or scripts/deploy-mcp.sh servers/mcptest
```

The variables expire with the SSO session (8 hours) and are never written to disk.
