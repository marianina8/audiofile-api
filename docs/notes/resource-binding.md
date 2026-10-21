# Cognito resource binding: finding

Date: 2026-10-21. Question (plan Task 19): does Cognito put the MCP server URL into the access token's `aud` claim when the authorize request carries `resource=<url>`, and does it survive a refresh?

**Answer: yes, with three conditions.** Verified with a temporary public client and a real sign-in.

| Attempt | Result |
|---|---|
| Classic hosted UI (`ManagedLoginVersion: 1`) + `resource` | Sign-in OK, but `resource` silently ignored: no `aud` |
| Managed login (`ManagedLoginVersion: 2`) + `resource` not registered | Code exchange fails with `invalid_grant` |
| Managed login, no `resource` | Works; no `aud` (expected) |
| Managed login + `resource` registered as a resource server identifier | **`aud` = the URL**, `token_use=access`, `cognito:groups=[mcp-users]`; **refresh keeps `aud`** |

Requirements this puts on the design:
1. The user pool domain must use **managed login** (`ManagedLoginVersion: 2`); the app client the bridge uses needs a `AWS::Cognito::ManagedLoginBranding` (Cognito-provided values are fine). Resource binding is documented as "exclusive to managed login".
2. Every MCP server URL used as `resource` must be registered as a **user pool resource server** whose identifier is exactly that URL. (Quota: 100 resource servers per pool, adjustable to 300.) A scope on it is required by the API but need not be requested.
3. `resource` goes on `/oauth2/authorize` only; the token request works without it, and refresh carries `aud` over.
4. The classic hosted UI needed `ALLOW_USER_SRP_AUTH` on the app client; keep it on clients used for browser sign-in.

No KMS fallback needed.
