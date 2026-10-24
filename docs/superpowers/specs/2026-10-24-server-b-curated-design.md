# Server B: the curated audiofile MCP server

Status: design · 2026-10-24 · Parent spec: `2026-10-20-audiofile-api-design.md`

## Why

The video (Ep09) compares two MCP servers for the same API:

- **Server A** (`servers/generated`, already built): openapi-mcp-generator makes one tool per endpoint, 33 tools, with no read-only or destructive hints.
- **Server B** (this design): a small set of tools designed by hand in Go.

Marian's own test of Server A (2026-10-24) changed what Server B has to prove. Server A *did the job*: in Claude it planned a correct 30-minute warm-up → peak → cooldown arc in a single `createPlaylist` call. Where it fell short:

1. **Consistency.** Each run, the model decides what "upbeat" means. Second runs let ~100 BPM tracks in. In a strict "120+ and high energy" run it chose its own energy cutoff (0.7).
2. **Padding.** When few tracks qualify, the model fills out the playlist with near-misses instead of saying so.
3. **Vague targets.** "Remove anything shorter than 2 minutes from that playlist" edited the wrong playlist: the most recent one, not the one Marian meant.
4. **Exposure.** Server A includes `deleteTrack` (it could delete her music) and the upload tools (their links carry temporary AWS credentials).
5. **Invisible work.** Claude and ChatGPT don't show which tools ran.

The honest lesson for the video: curating means putting *your* judgment into the tools (your definition of upbeat, your rules for what's safe) instead of leaving it to the model each time.

## Goals

- Six tools, each with an accurate read-only or destructive hint.
- Marian's definition of **upbeat**, built in: **bpm ≥ 120 and energy ≥ 0.6**. On today's library that's exactly 5 tracks (Prettyjohn1 Track 1, Arpmedia Track 1, Studiokolomna Track 1, Q, Scary Intro), and she approved them by ear.
- **Honest when the library is short:** never pad with tracks that don't qualify; say how much music there is.
- Every edit and delete names its playlist explicitly.
- No way to delete or upload tracks.
- A tool-call viewer, so the video can show what each server actually called.
- The same hosting as the other servers: `mcphost/`, the OAuth bridge, and a machine token for the API.

## Non-goals

- Uploads, track deletion, track tag edits, artist and album browsing, the book's legacy routes.
- Changing the API. Server B uses only existing endpoints.
- Improving the tempo detector. Suspicious readings (Morning Fog at 123.7 BPM with energy 0.19) are caught by the energy rule, not fixed.

## Anti-goals (failure even if it works)

- Rigging the comparison: Server B must not get a better API or extra data that Server A can't reach. The difference is tool design only.
- Any token, email, account ID or presigned URL in logs, tool output or the viewer.
- Tool descriptions written for humans instead of the model (vague, or overlapping with each other).

## The tools

All tools return structured output plus a short text summary the model can quote. Tracks are shown compactly: `id, title, artist, bpm, energy, seconds`. Lists in output schemas are plain arrays, never `["null","array"]` (the Inspector's *schema portability* warning on `things-test`); a test checks this.

**Moods** are defined once, in one place in the code, and quoted in the tool descriptions:

| Mood | Rule |
|---|---|
| `upbeat` | bpm ≥ 120 **and** energy ≥ 0.6 |
| `calm` | energy < 0.5 *(my call)* |
| `any` | no rule |

Only `ready` tracks are used.

### 1. `find_tracks` (read-only)

Search the library.
- Inputs (all optional): `mood` (upbeat | calm | any, default any), `bpm_min`, `bpm_max`, `energy_min`, `energy_max`, `min_seconds`, `max_seconds`, `limit` (default 25, max 100).
- Explicit numbers narrow the mood further; they never loosen it.
- Output: matching tracks sorted by bpm, a count, total minutes.

### 2. `build_workout_mix` (write, not destructive)

The whole job in one call.
- Inputs: `name` (default "Upbeat Workout Mix"), `minutes` (optional target), `shape` (`steady` | `arc`, default `steady`).
- **steady:** upbeat tracks only, ordered by bpm ascending.
  - With `minutes`, it adds tracks in that order until the total reaches the target. It may go over by at most the last track added *(my call)*.
  - It never adds non-upbeat tracks. If the upbeat tracks run out first, it uses them all and the summary says so plainly ("Only 9.1 minutes of upbeat music in the library; the mix is 9.1 minutes, not 30").
- **arc:** warm-up → peak → cooldown. Every choice is deterministic, so the same library gives the same mix every run *(all my calls)*.
  - **Peak:** upbeat tracks, bpm ascending, so it builds.
  - **Warm-up and cooldown candidates:** every ready track that isn't upbeat, ranked by energy, highest first (ties broken by bpm, then id). Warm-up takes from the **top** of that ranking (energetic but slower); cooldown takes from the **bottom** (the calmest). So Morning Fog (energy 0.19) lands in cooldown, not warm-up.
  - **Order within a section:** warm-up by bpm ascending (rising towards the peak); cooldown by bpm descending.
  - **Without `minutes`:** the whole peak, plus 2 warm-up and 2 cooldown tracks.
  - **With `minutes`:** target 20% warm-up, 60% peak, 20% cooldown. The peak takes upbeat tracks (bpm ascending) until it reaches its share, going over by at most one track. If the upbeat tracks run out first, the time left over is split evenly between warm-up and cooldown. Each section may go over its share by at most one track.
  - Each track appears once. If the whole library is shorter than the target, it uses everything that fits the shape and says so.
  - The output labels each track's section.
- If a playlist with the same name exists, it appends " 2", " 3" and so on, so names stay unique *(my call)*.
- Creates the playlist, adds the tracks in order, creates a player link.
- Output: playlist id and name, tracks (with section for arc), total minutes, player link, and a `note` (shortfall, or empty).

### 3. `list_playlists` (read-only)

Name, id, track count, total minutes for each playlist.

### 4. `show_playlist` (read-only)

- Input: `playlist`, a name or id (required).
- Output: the tracks in order with bpm, energy and seconds; total minutes; a player link.
- Creating a player link writes a temporary page to S3 but changes nothing in the library, so the tool stays read-only *(my call)*.

### 5. `edit_playlist` (write, not destructive)

- Input: `playlist` (name or id, **required**). Then any of: `add_track_ids`, `remove_track_ids`, `remove_shorter_than_seconds`, `order` (`keep` | `bpm_asc` | `bpm_desc` | `arc`, default `keep`).
- It applies removals, then additions, then ordering.
- `order=arc` uses the arc rules on the playlist's own tracks: its upbeat tracks become the peak, and the rest are split by energy into warm-up and cooldown. With no upbeat tracks, it's just warm-up then cooldown *(my call)*.
- Output: what changed (removed and added titles), the new track list, total minutes, a player link.

### 6. `delete_playlist` (destructive)

- Input: `playlist` (name or id, required).
- Deletes that one playlist. Tracks are untouched.

### Resolving `playlist`

An exact id, or a case-insensitive exact name. No match → an error that lists the existing playlist names. More than one match → an error that lists them with ids. Never guess.

### Errors

Tool errors come back as tool results with `IsError` and a plain message the model can act on (for example "no playlist named 'Gym'; playlists: Upbeat Workout Mix, …"). API failures are summarised without URLs or tokens.

## How it's built

- **Code:** `internal/curated` (tools, the mood rules, mix building, an API client) and `servers/curated` (the Lambda `main.go`, `template.yaml`, `samconfig.toml`). The pattern is the same as `internal/mcptest` / `servers/mcptest`: go-sdk v1.8.0, stateless JSON streamable HTTP, `httpadapter` for the REST proxy event.
- **API access:** the same as Server A, with **its own machine token**: Cognito client_credentials for the machine client, the secret from SSM `/audiofile/machine-client-secret`, cached until shortly before expiry. The caller's token is never forwarded or logged. Env: `API_BASE_URL`, `MACHINE_CLIENT_ID`, `TOKEN_URL` (the same imports as `servers/generated/template.yaml`).
- **Hosting:** the nested `mcphost/template.yaml`, ServerName `audiofile-curated`, stack `mcp-curated`, deployed with `scripts/deploy-mcp.sh servers/curated`. Makefile targets matching the mcptest ones.
- **Logging:** one line per tool call, `tool "<name>"` plus the input with ids shortened. No tokens, no URLs, no request headers.
- **Tests:** unit tests against a fake API (`httptest`). Cover:
  - the mood rules, steady and arc ordering, minutes targets, and the shortfall note;
  - playlist resolution (miss, ambiguous) and the order of edits;
  - the annotations on each tool, and that no output schema has a multi-type array;
  - that the token never appears in logs.
  - Plus one test that today's 18-track library gives exactly the 5 upbeat tracks, and a fixed arc for it. The fixture is `internal/curated/testdata/library-2026-10-24.json`: a real snapshot from the deployed API (`GET /tracks`), trimmed to id, title, artist, status, bpm, bpm_source, energy and duration_seconds, with no URLs.

## Notes from the repo

- **Machine token:** "its own" means its own cached token for the **same** machine client and SSM secret as Server A. No new Cognito client.
- **Duplicate playlist names:** the API allows them, so the " 2" suffix is Server B's job. The API already drops duplicate track ids.
- **Durations:** `GET /playlists` has no durations, so `list_playlists` makes one extra call per playlist. `/search` returns only ready tracks, sorted by title, so Server B re-sorts by bpm.
- **Server A's log line** is `Executing tool "<name>": <METHOD> <url>` (`servers/generated/app/src/index.ts`). The viewer strips everything after the name.
- **Build guide location:** `~/Movies/Youtube Channel/MCP Generated vs Designed - Ep09/00_Production/`.

## Tool-call viewer: `scripts/tool-calls.sh`

- `scripts/tool-calls.sh generated|curated [--since 10m]` follows that server's Lambda log group (`aws logs tail --follow`).
- It prints only `HH:MM:SS  toolName` (Server A logs `Executing tool "<name>"`; Server B logs `tool "<name>"`) and a running count.
- It strips anything that looks like a URL, account id or token.
- It's safe to show on camera.

## Build guide (for filming I08)

Written after the server is built and tested, into the episode folder as `00_Production/Ep09_ServerB-build-guide.md`. I08 in the shot list points to it. It's a walkthrough, not live coding:
1. The mood table in the code.
2. Each tool's name, description and hints: why it exists, what was left out.
3. A local run in the Inspector.
4. The deploy.
5. The viewer running beside a chat.

## Deploying

The `mcp-curated` stack (a Lambda function, an API Gateway and a Cognito resource server) costs pennies a month. **Ask Marian before the first deploy.**

## Left to the builder

- The file layout inside `internal/curated`.
- The API client's shape.
- The text summary wording.
- How arc time is split when `minutes` is odd.
- The exact help text, as long as the descriptions quote the mood rules and say what each tool won't do.
