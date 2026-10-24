# Server B (curated MCP server) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A six-tool, hand-designed Go MCP server for the audiofile API, hosted behind `mcphost/` like `things-test`, plus a tool-call viewer script and a build guide for filming.

**Architecture:** `internal/curated` holds four parts: an HTTP client for the deployed API (machine token via Cognito client_credentials), pure mix-building logic (the moods, steady and arc), and the MCP tools (go-sdk, stateless JSON streamable HTTP). `servers/curated` is the Lambda entrypoint and the SAM stack, which nests `mcphost/template.yaml`. `scripts/tool-calls.sh` follows a server's CloudWatch log group and prints only tool names.

**Tech Stack:** Go 1.25 (go.mod), `github.com/modelcontextprotocol/go-sdk` v1.8.0, aws-lambda-go + aws-lambda-go-api-proxy `httpadapter`, aws-sdk-go-v2 `ssm`, SAM (provided.al2023, arm64), bash.

**Spec:** `docs/superpowers/specs/2026-10-24-server-b-curated-design.md`

## Global Constraints

- **Moods**, defined once in code: `upbeat` = bpm ≥ 120 **and** energy ≥ 0.6; `calm` = energy < 0.5; `any` = no rule. Only tracks with `status == "ready"` are used.
- Exactly 6 tools: `find_tracks` (read-only), `build_workout_mix` (write), `list_playlists` (read-only), `show_playlist` (read-only), `edit_playlist` (write), `delete_playlist` (destructive). No track deletion, no uploads, no legacy routes.
- Annotations:
  - read-only tools: `ReadOnlyHint: true`;
  - write tools: `ReadOnlyHint: false`, `DestructiveHint: ptr(false)`;
  - `delete_playlist`: `DestructiveHint: ptr(true)`;
  - all tools: `OpenWorldHint: ptr(false)`.
- No output-schema property may have a multi-type `type` (for example `["null","array"]`). Lists are always non-nil.
- Never log or return tokens, request headers, URLs other than the `/play/` player link, account ids, or presigned URLs.
- API access uses a cached machine token for the **existing** machine client: env `API_BASE_URL`, `MACHINE_CLIENT_ID`, `TOKEN_URL`; the secret comes from env `MACHINE_CLIENT_SECRET` or, if that's unset, SSM SecureString `/audiofile/machine-client-secret`. The caller's token is never forwarded.
- Log one line per tool call: `tool "<name>" <input JSON with ids shortened to 8 chars>`.
- Stack `mcp-curated`, ServerName `audiofile-curated`, deployed only with `scripts/deploy-mcp.sh servers/curated`. **No deploy in this plan:** stop and ask Marian.
- After each task, append one short entry under `## Build` in `~/Movies/Youtube Channel/MCP Generated vs Designed - Ep09/00_Production/Ep09_build-log.md`: what was built, the test count and pass/fail, and any number worth showing on screen. Never put secrets, emails or account ids there.
- Commit after every task; end commit messages with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

- A `playlist` reference that matches by name *and* by a different playlist's id → id wins. Test it in Task 3.
- `minutes` ≤ 0 or > 600 → a tool error that says the valid range (1–600), not a panic or an empty mix. Test it in Task 3.
- An API 5xx or timeout mid-build (after `POST /playlists`) → a tool error that names the playlist that was created, so the model can show or delete it. Never a silent half-built mix. Test it in Task 3.
- The library has more than 100 tracks → the client pages through `GET /tracks` with `next_cursor` until it's exhausted. Test it in Task 1.
- An expired or rejected machine token (401 from the API) → refresh once and retry, then fail with a plain message. Test it in Task 1.

---

### Task 1: API client with machine token

**Files:**
- Create: `internal/curated/client.go`, `internal/curated/token.go`
- Test: `internal/curated/client_test.go`

**Interfaces:**
- Produces:
  - `type Track struct { ID, Title, Artist, Status string; BPM, Energy, DurationSeconds float64 }` (json: `id,title,artist,status,bpm,energy,duration_seconds`)
  - `type Playlist struct { ID, Name, Description string; TrackIDs []string }` (json `track_ids`)
  - `type API interface {`
    - `Tracks(ctx) ([]Track, error)`: all tracks, every page
    - `Playlists(ctx) ([]Playlist, error)`
    - `Playlist(ctx, id string) (Playlist, error)`
    - `CreatePlaylist(ctx, name, description string, trackIDs []string) (Playlist, error)`
    - `AddTracks(ctx, id string, trackIDs []string) error`
    - `RemoveTrack(ctx, id, trackID string) error`
    - `Reorder(ctx, id string, trackIDs []string) error`
    - `DeletePlaylist(ctx, id string) error`
    - `PlayerLink(ctx, id string) (string, error)`
  - `}`
  - `func NewClient(baseURL string, tok TokenSource, hc *http.Client) *Client` (implements `API`)
  - `type TokenSource interface { Token(ctx) (string, error); Invalidate() }`
  - `func NewMachineToken(tokenURL, clientID string, secret func(ctx) (string, error), hc *http.Client) TokenSource`
  - `var ErrNotFound`: wraps API 404s

- [ ] **Step 1: Write failing tests** against an `httptest` fake API and token endpoint:
  - `TestTracksPagesThroughCursor`: the fake returns 100 + 18 tracks over two pages (`next_cursor`). `len == 118`; the second request carries `cursor=`.
  - `TestTokenCachedUntilNearExpiry`: `expires_in: 3600` → 3 calls make 1 token request. `expires_in: 30` → refreshed on every call (refresh window 60 s).
  - `TestRetriesOnceOn401`: the first API call returns 401 → `Invalidate`, a new token, a retry that succeeds. Two 401s in a row → the error message contains `"API rejected the server's credentials"`.
  - `TestSendsBearerAndNeverCallerToken`: the fake checks `Authorization: Bearer machine-tok`.
  - `TestErrorsHaveNoURL`: a 500 from the fake → the error string doesn't contain `http`.
  - `TestDeletePlaylist404IsErrNotFound`: `errors.Is(err, ErrNotFound)`.
  - `TestPlayerLink`: `POST /playlists/{id}/player` returns `{"url":"https://x/play/abc","expires_at":"…"}` → the URL is returned.
- [ ] **Step 2:** `go test ./internal/curated/ -run 'Test(Tracks|Token|Retries|Sends|Errors|Delete|Player)'` → FAIL (undefined).
- [ ] **Step 3: Implement.** Endpoints: `GET /tracks?limit=100[&cursor=]`; `GET /playlists`; `GET /playlists/{id}`; `POST /playlists {name,description,track_ids}`; `POST /playlists/{id}/tracks {track_ids}`; `DELETE /playlists/{id}/tracks/{trackId}`; `PUT /playlists/{id}/tracks/order {track_ids}`; `DELETE /playlists/{id}`; `POST /playlists/{id}/player`. The token is a form POST `grant_type=client_credentials` with HTTP Basic `clientID:secret`. Errors are summarised as `"<operation>: API returned <status>"` plus the API's `error.message` when present, never the URL. Add `func SSMSecret(name string) func(ctx) (string, error)` (aws-sdk-go-v2 `ssm`, `WithDecryption`, cached after the first read).
- [ ] **Step 4:** the same command → PASS; `go vet ./internal/curated/`.
- [ ] **Step 5:** add a build-log entry, then commit `feat(curated): API client with cached machine token`.

### Task 2: Moods and mix building (pure)

**Files:**
- Create: `internal/curated/mix.go`
- Test: `internal/curated/mix_test.go` (uses `testdata/library-2026-10-24.json`, already committed: `{items:[{id,title,artist,status,bpm,bpm_source,energy,duration_seconds}]}`)

**Interfaces:**
- Consumes: `Track` (Task 1).
- Produces:
  - `type Mood string` with consts `Upbeat = "upbeat"`, `Calm = "calm"`, `Any = "any"`
  - `func (m Mood) Match(t Track) bool`
  - `const UpbeatMinBPM = 120.0`, `UpbeatMinEnergy = 0.6`, `CalmMaxEnergy = 0.5`
  - `type Filter struct { Mood Mood; BPMMin, BPMMax, EnergyMin, EnergyMax, MinSeconds, MaxSeconds *float64 }`
  - `func FindTracks(lib []Track, f Filter, limit int) []Track`: ready only, sorted by bpm ascending, then id
  - `type Section string` with consts `Warmup`, `Peak`, `Cooldown`, `Steady` (`"warm-up"`, `"peak"`, `"cooldown"`, `"steady"`)
  - `type MixTrack struct { Track; Section Section }`
  - `type Mix struct { Tracks []MixTrack; Seconds float64; Note string }`
  - `func Steady(lib []Track, minutes float64) Mix` (`minutes` 0 = no target)
  - `func Arc(lib []Track, minutes float64) Mix`
  - `func Minutes(sec float64) float64`: rounded to 0.1

- [ ] **Step 1: Write failing tests:**
  - `TestUpbeatOnTodaysLibrary`: `FindTracks(fixture, Filter{Mood: Upbeat}, 100)` titles == `["Scary Intro","Q","Studiokolomna Track 1","Arpmedia Track 1","Prettyjohn1 Track 1"]` (bpm order; Q 123.7 sorts before Studiokolomna 123.8).
  - `TestMoodBoundaries`: bpm 120 / energy 0.6 is upbeat; 119.9 or 0.59 isn't; energy 0.49 is calm and 0.5 isn't; non-ready tracks never match.
  - `TestFilterNarrowsNeverLoosens`: `Mood: Upbeat, BPMMin: 100` still excludes a 110 BPM track.
  - `TestSteadyNoTarget`: all 5 upbeat tracks, all `Steady`, Note empty.
  - `TestSteadyShortfallNote`: `Steady(fixture, 30)` → the 5 tracks; `Note == "Only 9.3 minutes of upbeat music in the library; the mix is 9.3 minutes, not 30."`. Compute 9.3 from the fixture in the test (sum of the 5 durations / 60, rounded to 0.1) and assert the exact string built from it.
  - `TestSteadyStopsAtTarget`: with a 4-minute target, tracks are added in bpm order until the total ≥ 240 s; no more than one track past the target.
  - `TestArcTodaysLibraryNoTarget`: the titles in order == `["Audiocoffee Track 1","Guitar Chrome Etiquette","Scary Intro","Q","Studiokolomna Track 1","Arpmedia Track 1","Prettyjohn1 Track 1","Morning Fog","Leberch Track 1"]`, sections `[warm-up×2, peak×5, cooldown×2]`.
  - `TestArcDeterministic`: a shuffled input gives an identical result.
  - `TestArcWithTarget`: `Arc(fixture, 30)`:
    - the peak holds all 5 upbeat tracks (their total is under 60% of 30 min);
    - the leftover time is split between warm-up and cooldown;
    - no track appears twice;
    - warm-up bpm is non-decreasing and cooldown bpm non-increasing;
    - each section goes over its share by at most one track.
  - `TestArcLibraryTooShort`: `Arc(fixture, 120)` uses every ready track; the Note starts with `"The whole library is only"`.
  - `TestNoNilSlices`: `Steady(nil, 0).Tracks != nil`.
- [ ] **Step 2:** `go test ./internal/curated/ -run 'Test(Upbeat|Mood|Filter|Steady|Arc|NoNil)'` → FAIL.
- [ ] **Step 3: Implement.** Arc algorithm (from the spec):

```
peak  = upbeat tracks, bpm asc (ties: id)
rest  = ready non-upbeat tracks, sorted energy desc, then bpm desc, then id
no target:   warm = first 2 of rest; cool = last 2 of rest (not overlapping)
with target T sec: peakShare = 0.6T; take peak tracks in order until sum ≥ peakShare
                   (≤1 over); left = T - peakSum; warmShare = coolShare = left/2;
                   warm takes from the front of rest until ≥ warmShare (≤1 over),
                   cool takes from the back of rest until ≥ coolShare (≤1 over), never overlapping
order: warm by bpm asc, peak by bpm asc, cool by bpm desc (ties: id)
note: if every ready track was used and the total < T: "The whole library is only X minutes; the mix is X minutes, not T."
```

  Steady's note copy is exactly as in the test.
- [ ] **Step 4:** the same command → PASS.
- [ ] **Step 5:** add a build-log entry (include the arc's track list and total minutes), then commit `feat(curated): moods, steady and arc mixes`.

### Task 3: The six MCP tools

**Files:**
- Create: `internal/curated/server.go` (`NewServer`, `Handler`), `internal/curated/tools.go` (handlers), `internal/curated/resolve.go`
- Test: `internal/curated/server_test.go` (an in-memory fake `API` seeded from the fixture; connect like `internal/mcptest/server_test.go` via `httptest` + `StreamableClientTransport`)

**Interfaces:**
- Consumes: `API`, `Track`, `Playlist`, `ErrNotFound` (Task 1); `Mood`, `Filter`, `FindTracks`, `Steady`, `Arc`, `Minutes` (Task 2).
- Produces:
  - `func NewServer(api API, logger *log.Logger) *mcp.Server`
  - `func Handler(api API, logger *log.Logger) http.Handler`: `&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true}`
  - `func ResolvePlaylist(ps []Playlist, ref string) (Playlist, error)`

Tool inputs and outputs (`jsonschema` tags hold the per-field help):

| Tool | Input | Output |
|---|---|---|
| `find_tracks` | `mood`, `bpm_min`, `bpm_max`, `energy_min`, `energy_max`, `min_seconds`, `max_seconds`, `limit` (1–100, default 25) | `{tracks:[TrackOut], count, minutes}` |
| `build_workout_mix` | `name` (default "Upbeat Workout Mix"), `minutes` (1–600, optional), `shape` (`steady`\|`arc`, default steady) | `{playlist_id, name, tracks:[TrackOut+section], minutes, player_link, note}` |
| `list_playlists` | none | `{playlists:[{id,name,track_count,minutes}]}` |
| `show_playlist` | `playlist` (required) | `{playlist_id, name, tracks, minutes, player_link}` |
| `edit_playlist` | `playlist` (required), `add_track_ids`, `remove_track_ids`, `remove_shorter_than_seconds`, `order` (`keep`\|`bpm_asc`\|`bpm_desc`\|`arc`) | `{playlist_id, name, removed:[title], added:[title], tracks, minutes, player_link}` |
| `delete_playlist` | `playlist` (required) | `{deleted: name}` |

`TrackOut` = `{id,title,artist,bpm,energy,seconds}`.

Descriptions must quote the mood rules ("upbeat = 120+ BPM and energy 0.6+") and say what the tool won't do. For example `build_workout_mix`: "…It never pads with tracks that don't qualify; if there isn't enough music it says so in `note`." `delete_playlist`: "…Deletes one playlist. Tracks are never deleted."

- [ ] **Step 1: Write failing tests:**
  - `TestSixToolsWithHints`: `ListTools` names == the 6, sorted. Annotations per Global Constraints.
  - `TestNoMultiTypeSchemas`: walk every tool's `InputSchema` and `OutputSchema` JSON; no `type` is an array.
  - `TestBuildSteadyTodaysLibrary`: `build_workout_mix{}` → 5 tracks, `player_link` ends with the fake's `/play/…`, the fake API got one `CreatePlaylist` with those 5 ids in bpm order.
  - `TestBuildShortfallNote`: `{minutes:30}` → `note` == the Task 2 string; the text content contains it.
  - `TestBuildArc`: `{shape:"arc"}` → 9 tracks with their sections (as in Task 2).
  - `TestBuildUniqueName`: existing "Upbeat Workout Mix" → the new one is "Upbeat Workout Mix 2"; with " 2" also taken → " 3".
  - `TestBuildMinutesOutOfRange`: `minutes: 0` → error `"minutes must be between 1 and 600"`; `601` → the same.
  - `TestBuildPartialFailureNamesPlaylist`: the fake fails `PlayerLink` → an `IsError` result whose text contains the created playlist's name and `"was created"`.
  - `TestResolveByIDBeatsName`, `TestResolveCaseInsensitiveName`, `TestResolveMissListsNames` ("no playlist named 'Gym'; playlists: A, B"), `TestResolveAmbiguousListsIDs`.
  - `TestEditOrder`: `remove_shorter_than_seconds:120` + `add_track_ids` + `order:"bpm_asc"` → removals before additions before the reorder; `removed`/`added` titles right; an unknown track id in `add_track_ids` → error naming it.
  - `TestEditRequiresPlaylist`: a missing `playlist` → error.
  - `TestDeletePlaylist`: deletes the resolved id only; the fake's tracks are untouched.
  - `TestLogsToolLinesWithoutSecrets`: a buffer logger gets `tool "find_tracks"`; the log never contains `Bearer`, `http` or a full 36-char id.
- [ ] **Step 2:** `go test ./internal/curated/` → FAIL.
- [ ] **Step 3: Implement.** Tool errors use `&mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: msg}}}` (as in `internal/mcptest`). If the SDK's inferred output schema produces `["null","array"]` for slices, set an explicit `OutputSchema` per tool, built with `jsonschema.For[T]` plus a post-pass that rewrites the multi-type arrays, or give `Tool.OutputSchema` directly. Either way, `TestNoMultiTypeSchemas` decides.
- [ ] **Step 4:** `go test ./internal/curated/ && go vet ./...` → PASS.
- [ ] **Step 5:** add a build-log entry (the tool count against Server A's 33; the token count of the `tools/list` JSON for both, measured as bytes/4 from Server B's ListTools and Server A's DRYRUN-REPORT if it has the JSON, otherwise just Server B's), then commit `feat(curated): six curated MCP tools`.

### Task 4: Lambda entrypoint and SAM stack

**Files:**
- Create: `servers/curated/main.go`, `servers/curated/template.yaml`, `servers/curated/samconfig.toml`
- Modify: `Makefile` (`.PHONY`, `build-CuratedServerFunction`, `validate-curated`, `build-curated`, following the mcptest targets), `README.md` (the Server B section, its script row, the Docs list)

**Interfaces:**
- Consumes: `Handler`, `NewClient`, `NewMachineToken`, `SSMSecret`.

- [ ] **Step 1:** `main.go`, following `servers/mcptest/main.go`:
  - reads env `API_BASE_URL`, `MACHINE_CLIENT_ID`, `TOKEN_URL`, optional `MACHINE_CLIENT_SECRET`;
  - `log.New(os.Stdout, "", 0)`;
  - `http.Client{Timeout: 10*time.Second}`;
  - `mux.Handle("/mcp", curated.Handler(...))`;
  - when `PORT` is set (local run), `http.ListenAndServe(":"+PORT, mux)` instead of `lambda.Start`. This is the on-camera local run.
- [ ] **Step 2:** `template.yaml`:
  - copy `servers/mcptest/template.yaml`'s shape;
  - Function `CuratedServerFunction`: MemorySize 128, Timeout 15;
  - env imports and the `ssm:GetParameter` policy exactly as in `servers/generated/template.yaml`;
  - a 14-day log group;
  - `McpHost` with `ServerName` a parameter, default `audiofile-curated`, the same `AllowedPattern`;
  - Output `McpUrl`.
  `samconfig.toml`: as mcptest's, with `stack_name = "mcp-curated"`.
- [ ] **Step 3:** `make validate-curated` → "is a valid SAM Template"; `make build-curated` → "Build Succeeded". `go build ./...` passes.
- [ ] **Step 4:** a local smoke test without AWS: `PORT=3999 API_BASE_URL=http://127.0.0.1:1 MACHINE_CLIENT_ID=x TOKEN_URL=http://127.0.0.1:1 MACHINE_CLIENT_SECRET=x go run ./servers/curated &`. An MCP `initialize` + `tools/list` POST to `http://127.0.0.1:3999/mcp` lists 6 tools. Kill it.
- [ ] **Step 5:** add a build-log entry, then commit `feat(curated): Lambda entrypoint and mcp-curated stack`.

### Task 5: Tool-call viewer

**Files:**
- Create: `scripts/tool-calls.sh`, `scripts/test/tool_calls_test.sh`

- [ ] **Step 1: Write a failing test.** `scripts/test/tool_calls_test.sh` pipes canned log lines through the filter (`TOOL_CALLS_STDIN=1 scripts/tool-calls.sh generated`) and checks:
  - `Executing tool "listTracks": GET https://abc.execute-api.us-west-2.amazonaws.com/tracks` → `HH:MM:SS  listTracks   (1)`;
  - `tool "find_tracks" {"mood":"upbeat"}` → `find_tracks   (2)`;
  - a line with `Bearer eyJ…`, a 12-digit number or `https://` prints nothing;
  - the whole output contains no `http`, no 12-digit run, no `Bearer`.
- [ ] **Step 2:** run it → FAIL.
- [ ] **Step 3: Implement.** Usage: `scripts/tool-calls.sh generated|curated [--since 10m]`. The stack is `mcp-generated` or `mcp-curated`; it finds the function's log group from stack resources (the private-log pattern from `deploy-mcp.sh`: AWS errors go to a private temp log, never the terminal). It runs `aws logs tail <group> --follow --since <since> --format short` piped into the filter. The filter keeps only `tool "<name>"` / `Executing tool "<name>"`, prints the time, the name and a running count. `TOOL_CALLS_STDIN=1` reads stdin instead (for the test). The header line `watching <server> tool calls (Ctrl-C to stop)` is printed.
- [ ] **Step 4:** run the test → PASS; `shellcheck scripts/tool-calls.sh` if installed.
- [ ] **Step 5:** add a README script-table row and a build-log entry, then commit `feat(scripts): tool-call viewer for Server A and B`.

### Task 6: Build guide and shot-list update (episode folder, not git)

**Files (outside the repo):**
- Create: `~/Movies/Youtube Channel/MCP Generated vs Designed - Ep09/00_Production/Ep09_ServerB-build-guide.md`
- Modify: the same folder's `Ep09_screen-shot-list.md` (I08 points to the guide; replace "*(Commands from the build guide.)*" with the real commands)

- [ ] **Step 1:** Write the guide as filming steps, each with **Do / Say (gist) / Hold on**:
  1. The mood table in `internal/curated/mix.go` (the constants).
  2. The tool list in `internal/curated/server.go`: the six names and hints. What was left out and why: `deleteTrack`, uploads.
  3. `build_workout_mix`'s description: "never pads".
  4. `go test ./internal/curated/ -v`, holding on the PASS lines, the upbeat-library test and the arc test.
  5. A local run: `go run ./servers/curated` with `PORT=3999` and the real env, using the commands to fetch the env values with `out` (from `scripts/lib.sh`) without printing the secret. Then the Inspector at `http://127.0.0.1:3999/mcp`, no sign-in.
  6. The deploy: `scripts/deploy-mcp.sh servers/curated` (**needs Marian's OK first**).
  7. `scripts/tool-calls.sh curated` beside the chat.
  Include the pre-record checklist reminder: neutral prompt, and no `MACHINE_CLIENT_SECRET` typed on screen. The local run reads the secret from SSM with your AWS session.
- [ ] **Step 2:** Update I08 in the shot list to "Follow `Ep09_ServerB-build-guide.md`", and add `scripts/tool-calls.sh` to I06, I07, I10 and I11 as an optional second window.
- [ ] **Step 3:** Run the guide's local-run commands for real (no deploy) and confirm the Inspector lists 6 tools. If the Inspector can't be driven, do a `curl` `tools/list` instead.
- [ ] **Step 4:** add a build-log entry: "Ready to deploy, waiting for OK".
