# Generator dry run: openapi-mcp-generator against openapi.yaml

Run on 2026-10-21. Nothing generated is in this repo (output lives in `/tmp/server-a-dryrun`).
> **Update (later 2026-10-21):** the crash, single-session and hardcoded-port problems below, and the missing API auth, are now fixed by `scripts/patch-generated-server.sh` (run by `scripts/generate-server.sh --patch`; see `servers/generated/README.md`). The report below records the original findings. `getTrackStream` now returns a short `/play/<pid>` link, and the public `/play/{pid}` route is not in `openapi.yaml`, so the generated tool count stays 33.

Live API host used: `*.execute-api.us-west-2.amazonaws.com` (read-only GET calls only, machine token).

## Command and version

```
npx --yes openapi-mcp-generator@4.1.0 \
  --input openapi.yaml --output /tmp/server-a-dryrun \
  --transport=streamable-http --port=3000
```

Generator 4.1.0. It completed with no warnings or errors, even though the spec is OpenAPI 3.1.0.
Output: `src/index.ts` (1007 lines), `src/streamable-http.ts` (248 lines), `public/index.html`, `package.json`, `.env.example`, lint/jest configs.
Dependencies: `@modelcontextprotocol/sdk` 1.32.1, `hono` 4.13, `fetch-to-node` 2.1.0, `axios`, `zod`, `json-schema-to-zod`.

## Result in one paragraph

Generation: fine. `npm install` and `npm run build`: fine. `npm run start:http` as generated: it starts, then
**crashes on the first MCP request** (the `initialize` call). With a 2-line patch it works for one client
session, but a **second session fails** until the server is restarted. Both are real, and either could
happen on camera. Details below.

## Tools: 33

Count confirmed in source (33 entries) and over MCP `tools/list` (33 tools).

getHealth, createTrackUpload, listTracks, getTrack, deleteTrack, updateTrack, getTrackStream, getTrackStatus,
searchTracks, listArtists, getArtist, listArtistTracks, listArtistAlbums, listAlbums, getAlbum, listAlbumTracks,
listGenres, listTags, addTrackTags, removeTrackTag, listPlaylists, createPlaylist, getPlaylist, deletePlaylist,
updatePlaylist, listPlaylistTracks, addPlaylistTracks, reorderPlaylistTracks, removePlaylistTrack,
createPlaylistPlayer, uploadAudio, getAudio, listAudio

(Tool names are the operationIds. Nothing was renamed or truncated.)

### Five sample tool descriptions, verbatim

The description is only the operation `summary`. Nothing else from the spec is carried over.

| Tool | Description |
|---|---|
| listTracks | `List tracks` |
| searchTracks | `Search tracks` |
| createPlaylistPlayer | `Create a playlist player link` |
| listAudio | `List audio` |
| uploadAudio | `Upload audio` |

Other examples: getHealth = `Check health`, getTrackStream = `Get a track stream link`.
Parameters have no descriptions at all (the spec has none), e.g. searchTracks takes `q, artist, genre, year, bpm_min, bpm_max, energy_min, energy_max, limit` as bare typed fields.
Good on-camera point: the generator can only be as good as the OpenAPI text, and ours is terse.

## Annotations

None. `grep -i "annotations|readOnlyHint|destructiveHint|idempotentHint|openWorldHint" src/*.ts` finds nothing, and `tools/list`
returns only `name`, `description`, `inputSchema`. So a client sees `deleteTrack` and `getTrack` as equally safe.
(The internal tool map has `tags` and `deprecated` fields, but they are not sent to the client and are empty/false.)

## Build and start

- `npm install`: succeeds; "5 high severity vulnerabilities" reported by npm audit (transitive; not investigated).
- `npm run build` (`tsc && shx chmod 755 build/index.js`): succeeds, no errors.
- `npm run start:http` as generated, on Node 26.8.1 and also Node 22.23.3: server prints "running at http://localhost:3000", then on the first
  POST /mcp (initialize) the process **exits**:

  ```
  TypeError [ERR_INVALID_STATE]: Invalid state: ReadableStream is locked
      at FetchIncomingMessage._read (node_modules/fetch-to-node/dist/fetch-to-node/http-incoming.js:169:37)
  ```
  Cause: `src/streamable-http.ts` line 51 reads `await c.req.json()`, then line 54 passes the same (already consumed) request to
  `toReqRes(c.req.raw)`.
- Scratch fix used for the dry run (not in repo): clone before reading.
  ```
  const rawReq = c.req.raw.clone();      // new, before line 51
  const body = await c.req.json();       // line 51
  const { req, res } = toReqRes(rawReq); // line 54, was c.req.raw
  ```
  After that: `tools/list` works.
- Second problem: after one client has initialized, a **new client session** gets HTTP 500
  `Error: Already connected to a transport. Call close() before connecting to a new transport, or use a separate Protocol instance per connection.`
  The single module-level `Server` (`src/index.ts` line 59) is passed to `setupStreamableHttpServer` (line 925) and `this.server.connect(transport)`
  (streamable-http.ts line 86) is called for each new session. Each MCP Inspector CLI command opens a new session and never closes it, so
  **every inspector call after the first one failed until I restarted the server**. I restarted the server between calls.

## Live calls (patched server, restarted per call)

All through MCP Inspector CLI: `npx --yes @modelcontextprotocol/inspector@2.10.1 --cli http://localhost:3000/mcp --transport http --method tools/call --tool-name <name> --tool-arg key=value`
(`--transport http` is required; without it the CLI guesses wrong.) Note `tools/list` uses `--method tools/list` with the same flags.

- `tools/list`: 33 tools.
- `getHealth`: HTTP 200, `{"status":"ok"}`.
- `listTracks` (`limit=100`): HTTP 200, 18 items. About 15.6 KB of text for 18 tracks (a full JSON dump of every field, including empty ones).
- `searchTracks` (`bpm_min=120`): HTTP 200, 9 items, all with bpm >= 120.
- No create/update/delete/upload/player tools were called.
- Server stopped afterwards; `lsof -i :3000` shows nothing.

Tool results come back as one text block: `API Response (Status: 200):\n<pretty JSON>`. No structured output, no `isError` flag.

## Would break or surprise on camera

1. **It crashes on the first request as generated** (see Build and start). Do not run `npm run start:http` cold on camera without the 2-line patch.
2. **One session at a time.** The second client to connect gets a 500. Show it with Inspector plus a second client (Claude/ChatGPT) and it will fail.
3. **Stateful session map.** `transports: {[sessionId]: StreamableHTTPServerTransport}` (streamable-http.ts lines 26-27, filled at 95, deleted at 100).
   A request carrying `mcp-session-id` is only valid on the process that created the session. Behind Lambda that breaks: a cold start, a different
   concurrent instance, or the instance being recycled returns 400 `Bad Request: invalid session ID or method.` Non-initialize requests with no known session always get 400.
   GET /mcp returns 405 (no SSE stream), which is fine for stateless use.
4. **Port is hardcoded.** `.env.example` lists `PORT=3000`, but `src/index.ts` line 925 calls `setupStreamableHttpServer(server, 3000)`; `PORT` is never read.
   The port is whatever `--port` was at generation time. (`public/index.html` also is not served correctly: the static handler looks in `../../public`, one directory too high.)
5. **Bearer env var name is `BEARER_TOKEN_BEARERAUTH`** (`BEARER_TOKEN_` + scheme name `bearerAuth` upper-cased). Easy to mistype; if unset the call is sent with no auth and the API answers 401.
   Base URL comes from `API_BASE_URL`; without it the server uses the spec's `https://api.example.com` and every call fails. The spec `servers` entry is a placeholder.
6. **No OpenAPI 3.1 conversion warnings or failures.** The generator printed none, and the schemas it produced look correct (e.g. `size_bytes` keeps min/max, `content_type` enum). I did not find
   anything that failed. The one visible loss: `year` is an `integer` in the spec but becomes `"type":"number"` in the tool schema.
7. **No `[DEPRECATED]` or `(Tags: ...)` decoration.** Nothing in the spec is deprecated and the spec has no tags, so descriptions are the bare summaries. Those decorations would appear if tags/deprecation were added.
8. **Legacy `/upload` tool (`uploadAudio`).** Its input schema is `{"requestBody": {"type":"string","description":"Request body (content type: multipart/form-data)"}}`. A model cannot send a file through a string field,
   so the tool is useless but looks usable. The real flow is `createTrackUpload` then PUT to a presigned URL, which a model also cannot do. Neither upload tool is a good agent tool.
9. **Overlap: listAudio vs listTracks vs searchTracks.** Descriptions are `List audio`, `List tracks`, `Search tracks`. For "find something upbeat" a model has nothing to tell them apart:
   `listAudio` (GET /list, legacy, no parameters, returns every item) looks as good as `listTracks`. `searchTracks` has `bpm_min`/`energy_min` but no description explaining that energy is 0 to 1 or that bpm is tempo,
   so "upbeat" must be guessed. Likely behaviours: it picks `listAudio` or `listTracks` and filters by eye, or guesses numeric thresholds.
   Also `getAudio` (GET /request?id=) duplicates `getTrack`.
10. **Player tool.** `createPlaylistPlayer` is a POST (it creates something, so it is not read-only) and returns the raw API JSON text: `{"url": "...", "expires_at": "..."}` as a plain text block.
    No MCP app/UI resource, no embedded player. The model gets a URL string to paste; nothing renders. (I did not call it; this is from the spec and the generic response handling in `executeApiTool`.)
11. **No safety signalling.** 33 tools in one flat list including 8 destructive ones (deleteTrack, deletePlaylist, removeTrackTag, removePlaylistTrack, updateTrack, updatePlaylist, reorder, addTrackTags) with no hints. Clients that auto-approve "read-only" tools cannot tell.
12. **Large unfiltered output.** listTracks returns every field (15.6 KB for 18 tracks); with 100 tracks that is a lot of model context.
13. **Errors and validation come back as ordinary text**, not tool errors (`Invalid arguments for tool 'x': ...`, `Error: Unknown tool requested`), so a client cannot detect failure programmatically.
14. **No auth on the MCP endpoint itself.** Anyone who can reach port 3000 gets the machine token's access. CORS is wide open (`cors()` with defaults). Fine locally; not fine deployed without a gateway.

## Hosting it on mcp-host

- **Lambda Web Adapter layer (arm64):** aws/aws-lambda-web-adapter v1.1.0, `arn:aws:lambda:${AWS::Region}:753240598075:layer:LambdaAdapterLayerArm64:30`.
  Function runtime `nodejs22.x` (or 20.x), architecture `arm64`, handler `run.sh`, env `AWS_LAMBDA_EXEC_WRAPPER=/opt/bootstrap`.
- **Port:** LWA forwards to `AWS_LWA_PORT` (default 8080). The generated server ignores `PORT` (it is hardcoded to 3000), so either set `AWS_LWA_PORT=3000`
  (no code change, recommended) or regenerate with `--port=8080`. A one-line patch can make `index.ts` line 925 use `Number(process.env.PORT ?? 3000)`.
- **Startup command:** `run.sh` containing `exec node build/index.js --transport=streamable-http`. Ship `build/`, `node_modules/` (prod deps only) and `package.json`. Set `API_BASE_URL` and
  `BEARER_TOKEN_BEARERAUTH` (from Secrets Manager, not plain env) on the function. Set `AWS_LWA_READINESS_CHECK_PATH=/health` (the server exposes GET /health).
- **Minimal patch to make it stateless** (about 25 lines, 2 files):
  1. Required either way: the `c.req.raw.clone()` fix at streamable-http.ts lines 51 and 54.
  2. In `src/index.ts`, wrap lines 59-62 (`const server = new Server(...)`) and the two `setRequestHandler` calls in a `createServer()` factory, and pass the factory to `setupStreamableHttpServer` (line 925) instead of `server`.
  3. In `streamable-http.ts` `handlePostRequest` (lines 49-113), delete the `transports` map (lines 26-27, 57-70, 94-103) and the initialize-only branch (line 73). For every POST, do:
     `const server = createServer(); const transport = new StreamableHTTPServerTransport({ sessionIdGenerator: undefined }); await server.connect(transport); await transport.handleRequest(req, res, body);`
     and close both on `res.on('close', ...)`. With `sessionIdGenerator: undefined` there is no `mcp-session-id` header, so any Lambda instance can answer any request, and the "already connected" error also goes away.
- **`--header-passthrough`:** do NOT use it here. It exists to forward chosen inbound headers (e.g. `Authorization`) to the API. The MCP server must use its own machine token from
  `BEARER_TOKEN_BEARERAUTH`; the user's token is verified at the gateway in front and must never be forwarded. I did not generate with it; the default build forwards nothing
  inbound (the only auth source in `index.ts` is `process.env.BEARER_TOKEN_*`, lines ~758), which is what we want.
- Also note the generated server does no auth of its own, so mcp-host's gateway auth is the only protection.

## Honest summary for the video

The generator gets you 33 tools in one command, they build, and tool names are good. The description quality is exactly the spec's `summary` fields,
there are no annotations, the transport code is buggy (crash plus single-session), and the session map is stateful. Fixing it for Lambda is about 25 lines of code. The hand-designed Go server's
contrast is therefore: fewer, intent-shaped tools; real descriptions; read-only hints; stateless from day one.
