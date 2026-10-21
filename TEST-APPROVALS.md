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
| Claude Desktop | **no** | **no** (after remove + re-add) | per-tool setting; both tools showed **Always allow** in connector settings | First run: a prompt with Always allow appeared for delete; after re-adding, both tools defaulted to Always allow and ran without asking. Desktop does not appear to use `readOnlyHint`/`destructiveHint` to pick defaults; the user must set "Needs approval" per tool |
| Claude Code (default mode) | **yes** | **yes** | standard "Do you want to proceed?" permission prompt | Prompts for every MCP tool regardless of `readOnlyHint`/`destructiveHint`; in auto mode it asked for neither (first run). After the delete Claude reported "apple and cherry are still there" without banana, which is wrong (the stateless test server always returns all three); it didn't re-check |
| ChatGPT (Plugins › custom MCP server) | **no**: ran straight away | **yes**: "Allow ChatGPT to use things-test? Deletes the persistent item named \"banana\"." | **Deny / Allow once** (no always-allow) | OAuth via the bridge worked (CIMD). Evidence that ChatGPT honours `readOnlyHint`: read ran freely, destructive asked |


### Summary across apps (2026-10-21)
| App | Uses the hints? | Observed |
|---|---|---|
| ChatGPT | yes, plus its own judgement from names | read-only ran freely, destructive asked; with no hints it still asked before delete but not before create |
| Claude Code | no | default mode asks for every tool; auto mode asks for none |
| Claude Desktop | no (for defaults) | both tools defaulted to Always allow; approval is a per-tool user setting |

### ChatGPT with the generated server (no annotations at all), 2026-10-21
| Tool (generated, unmarked) | Kind | Asked first? |
|---|---|---|
| `listTracks` | read | **no** |
| `createPlaylist` (x2) | write | **no** |
| `deletePlaylist` | destructive | **yes** |

Takeaway: ChatGPT does not rely only on `readOnlyHint`/`destructiveHint`. With no marks it still judged a delete risky, from the tool's name and description, but let unmarked **writes** (create) through without asking. On `things-test`, the read-only-marked tool ran freely and the destructive-marked one asked (Deny / Allow once).

Also found: ChatGPT appends `?utm_source=chatgpt.com` to links it shows, which breaks raw presigned S3 links (SignatureDoesNotMatch). Short `/play/...` links are unaffected.

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
