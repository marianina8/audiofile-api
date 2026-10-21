# TEST-APPROVALS: how Claude and ChatGPT ask before using tools

A throwaway MCP server, **things-test**, has exactly two tools:

| Tool | What it does | Annotation |
|---|---|---|
| `list_things` | returns `apple`, `banana`, `cherry` | `readOnlyHint: true` |
| `delete_thing` | "deletes" a thing by name (nothing real happens) | `destructiveHint: true` |

The goal is to record what each app does before calling a read-only tool and before calling a destructive one. Nothing here touches the music library.

**Server URL:** the `McpUrl` output of the `mcp-test` stack:

```sh
aws cloudformation describe-stacks --stack-name mcp-test --profile audiofile-admin \
  --query "Stacks[0].Outputs[?OutputKey=='McpUrl'].OutputValue" --output text
```

It ends in `/v1/mcp`. Sign-in uses your Cognito user (the one from `scripts/set-password.sh`), not the AWS console login.

> Recording? The URL is fine to show. Keep the sign-in screen, your email and any tokens off screen.

---

## 1. Claude (claude.ai web or Claude Desktop)

1. **Settings › Connectors › Add custom connector.**
2. Name: `things-test`. URL: paste the `McpUrl`. Leave the advanced OAuth fields empty, because Claude discovers them itself.
3. Click **Connect**. A Cognito sign-in page opens. Sign in.
4. In a new chat, check that the connector is enabled (tools menu), then ask:
   - **Prompt A:** `List the things.`
   - **Prompt B:** `Delete the banana.`
5. For each prompt, note whether Claude asked before calling the tool, and which options it offered (e.g. *Allow once*, *Always allow*, *Deny*).

## 2. Claude Code

```sh
claude mcp add --transport http things-test <McpUrl>
claude          # then type /mcp, pick things-test, choose Authenticate
```

The browser opens a Cognito sign-in page. After you sign in, run Prompt A and Prompt B as above and note the prompts Claude Code shows.

To clean up afterwards: `claude mcp remove things-test`.

## 3. ChatGPT (custom MCP server)

> Current UI (2026-10): **chatgpt.com/plugins › + › Add custom MCP server** › name, Public endpoint URL, OAuth (leave client fields empty) › *I understand and want to continue* › *Create as a plugin*. In a chat, type `@` and pick the plugin. The older developer-mode steps below may no longer match.

1. **Settings › Apps & Connectors › Advanced settings** → turn on **Developer mode**.
2. **Settings › Apps & Connectors › Create** (or the *+* in the connectors list):
   - Name: `things-test`
   - MCP server URL: the `McpUrl`
   - Authentication: **OAuth**
3. If ChatGPT shows a **callback / redirect URL** that is *not* `https://chatgpt.com/connector_platform_oauth_redirect`, copy the part after `/connector/oauth/` and tell Claude. The bridge then needs one redeploy with that id (`ChatGPTCallbackId`) before sign-in will work.
4. Click **Create / Connect** and sign in on the Cognito page.
5. In a new chat, enable the connector for that chat (developer mode tools), then run Prompt A and Prompt B.
6. Note what ChatGPT shows before each call (confirmation dialog? "remember for this conversation"?).

---

## Results (fill in)

| App | Asked before `list_things`? | Asked before `delete_thing`? | Options offered | Notes |
|---|---|---|---|---|
| Claude (web) | | | | |
| Claude Desktop | not recorded (re-check) | **yes**: prompt appeared | included **Always allow** (accidentally chosen) | Tool calls shown as "Listed things-test things" / "Used things-test". Claude noticed the delete didn't stick (expected: the test server is stateless) and promised to ask in chat before future deletes |
| Claude Code | | | | |
| ChatGPT (Plugins › custom MCP server) | **no**: ran straight away | **yes**: "Allow ChatGPT to use things-test? Deletes the persistent item named \"banana\"." | **Deny / Allow once** (no always-allow) | OAuth via the bridge worked (CIMD). Evidence that ChatGPT honours `readOnlyHint`: read ran freely, destructive asked |

### Things worth noting while you test
- Did the app show the tool's **title** ("List things" / "Delete thing") or its **name**?
- Did "Always allow" on `list_things` carry over to `delete_thing`? (It shouldn't.)
- Anything confusing in the sign-in flow that the OAuth storyboard should explain?

## If something goes wrong

Tell Claude what the app showed. The bridge logs every step without tokens, so Claude can read them:

```sh
aws logs tail /aws/lambda/$(aws cloudformation describe-stack-resource --stack-name mcp-auth \
  --logical-resource-id BridgeFunction --profile audiofile-admin \
  --query StackResourceDetail.PhysicalResourceId --output text) --since 15m --profile audiofile-admin
```
