# B-roll shot list (screen recordings)

What to do while the screen is recording, for the clips that can be shot now. Server B (the hand-built Go server) isn't built yet; its clips come after the build guide.

Each clip becomes its own insert in the edit (`I##_what-it-shows.mp4`). Do every action slowly and pause 2–3 seconds on each result, so there's a clean frame to hold.

Go top to bottom: each clip builds on the one before. Keep this file open on a second screen or your phone, outside the window you are recording.

## Before you hit record (every session)

- [ ] Terminal in the repo: `cd ~/Code/github.com/marianina8/audiofile-api`
- [ ] **Do Not Disturb** on. **Personal Hotspot** off (the Wi-Fi menu shows your phone's name).
- [ ] Close Finder, Activity Monitor, Slack, mail, and anything else that isn't in the shot.
- [ ] Record **the app window**, not the whole screen.
- [ ] Terminal: neutral prompt, so the computer name doesn't show: `PROMPT='%~ %# '`. Bump the font size (⌘+ two or three times).
- [ ] Browser: a **private window**. Bookmarks bar hidden (⇧⌘B). No other tabs.
- [ ] AWS sign-in done **before** recording: `aws sso login --profile audiofile-admin --use-device-code`
- [ ] API token loaded **before** recording: `source scripts/token.sh` (prints only "TOKEN set").
- [ ] Clean state: `scripts/reset-playlists.sh`
- [ ] Never on screen: the AWS console, `aws sts get-caller-identity`, devtools or view-source on a player page, upload URLs.
- [ ] **Sign-in pages show your email.** Pause the recording while you type it, and resume on the "connected" screen. The OAuth flow is covered by the animation.

---

## I01: Repo tour (about 1 min)

Shows what the API is. Use your editor (VS Code), full window, a large font.

1. `README.md`: scroll slowly through the top: credit to the book, and the endpoint table.
2. `openapi.yaml`: scroll through `paths:` at a steady speed. This is the "34 routes" visual.
3. `COMPARISON.md`: the *At a glance* table (book vs now). Hold.

## I02: The API alone builds a workout mix (about 40 s)

Proof the endpoints work before any AI is involved, and the chain the model has to discover.

1. Terminal: `scripts/try-workout-mix.sh`
2. Hold on each numbered step as it prints (search, create, add, reorder).
3. The player opens in the browser. Press ▶, let a track play for 5–10 s (use system audio if you're capturing sound), then skip to the next track.
4. Afterwards, off camera: `scripts/reset-playlists.sh`

## I03: Generate Server A (about 1 min)

"One tool per endpoint, in one command."

1. Terminal: `scripts/generate-server.sh --patch`
2. Hold on the last line: **`generated 33 tools into …`**. That number gets boxed in the edit.
3. Optional: open `servers/generated/app/src/index.ts` and scroll through the tool definitions quickly, which shows the volume.

## I04: Deploy Server A (about 1 min, mostly waiting)

> ⚠️ **This creates the real Server A stack on AWS** (a Lambda and an API Gateway, pennies a month, the same as the rehearsal). Tell Claude before your first run.

1. Terminal: `scripts/deploy-mcp.sh servers/generated`
2. Let it run. It prints nothing until the end; trim the wait in the edit.
3. Hold on: **`deployed audiofile-generated: https://…/mcp`**. The URL is fine to show.

## I05: Server A in the Inspector, "what the AI sees" (about 2 min)

Shows what an MCP server is made of, and what the generator gave us: 33 tools, one per endpoint, with no hints.

1. Terminal: `npx @modelcontextprotocol/inspector@2.10.1`. Let the browser open at localhost:6274.
2. Left panel: **Transport Type** = `Streamable HTTP`, then paste the Server A URL from I04. Pause on it.
3. Click **Connect**. ⏸ **Pause the recording** for the sign-in, then resume once it says *Connected*.
4. **Tools** tab, then **List Tools**. Scroll the whole list slowly, top to bottom: this is the "33 tools" visual.
5. Click `listTracks`, `listAudio`, `searchTracks` and `createPlaylist`. Read each description. Hold on any that are vague or overlap: `listTracks` vs `listAudio` (the book route) is a good one: two tools that both list tracks.
6. Click `deletePlaylist`. Hold on the **missing** destructive tag (compare it with `listTracks`: neither is tagged).
7. Run `listTracks` (read-only, safe). Hold on the raw JSON result.
8. Don't run any delete or upload tool here.
9. If the Inspector shows a warning (for example *schema portability*), hover it and hold.

The same clip with Server B comes later: a handful of tools, clear descriptions, read-only and destructive tags. Put the two side by side in the edit.

## I06: Server A in ChatGPT, the stumble (about 2–3 min)

The heart of the video: the generated server's 33 tools against a simple request.

Before: remove the old connector from your last test, so you add it fresh on camera.

1. ChatGPT: add the custom connector, the same way you added `things-test`. Paste the Server A URL. ⏸ **Pause for sign-in**, then resume.
2. New chat, with the connector turned on. Type: **"create a workout mix that's upbeat"**
3. Let it work. **Don't help it.** Capture every tool call it makes, any wrong guesses, retries, or a "which tracks do you mean?" question. Expand the tool-call details if ChatGPT offers it.
4. If it creates a playlist: click the play link, press ▶, hold.
5. Type: **"delete that playlist"**. Hold on the **Deny / Allow once** prompt, then click **Allow once**.
6. Afterwards: `scripts/reset-playlists.sh`

## I07: Server A in Claude (about 2 min)

The same prompt in a second app.

Before: remove the old connector. In the connector's tool settings, set every `delete*` tool to **Needs approval**, and record that settings screen too: it's the "the app decides, not the server" point.

1. Add the connector with the Server A URL. ⏸ **Pause for sign-in**, then resume.
2. New chat: **"create a workout mix that's upbeat"**. Capture the tool calls.
3. **"delete that playlist"**. Hold on the approval prompt.
4. Afterwards: `scripts/reset-playlists.sh`

## Later (after the Server B guide)

- I08: building Server B (code, `go run`), then the Inspector clip from I05 repeated against Server B
- I09–I10: the same prompt in ChatGPT and Claude, with Server B getting it right
- Side-by-side tool lists: 33 tools against a handful

## After each session

- [ ] `scripts/reset-playlists.sh`
- [ ] Note which takes were good (clip number + rough time), so the edit can find them.
