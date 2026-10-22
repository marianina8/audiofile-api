# LEARN-MCP: a short hands-on lab

The video covers two things: **turning an API into an MCP server**, and **using that MCP server** from Claude and ChatGPT. This lab teaches just enough to explain both with confidence. It uses the throwaway `things-test` server (two tools: `list_things`, `delete_thing`), not the music library.

> Recording? Keep tokens, emails and account IDs off screen. The server URL and tool names are fine to show.

**Server URL** (`McpUrl` of the `mcp-test` stack): `https://jxnckktxp3.execute-api.us-west-2.amazonaws.com/v1/mcp`
**Sign-in:** your Cognito user (the one from `scripts/set-password.sh`).

---

## Step 1: What an MCP server is made of

**Do:**
1. In a normal Terminal: `npx @modelcontextprotocol/inspector`
   It opens **http://localhost:6274** in your browser. Keep that Terminal open.
2. In the Inspector's left panel:
   - **Transport Type:** `Streamable HTTP`
   - **URL:** the server URL above
   - Click **Connect**.
3. The server answers "401, sign in first". The Inspector runs the OAuth flow and opens the Cognito sign-in page. Sign in. You land back in the Inspector, connected.
4. Open the **Tools** tab › **List Tools**.
5. Click **`list_things`** and read its **name**, **description** and **annotations** (`readOnlyHint: true`). Then click `delete_thing` and compare (`destructiveHint: true`, plus an input field `name`).
6. Select `list_things` › **Run Tool**. Then try `delete_thing` with `name` = `banana`.

**Found in the lab:** the Inspector flagged a *schema portability* warning on `list_things`: the Go SDK infers `things` as `["null","array"]` (a Go slice can be nil). It's legal, but some clients reject multi-type schemas. In the curated Go server, make list fields non-nullable.

**What's happening, in plain words:**
- An MCP server is a **menu of tools**. Each tool has a name, a description written for the model, an input shape, and optional hints such as "read-only" or "destructive".
- The **app** (Inspector here; Claude or ChatGPT in real use) asks the server for that menu (`tools/list`).
- The **model** reads the descriptions and picks a tool that fits your request.
- The **app** calls it (`tools/call`) and passes the result back to the model.
- The Inspector is that same app with no model in it: you do the picking.

## Step 2: Using it in apps

You already did this while running `TEST-APPROVALS.md`: you connected `things-test` in Claude Desktop, Claude Code and ChatGPT, then asked for a list and a delete.

**What you saw (from TEST-APPROVALS.md):**
- **ChatGPT** used the hints: the read-only tool ran straight away, and the destructive one asked (*Deny / Allow once*). With a server that has no hints at all, it still asked before a delete but not before creating playlists.
- **Claude Code** in default mode asks before every tool, and in auto mode before none.
- **Claude Desktop** defaulted both tools to *Always allow*; you choose per tool whether it needs approval.

**Plain words:** the server describes its tools and can *hint* at which are risky. The app decides when to ask you, and different apps decide differently.

## Step 3: The login, in one picture

See [`OAUTH-FLOW-STORYBOARD.md`](OAUTH-FLOW-STORYBOARD.md): seven plain-language steps for the motion graphic.

---

## My explanations

*(One sentence per step, in your own words, saved after we talk it through.)*

- Step 1: An MCP server is a menu of tools, each with a name, a description, an input shape and optional hints like read-only or destructive. Any app can fetch the menu and call the tools, but because each tool describes itself, an app with an AI model in it can let the model pick the right tool for a request, and the server does the work, here by calling our API. *(Short version: the app holds the AI; the MCP server explains its tools to the AI and runs them, whether that means calling API endpoints, a database, files or a local program.)*
- Step 2: The app decides whether to ask before a tool runs. The server's hints only describe what kind of operation a tool is (read-only, destructive, or idempotent, meaning safe to repeat), and some apps like ChatGPT use them to decide when to ask, while others like Claude leave it to your per-tool settings.
- Step 3:
