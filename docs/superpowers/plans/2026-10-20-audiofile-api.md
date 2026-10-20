# audiofile-api Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build, deploy and secure the 33-route audiofile music API on AWS, plus the Cognito OAuth bridge, reusable MCP hosting, a throwaway Go MCP server, a generator dry run, a seeded library, helper scripts and the MCP lab docs.

**Architecture:** Three SAM stacks: `audiofile-api` (Cognito, HTTP API + JWT authorizer, one Go api Lambda, S3-triggered processor Lambda with an ffmpeg layer, S3, 4 DynamoDB tables, budget), `mcp-auth` (Go OAuth bridge on an HTTP API `$default` stage + TTL table) and `mcp-host` (nested template: REST API + Go TOKEN authorizer + protected-resource metadata, fronting any MCP server Lambda). Business logic sits behind small interfaces (`store.Store`, `blob.Blob`) with in-memory fakes, so almost everything is tested without AWS.

**Tech Stack:** Go (stdlib `net/http` ServeMux patterns), aws-lambda-go, aws-lambda-go-api-proxy (`httpadapter`), aws-sdk-go-v2, gonum `dsp/fourier`, `golang.org/x/text` (NFKD), `github.com/lestrrat-go/jwx/v3`, `github.com/modelcontextprotocol/go-sdk` v1.8.0, `gopkg.in/yaml.v3` (tests), AWS SAM, static arm64 ffmpeg/ffprobe, bash + curl + jq for scripts, `@redocly/cli`, `openapi-mcp-generator@4.1.0`.

**Spec:** `docs/superpowers/specs/2026-10-20-audiofile-api-design.md`. Read it first; this plan does not repeat its tables.

## Global Constraints

- Module `github.com/marianina8/audiofile-api`. Lambdas: `provided.al2023`, `arm64`, binary named `bootstrap`, built with `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 -tags lambda.norpc`.
- Never modify the book repo. Never use the AWS root user; every AWS command uses `--profile audiofile-admin` (`AWS_PROFILE=audiofile-admin` in scripts).
- No secrets, account IDs, emails, tokens, pool IDs or client IDs in the repo, logs or terminal output. Scripts read IDs from stack outputs at runtime; tokens go into env vars and are never echoed; Lambdas never log `Authorization` headers or tokens.
- Audio is never committed (`.gitignore` already excludes it). `~/audio` is read only by `scripts/seed.sh`, `scripts/draft-library.sh`, `cmd/genreguess` (local tool, never deployed), and the local-only beatdoctor check. Bedrock is used only by `cmd/genreguess` on Marian's Mac; no deployed Lambda calls Bedrock.
- JSON is snake_case except the legacy `Audio` shape, which keeps the book's exact field names. Errors: `{"error":{"code":"…","message":"…"}}`; codes `bad_request`, `not_found`, `payload_too_large`, `internal`.
- Enums: `TrackStatus` = `processing|ready|failed`; `BPMSource` = `tag|measured|manual`.
- BPM: valid TBPM is 40–250; detection range 60–200; 1 decimal. Energy = clamp((LUFS+24)/18, 0, 1), 2 decimals.
- Presigned URL lifetimes: upload PUT 15 min; stream GET 15 min; player page 1 h; tracks inside a player 75 min. S3 lifecycle expires `players/` after 1 day.
- Upload limits: mp3 only (`audio/mpeg`), ≤ 50 MB via presigned; legacy `POST /upload` returns 413 above 4,500,000 bytes of file data.
- Pagination: `limit` default 50, max 100.
- Route scopes (HTTP API authorization scopes are OR-lists): GET → `["audiofile/read","aws.cognito.signin.user.admin"]`; other methods → `["audiofile/write","aws.cognito.signin.user.admin"]`; `/health` → no authorizer. The `aws.cognito.signin.user.admin` entry lets Marian's own access tokens from `USER_PASSWORD_AUTH` through (Cognito omits custom scopes in that flow); ID tokens have no `scope` claim and are still rejected; the authorizer audience (CLI client + machine client) rejects bridge-issued tokens.
- Throttling: api stage 20 rps / burst 40; bridge 10 / 20; mcp-host 20 / 40.
- Commit after every task with a message ending in `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- Tests needing ffmpeg/ffprobe call `testutil.RequireFFmpeg(t)`, which skips with `"ffmpeg/ffprobe not on PATH; install ffmpeg to run audio tests"`.
- **STOP points** (ask Marian, wait): before T16, T17's deploy, T18's first Bedrock call, T18's upload, T19's browser sign-in, T25's deploy, T26 hand-off, each T28 lab step, and before any deploy that adds a paid resource.

## Review Focus

- **Tag-less files.** All 18 of Marian's files have no ID3 tags at all. A track must still end up `ready` with a filename-derived title, `Unknown Artist`, no album, and `bpm_source: measured`. Covered in T14 (`TestProcess_NoTags`). T18 adds automatic `library.csv` tagging (filename + optional Voxtral genre guess) so real metadata exists without manual work.
- **Tracks that aren't ready.** A `processing` or `failed` track sent to `POST /playlists/{id}/tracks` gets a 400 naming its id. `/search` never returns it. T7 has `TestSearch_ExcludesNotReady`; T8 has `TestAddPlaylistTracks_RejectsNotReady`.
- **Audio with no beat.** Silence, a lone sustained tone, or a clip under 10 s gives the track `failed` status and an `error` that says no tempo could be detected. It must not get a garbage BPM. T13 has `TestDetectBPM_SilenceErrors`; T14 has `TestProcess_TempoFailureMarksFailed`.
- **Reprocessing.** An S3 retry or a second upload to the same key must not overwrite a `manual` BPM or user `tags`. T14 has `TestProcess_PreservesManualBPMAndTags`.
- **Hostile or Unicode text.** A title like `<script>alert(1)</script>` must be escaped on the player page (T9 `TestPlayer_EscapesTitles`). `"Beyoncé & Jay-Z"` must slug to `beyonce-jay-z` (T1 `TestSlug`).

---

## Phase A — Local build (no AWS)

### Task 1: Scaffold, models, slugs

**Files:**
- Create: `go.mod`, `Makefile`, `README.md` (placeholder line only), `internal/model/track.go`, `internal/model/catalog.go`, `internal/model/playlist.go`, `internal/model/legacy.go`, `internal/slug/slug.go`, `internal/testutil/ffmpeg.go`
- Test: `internal/model/model_test.go`, `internal/slug/slug_test.go`

**Interfaces:**
- Produces:
  - `model.TrackStatus` (consts `StatusProcessing`, `StatusReady`, `StatusFailed`) and `func (TrackStatus) Valid() bool`.
  - `model.BPMSource` (`BPMTag`, `BPMMeasured`, `BPMManual`) and `Valid()`.
  - `model.Track` holds every spec §5 field plus `ContentSHA256`. Struct tags are `json:"snake_name"` and `dynamodbav:"snake_name"`. `Tags` is `json:"tags"` with `dynamodbav:"tags,stringset,omitempty"`. `Error` and `AlbumID` are `omitempty` in both.
  - `func (t Track) JSONTags() []string` returns `[]string{}` when nil.
  - `model.Artist{ID, Name, CreatedAt}`.
  - `model.Album{ID, Title, ArtistID, Artist, Year, CreatedAt}`.
  - `model.Playlist{ID, Name, Description, TrackIDs []string, CreatedAt, UpdatedAt}` with `track_ids` as a DynamoDB list.
  - `model.Audio`, `model.Metadata`, `model.Tags` copy the book's types exactly, same JSON names. `func (a Audio) JSON() (string, error)` indents with 4 spaces, as the book does.
  - `func model.AudioFromTrack(t Track) Audio`. Status maps `processing` → `"Initiating"` and `ready`/`failed` → `"Complete"`. `Error` is `[]string{t.Error}` when failed, otherwise `[]string{}`. `Path` = `S3Key`. `Transcript` = `""`.
  - `slug.Make(s string) string`: NFKD, drop non-ASCII, lowercase, collapse runs of non-`[a-z0-9]` to `-`, trim `-`. An empty result becomes `unknown`.
  - `slug.AlbumID(artistID, albumTitle string) string` = `artistID + "--" + Make(albumTitle)`.
  - `testutil.RequireFFmpeg(t *testing.T)`.

- [ ] **Step 1:** `go mod init github.com/marianina8/audiofile-api`.
  - Makefile targets: `test` (`go test ./...`), `lint-spec` (`npx --yes @redocly/cli@latest lint openapi.yaml`), `build` (`sam build` for each stack), `layer` (filled in by T15).
- [ ] **Step 2:** Write the failing tests:
  - `TestSlug`: table with `"Beat Doctor"→"beat-doctor"`, `"Beyoncé & Jay-Z"→"beyonce-jay-z"`, `"  --  "→"unknown"`, and `AlbumID("beat-doctor","Best Bytes Volume 4")=="beat-doctor--best-bytes-volume-4"`.
  - `TestTrackStatusValid`: the three values are valid; `"done"` is not.
  - `TestAudioFromTrack`:
    - A processing track gives `Status "Initiating"` and an empty, non-nil `Error`.
    - A failed track with `"boom"` gives `"Complete"` and `["boom"]`.
    - Marshalled JSON contains `"transcript": ""` and the keys `Id`, `Path`, `Metadata`, `Status`, `Error`.
  - `TestJSONTags`: a track with nil `Tags` returns a non-nil empty slice from `JSONTags()`. T6's `TestGetTrack_TagsNeverNull` checks the HTTP JSON.
- [ ] **Step 3:** Run `go test ./internal/...`. Expected: FAIL (undefined).
- [ ] **Step 4:** Implement.
- [ ] **Step 5:** Run `go test ./internal/...`. Expected: PASS.
- [ ] **Step 6:** Commit `feat: scaffold module, models and slugs`.

### Task 2: Store interface and in-memory store

**Files:**
- Create: `internal/store/store.go`, `internal/store/memory.go`, `internal/store/contract.go` (exported contract suite)
- Test: `internal/store/memory_test.go`

**Interfaces:**
- Produces: `store.ErrNotFound`, and `store.Store`:
```go
type Store interface {
	PutTrack(ctx context.Context, t model.Track) error
	GetTrack(ctx context.Context, id string) (model.Track, error)
	DeleteTrack(ctx context.Context, id string) error
	ListTracks(ctx context.Context, limit int, cursor string) ([]model.Track, string, error) // next=="" at end
	AllTracks(ctx context.Context) ([]model.Track, error)
	TracksByArtist(ctx context.Context, artistID string) ([]model.Track, error) // by title
	TracksByAlbum(ctx context.Context, albumID string) ([]model.Track, error)   // by track_number
	EnsureArtist(ctx context.Context, a model.Artist) error // create if absent; never overwrites
	GetArtist(ctx context.Context, id string) (model.Artist, error)
	ListArtists(ctx context.Context) ([]model.Artist, error)
	EnsureAlbum(ctx context.Context, a model.Album) error
	GetAlbum(ctx context.Context, id string) (model.Album, error)
	ListAlbums(ctx context.Context) ([]model.Album, error)
	AlbumsByArtist(ctx context.Context, artistID string) ([]model.Album, error) // by year
	PutPlaylist(ctx context.Context, p model.Playlist) error
	GetPlaylist(ctx context.Context, id string) (model.Playlist, error)
	ListPlaylists(ctx context.Context) ([]model.Playlist, error)
	DeletePlaylist(ctx context.Context, id string) error
}
```
  - `store.NewMemory() *Memory`.
  - `store.RunContract(t *testing.T, newStore func(t *testing.T) Store)`: every behaviour below runs against any implementation.

- [ ] **Step 1:** Write `RunContract` sub-tests, and `TestMemory` that calls it:
  - Get on a missing id returns `errors.Is(err, ErrNotFound)`. This holds for each entity, and for Delete on a missing id.
  - Paging 5 tracks with `ListTracks(limit=2)` gives 2, 2, 1 and then `next==""`, with no duplicates.
  - `EnsureArtist` twice with different names keeps the first name.
  - `TracksByAlbum` orders by `track_number`. A track with no `album_id` doesn't appear.
  - `PutPlaylist` round-trips `TrackIDs` in order (`["c","a","b"]`).
  - Tags round-trip; nil stays empty.
- [ ] **Step 2:** Run `go test ./internal/store/`. Expected: FAIL.
- [ ] **Step 3:** Implement `Memory` with a mutex and maps. The cursor is an opaque base64 of the last id in id order.
- [ ] **Step 4:** Run. Expected: PASS.
- [ ] **Step 5:** Commit `feat: store interface, memory store and contract tests`.

### Task 3: DynamoDB store

**Files:**
- Create: `internal/store/dynamo.go`, `internal/store/dynamo_test.go`
- Modify: `Makefile`. Add `test-dynamo`: run `amazon/dynamodb-local` on port 8000 in Docker, run the test with `DYNAMODB_ENDPOINT=http://localhost:8000`, then stop the container.

**Interfaces:**
- Consumes: `store.Store`, `RunContract` (T2).
- Produces:
  - `store.Tables{Tracks, Artists, Albums, Playlists string}`.
  - `store.NewDynamo(client *dynamodb.Client, t Tables) *Dynamo`.
  - `store.CreateTablesForTest(ctx, client, t Tables) error`, used only by tests. It creates the same keys and GSIs as `template.yaml`.
  - Index names `by-artist` and `by-album` on tracks, and `by-artist` on albums.

- [ ] **Step 1:** Write `TestDynamo`. It skips unless `DYNAMODB_ENDPOINT` is set, creates uniquely-named tables, and calls `RunContract`.
- [ ] **Step 2:** Run `make test-dynamo`. Expected: FAIL.
- [ ] **Step 3:** Implement:
  - Use `attributevalue` marshalling.
  - `Ensure*` uses `PutItem` with `attribute_not_exists(id)` and treats `ConditionalCheckFailedException` as success.
  - `ListTracks` uses `Scan` with `Limit` and `ExclusiveStartKey` (base64 JSON).
  - `AllTracks`, `ListArtists`, `ListAlbums` and `ListPlaylists` scan every page.
  - The `By*` methods use `Query` on the GSI.
- [ ] **Step 4:** Run `make test-dynamo` and `go test ./...`. Expected: PASS. The plain `go test` run skips Dynamo.
- [ ] **Step 5:** Commit `feat: DynamoDB store`.

### Task 4: Blob storage

**Files:**
- Create: `internal/blob/blob.go`, `internal/blob/fake.go`, `internal/blob/s3.go`
- Test: `internal/blob/s3_test.go`

**Interfaces:**
- Produces:
```go
type Blob interface {
	PresignPut(ctx context.Context, key, contentType string, size int64, ttl time.Duration) (url string, headers map[string]string, err error)
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
	Put(ctx context.Context, key, contentType string, body []byte) error
	Delete(ctx context.Context, key string) error
	Download(ctx context.Context, key, dstPath string) error
}
```
  - `blob.NewS3(client *s3.Client, bucket string) *S3`.
  - `blob.NewFake() *Fake`. Its fields `Objects map[string][]byte` and `ContentTypes map[string]string` are inspectable. Its presigned URLs look like `https://fake/{key}?ttl={seconds}`.

- [ ] **Step 1:** Write `TestS3_PresignPutSignsTypeAndLength`. Build an `S3` with static fake credentials and region `us-west-2`, then presign. The URL must contain `X-Amz-SignedHeaders` including `content-length` and `content-type`, and `X-Amz-Expires=900`. The returned headers map must equal `{"Content-Type":"audio/mpeg","Content-Length":"1234"}`.
- [ ] **Step 2:** Run. Expected: FAIL.
- [ ] **Step 3:** Implement with `s3.NewPresignClient`. `Download` streams to the file.
- [ ] **Step 4:** Run. Expected: PASS.
- [ ] **Step 5:** Commit `feat: blob storage (S3 + fake)`.

### Task 5: API skeleton, route table, three-way spec test

**Files:**
- Create: `internal/api/server.go`, `internal/api/routes.go`, `internal/api/respond.go`, `internal/api/health.go`, `openapi.yaml` (info, servers, `bearerAuth` security scheme, `/health`), `template.yaml` (audiofile-api stack with `ApiFunction` and only the `/health` event for now; T15 completes the stack)
- Test: `internal/api/spec_test.go`, `internal/api/health_test.go`

**Interfaces:**
- Consumes: `store.Store`, `blob.Blob`.
- Produces:
  - `api.Deps{Store store.Store; Blob blob.Blob; Now func() time.Time; NewID func() string}`
  - `api.Route{Method, Path, Scope string}`. `Path` is in OpenAPI form, e.g. `/tracks/{id}`. `Scope` is `audiofile/read`, `audiofile/write`, or `""` for `/health`.
  - `func api.Routes() []Route` is the single source of truth.
  - `func api.New(d Deps) http.Handler` registers each route as a ServeMux pattern `"METHOD /path"` with the same `{param}` names.
  - Helpers: `writeJSON(w, status, v)` and `writeError(w, status, code, msg)`.
  - Security scheme name `bearerAuth` (http bearer, JWT). The generator later reads `BEARER_TOKEN_BEARERAUTH`.

- [ ] **Step 1:** Write the tests:
  - `TestHealth`: `GET /health` returns 200 with `{"status":"ok"}`.
  - `TestSpecCoverage`:
    - Parse `../../openapi.yaml`. Collect (method, path) from `paths`.
    - Parse `../../template.yaml` with `yaml.v3` Nodes, which tolerates `!Ref`/`!Sub`. Collect `ApiFunction.Properties.Events.*` where `Type: HttpApi`, as (Method, Path, AuthorizationScopes or `Authorizer: NONE`).
    - Assert that the three sets (`Routes()`, the spec, the template) are equal in every direction. Failure messages name the missing route and which source lacks it.
    - Assert template scopes equal `[Scope, "aws.cognito.signin.user.admin"]` for each non-health route.
    - Assert every spec operation has an `operationId` and a `summary`, and that every non-health operation has `security: [{bearerAuth: []}]`.
  - `TestRoutesUnique`: no duplicate (method, path).
- [ ] **Step 2:** Run `go test ./internal/api/`. Expected: FAIL.
- [ ] **Step 3:** Implement. Unknown routes get the JSON 404 via a catch-all.
- [ ] **Step 4:** Run. Expected: PASS.
- [ ] **Step 5:** Commit `feat: API skeleton, route table, spec coverage test`.

> **Rule for T6–T10:** every route a task adds goes into `Routes()`, gets an `openapi.yaml` operation, and gets a `template.yaml` HttpApi event. `TestSpecCoverage` must stay green at the end of each task.
>
> **openapi.yaml style:** an `operationId` in lowerCamel verb+noun (`getPlaylist`, `listTracks`, `addPlaylistTracks`), a short `summary` ("Get a playlist"), and no `description` unless one is truly needed. Parameters and request/response schemas reference shared `components/schemas`: `Track`, `Artist`, `Album`, `Playlist`, `Error`, `Audio`. Do not write prose aimed at AI.

### Task 6: Track endpoints

**Files:**
- Create: `internal/api/tracks.go`
- Test: `internal/api/tracks_test.go`
- Modify: `routes.go`, `openapi.yaml`, `template.yaml`

**Interfaces:**
- Consumes: `Deps`, `writeJSON`/`writeError`, `slug`.
- Produces:
  - Routes `createTrackUpload`, `listTracks`, `getTrack`, `updateTrack`, `deleteTrack`, `getTrackStream`, `getTrackStatus`, behaving as spec §7.
  - `func (s *server) trackView(t model.Track) map[string]any`, or a view struct, that serializes `tags` as `[]`.
  - `func (s *server) ensureCatalog(ctx, artist, album string, year int) (artistID, albumID string, err error)`. The processor (T14) reuses this logic through the exported `catalog.Ensure` in `internal/catalog/catalog.go`. Create that package here with `func Ensure(ctx, st store.Store, now time.Time, artist, album string, year int) (artistID, albumID string, err error)`. An empty album gives `albumID==""`.

- [ ] **Step 1:** Write the failing tests, using `store.NewMemory()`, `blob.NewFake()`, a fixed `Now`, and sequential `NewID`:
  - `TestCreateUpload`:
    - The 201 body has `track_id`, `upload_url`, `upload_method:"PUT"`, `upload_headers`, `expires_at`.
    - The stored track has `status:"processing"`, `s3_key:"tracks/{id}.mp3"`, and the `original_filename`, `size_bytes` and `content_sha256` from the request.
  - `TestCreateUpload_Validation`: each of these returns 400: a filename not ending in `.mp3` (case-insensitive), `size_bytes` ≤ 0, `size_bytes` > 52428800, and `content_type` other than `audio/mpeg`.
  - `TestListTracks_Paging`:
    - `limit=2` returns `next_cursor`, and following it ends with `next_cursor` absent or empty.
    - `limit=0` and `limit=101` both return 400.
  - `TestPatchTrack`:
    - `bpm:128` sets `bpm_source:"manual"`.
    - `artist:"New Artist"` sets `artist_id:"new-artist"` and creates the artist.
    - An unknown field returns 400. An unknown id returns 404.
  - `TestDeleteTrack_RemovesFromPlaylists`: seed a playlist `[a,b,c]` and delete `b`. Expect 204, the playlist becomes `[a,c]`, and the blob key is gone.
  - `TestStream`: the response has a `url` containing `ttl=900` and an `expires_at`.
  - `TestStatus`: a failed track returns `{"id","status":"failed","error":"…"}`.
  - `TestGetTrack_TagsNeverNull`: a track stored with nil tags returns `"tags":[]`.
- [ ] **Step 2:** Run `go test ./internal/api/ -run 'Track|Upload|Stream|Status'`. Expected: FAIL.
- [ ] **Step 3:** Implement. Decode JSON with `DisallowUnknownFields` for PATCH.
- [ ] **Step 4:** Run `go test ./...`. Expected: PASS, including `TestSpecCoverage`.
- [ ] **Step 5:** Commit `feat: track endpoints`.

### Task 7: Search, artists, albums, genres, tags

**Files:**
- Create: `internal/api/search.go`, `internal/api/catalog.go`, `internal/api/tags.go`
- Test: `internal/api/search_test.go`, `internal/api/catalog_test.go`, `internal/api/tags_test.go`
- Modify: `routes.go`, `openapi.yaml`, `template.yaml`

**Interfaces:**
- Consumes: T6's track view and `catalog.Ensure`.
- Produces:
  - Routes `searchTracks`, `listArtists`, `getArtist`, `listArtistTracks`, `listArtistAlbums`, `listAlbums`, `getAlbum`, `listAlbumTracks`, `listGenres`, `listTags`, `addTrackTags`, `removeTrackTag`.
  - `func normalizeTag(s string) (string, error)`: trim, lowercase, spaces → `-`. The result must match `^[a-z0-9-]{1,32}$`, otherwise it's an error.

- [ ] **Step 1:** Write the failing tests. The fixture is 6 tracks with varied bpm, energy, genre, year and artist; one of them is `processing`.
  - `TestSearch_Filters`:
    - `bpm_min=120&energy_min=0.6` returns exactly the expected ids.
    - `genre=ROCK` matches `Rock`.
    - `q=doc` matches the artist "Beat Doctor".
    - `artist=beat-doctor` and `artist=Beat Doctor` give the same result.
  - `TestSearch_ExcludesNotReady`.
  - `TestSearch_BadNumber`: `bpm_min=abc` returns 400.
  - `TestSearch_Limit`: `limit=2` returns 2 items; `limit=101` returns 400.
  - `TestArtistEndpoints`: `track_count` and `album_count` are correct, and an unknown id returns 404.
  - `TestAlbumTracksOrdered`.
  - `TestGenres`: returns `{items:[{name,track_count}]}` sorted by name. Empty genre is excluded.
  - `TestTags`:
    - POSTing `{"tags":["Workout"," favorite "]}` stores `["favorite","workout"]`.
    - POSTing `"bad tag!"` returns 400.
    - DELETE returns 204, and `/tags` counts update.
- [ ] **Step 2:** Run. Expected: FAIL.
- [ ] **Step 3:** Implement. Search and aggregates call `AllTracks` and filter in memory. Sort search results by title.
- [ ] **Step 4:** Run `go test ./...`. Expected: PASS.
- [ ] **Step 5:** Commit `feat: search, catalog and tag endpoints`.

### Task 8: Playlist endpoints

**Files:**
- Create: `internal/api/playlists.go`
- Test: `internal/api/playlists_test.go`
- Modify: `routes.go`, `openapi.yaml`, `template.yaml`

**Interfaces:**
- Produces: routes `createPlaylist`, `listPlaylists`, `getPlaylist`, `updatePlaylist`, `deletePlaylist`, `listPlaylistTracks`, `addPlaylistTracks`, `removePlaylistTrack`, `reorderPlaylistTracks`.
  - Playlist summary: `{id, name, description, track_count, created_at, updated_at}`.
  - Playlist detail: the summary plus `track_ids`.

- [ ] **Step 1:** Write the failing tests:
  - `TestCreatePlaylist`: a missing or blank `name` returns 400. With `track_ids` the response is 201. The ids are validated like add.
  - `TestAddPlaylistTracks`:
    - Starting from `[a,b]`, adding `{track_ids:[c,a]}` gives `[a,b,c]`. A duplicate is a no-op.
    - Adding `{track_ids:[d], position:0}` gives `[d,a,b,c]`.
    - `position` beyond the length appends.
  - `TestAddPlaylistTracks_RejectsNotReady`: returns 400 with a message that contains each bad id. Unknown ids are treated the same way.
  - `TestReorder`:
    - A valid permutation gives 200 and the new order.
    - A missing id, an extra id or a duplicate id gives 400.
  - `TestRemovePlaylistTrack`: 204, and an id not in the playlist gives 404.
  - `TestListPlaylistTracks_Order`: returns full track views in play order.
  - `TestPatchDeletePlaylist`.
- [ ] **Step 2:** Run. Expected: FAIL.
- [ ] **Step 3:** Implement. Every mutation sets `updated_at`.
- [ ] **Step 4:** Run `go test ./...`. Expected: PASS.
- [ ] **Step 5:** Commit `feat: playlist endpoints`.

### Task 9: Playlist player

**Files:**
- Create: `internal/api/player.go`, `internal/api/player.html.tmpl` (embedded with `//go:embed`)
- Test: `internal/api/player_test.go`
- Modify: `routes.go`, `openapi.yaml` (`createPlaylistPlayer`), `template.yaml`

**Interfaces:**
- Produces:
  - `POST /playlists/{id}/player` returns `201 {url, expires_at}`.
  - The page is written to key `players/{playlist_id}/{NewID()}.html` with content type `text/html; charset=utf-8`.

- [ ] **Step 1:** Write the failing tests:
  - `TestPlayer`:
    - A 2-track playlist gives 201.
    - The fake blob has one `players/` object whose HTML contains both titles in order and two track URLs with `ttl=4500`.
    - The returned `url` has `ttl=3600`.
  - `TestPlayer_EmptyPlaylist`: returns 400.
  - `TestPlayer_EscapesTitles`: a title `<script>alert(1)</script>` appears as `&lt;script&gt;`, and the page has no `<script src=`.
- [ ] **Step 2:** Run. Expected: FAIL.
- [ ] **Step 3:** Implement with `html/template`.
  - The page has a list of title, artist and bpm, plus one `<audio controls>`.
  - Previous and Next buttons, and auto-advance on `ended`, come from a small inline script that reads a JSON array placed through `html/template`'s JS context.
  - No external assets.
- [ ] **Step 4:** Run `go test ./...`. Expected: PASS.
- [ ] **Step 5:** Commit `feat: playlist player link`.

### Task 10: Legacy book routes

**Files:**
- Create: `internal/api/legacy.go`
- Test: `internal/api/legacy_test.go`
- Modify: `routes.go`, `openapi.yaml` (`uploadAudio`, `getAudio`, `listAudio`; the `Audio` schema mirrors the book), `template.yaml`

**Interfaces:**
- Consumes: `model.AudioFromTrack`, `Audio.JSON()`.

- [ ] **Step 1:** Write the failing tests:
  - `TestLegacyUpload`:
    - A multipart `file` field with a 1 KB body and filename `x.mp3` gives 200 with `text/plain`, and the body is the new id.
    - The blob holds `tracks/{id}.mp3`, and the track is `processing` with `original_filename:"x.mp3"`.
  - `TestLegacyUpload_TooLarge`: 4,500,001 bytes gives 413 with JSON error code `payload_too_large`, and the message mentions `POST /tracks/uploads`.
  - `TestLegacyUpload_MissingFile`: returns 400.
  - `TestLegacyRequest`: the body unmarshals into `model.Audio` with the expected fields. A missing `id` gives 400; an unknown id gives 404.
  - `TestLegacyList`: returns a JSON array of every track, including non-ready ones, in the Audio shape.
- [ ] **Step 2:** Run. Expected: FAIL.
- [ ] **Step 3:** Implement. Pretty-print JSON as the book did.
- [ ] **Step 4:** Run `go test ./...`. Expected: PASS, and `Routes()` now has 33 entries. Add `TestRouteCount` asserting 33.
- [ ] **Step 5:** Commit `feat: legacy book routes`.

### Task 11: OpenAPI completeness and lint

**Files:**
- Modify: `openapi.yaml`
- Create: `redocly.yaml` (`extends: [recommended]`; disable only `info-license-url` if needed, and say why in a comment)
- Test: extend `internal/api/spec_test.go`

- [ ] **Step 1:** Write a failing test, `TestSpecResponses`:
  - Every operation declares its success code, as used in the handlers (200, 201 or 204), and `401` for protected operations.
  - Every `{param}` in a path is declared as a path parameter.
  - The `Track` schema's `status` enum equals `processing,ready,failed`, and `bpm_source` equals `tag,measured,manual`.
- [ ] **Step 2:** Run. Expected: FAIL wherever earlier tasks left gaps.
- [ ] **Step 3:** Complete the spec: `components/responses` for `Unauthorized`, `NotFound` and `BadRequest`, plus a `license` and `servers` placeholder `https://api.example.com`.
- [ ] **Step 4:** Run `go test ./internal/api/` and `make lint-spec`. Expected: PASS, and redocly prints `Your API description is valid.`
- [ ] **Step 5:** Commit `docs: complete OpenAPI spec, lint clean`.

### Task 12: Audio tools (ffprobe, loudness, decode)

**Files:**
- Create: `internal/audio/probe.go`, `internal/audio/loudness.go`, `internal/audio/decode.go`, `internal/audio/testsignal_test.go` (helpers that generate MP3 fixtures)
- Test: `internal/audio/probe_test.go`, `internal/audio/loudness_test.go`, `internal/audio/decode_test.go`

**Interfaces:**
- Produces:
  - `audio.Tools{FFmpeg, FFprobe string}`. `audio.ToolsFromEnv()` uses `FFMPEG_PATH` and `FFPROBE_PATH`, defaulting to `ffmpeg`/`ffprobe`. The layer sets `/opt/bin/...`.
  - `type Probe struct{Title, Artist, Album, AlbumArtist, Composer, Genre, Lyrics, Comment, Copyright, TBPM string; Year, TrackNumber int; DurationSeconds float64}`.
  - `func (t Tools) Probe(ctx, path string) (Probe, error)` reads `-show_format -show_streams -of json` tags, merging format and stream tags with case-insensitive keys:
    - `Year` is the first 4 digits of `date`, `year` or `TYER`.
    - `TrackNumber` is parsed from `track`, e.g. `"9/12"`.
    - `Lyrics` is the first key with the prefix `lyrics`.
    - `TBPM` comes from `TBPM`, then `bpm`.
  - `func ParseTBPM(s string) (float64, bool)` accepts 40–250.
  - `func (t Tools) IntegratedLUFS(ctx, path string) (float64, error)` runs `-nostats -i path -af ebur128=framelog=quiet -f null -` and parses the last `I: <x> LUFS` from stderr.
  - `func EnergyFromLUFS(l float64) float64`.
  - `func (t Tools) DecodeMono(ctx, path string, startSec, durSec float64, rate int) ([]float32, error)` uses `-ss start -t dur -i path -ac 1 -ar rate -f f32le pipe:1`.

- [ ] **Step 1:** Write the failing tests. Each calls `RequireFFmpeg`. The helper `writeTestMP3(t, args ...string) string` runs ffmpeg with `lavfi` sources into `t.TempDir()`.
  - `TestProbe_Tags`: write tags with `-metadata title=T -metadata artist=A -metadata genre=Rock -metadata TBPM=124 -metadata date=2017 -metadata track=9/12`. Expect `Probe` returns them, `TrackNumber==9`, `Year==2017`, and a duration within 0.1 s.
  - `TestProbe_NoTags`: returns empty strings without error.
  - `TestParseTBPM`: `"124"` → 124 true. `"124.5"` → 124.5 true. `"300"`, `""` and `"abc"` → false.
  - `TestIntegratedLUFS`: a 1 kHz sine at amplitude 0.5 for 10 s measures within ±1.5 LU of −9.0 LUFS (ffmpeg's ebur128 result for that signal; recompute once and pin it). The same tone at amplitude 0.05 measures about 20 LU lower.
  - `TestEnergyFromLUFS`: −24 → 0, −6 → 1, −15 → 0.5, −40 → 0, 0 → 1.
  - `TestDecodeMono`: 4 s of audio decoded from `start=1, dur=2` at 22050 Hz gives 44100 ± 1200 samples.
- [ ] **Step 2:** Run `go test ./internal/audio/`. Expected: FAIL.
- [ ] **Step 3:** Implement with `exec.CommandContext`. Errors include the tool name and the last 300 bytes of stderr.
- [ ] **Step 4:** Run. Expected: PASS.
- [ ] **Step 5:** Commit `feat: ffprobe/ffmpeg audio tools`.

### Task 13: Tempo detector

**Files:**
- Create: `internal/audio/tempo.go`
- Test: `internal/audio/tempo_test.go`

**Interfaces:**
- Produces: `var ErrNoTempo = errors.New("no tempo detected")` and `func DetectBPM(samples []float32, rate int) (float64, error)`, rounded to 1 decimal.

Algorithm (fixes what the tests can't pin down):
```text
frame=1024, hop=256 → env rate fr = rate/256 (≈86.13 Hz at 22050)
for each frame: Hann window → FFT (gonum fourier) → log(1+|X|) per bin
flux[n] = Σ max(0, mag[n][k] − mag[n−1][k]); subtract a 16-frame moving average; clamp ≥0
if len(samples) < 10*rate or max(flux)==0 or var(flux) tiny → ErrNoTempo
acf[L] = Σ flux[n]·flux[n+L] for L in [round(60*fr/200), round(60*fr/60)]
score[L] = acf[L]/acf[0] · exp(−0.5·(log2((60*fr/L)/120)/1.0)²)
best L = argmax score; if score[best]·(acf[0]>0) < 0.05 → ErrNoTempo
refine L with parabolic interpolation on acf around best; bpm = 60*fr/L
```

- [ ] **Step 1:** Write the failing tests. Fixtures are click tracks built in Go: 5 ms 2 kHz bursts at the target BPM over 30 s, at 22050 Hz, plus a little noise. They need no ffmpeg.
  - `TestDetectBPM_Clicks`: the table 90, 120, 128 and 150 is each within ±1.5.
  - `TestDetectBPM_Fast`: a 174 BPM click gives 174 ± 1.5 **or** 87 ± 1.5. This documents the octave limit.
  - `TestDetectBPM_SilenceErrors`: silence, and a steady 440 Hz sine, both return `ErrNoTempo`.
  - `TestDetectBPM_TooShort`: 5 s returns `ErrNoTempo`.
  - `TestDetectBPM_RealFile`: skips unless `BEATDOCTOR_MP3` is set. It decodes the middle 60 s with `DecodeMono` and expects 124 ± 2 (or 62 ± 2), logging the value.
- [ ] **Step 2:** Run. Expected: FAIL.
- [ ] **Step 3:** Implement as above.
- [ ] **Step 4:** Run `go test ./internal/audio/`, then `BEATDOCTOR_MP3=<local clone of github.com/marianina8/audiofile, outside this repo>/audio/beatdoctor.mp3 go test ./internal/audio/ -run RealFile -v`. Expected: PASS. Record the measured value in the commit message.
- [ ] **Step 5:** Commit `feat: tempo detector`.

### Task 14: Processor

**Files:**
- Create: `internal/processor/processor.go`, `internal/processor/filename.go`
- Test: `internal/processor/processor_test.go`, `internal/processor/filename_test.go`

**Interfaces:**
- Consumes: `store.Store`, `blob.Blob`, `audio.Tools`, `audio.DetectBPM`, `catalog.Ensure`.
- Produces:
  - `type Analyzer interface{ Probe(ctx, path string) (audio.Probe, error); IntegratedLUFS(ctx, path string) (float64, error); DecodeMono(ctx, path string, start, dur float64, rate int) ([]float32, error) }`. `audio.Tools` satisfies it.
  - `processor.Processor{Store store.Store; Blob blob.Blob; Analyzer Analyzer; Now func() time.Time; TmpDir string}`.
  - `func (p *Processor) Process(ctx, key string) error` returns nil after recording `failed`. It returns an error only when it can't write to the store.
  - `func TitleFromFilename(name string) string`: drop the extension and a trailing `-\d+` and `(\d+)`, remove the case-insensitive phrases `no-copyright-music`, `no-copyright`, `free-music`, `background-music` and `music`, swap `-` and `_` for spaces, collapse spaces, Title Case. An empty result becomes `"Untitled"`.

Flow:
1. Key `tracks/{id}.mp3` → id. Anything else → return nil and log.
2. Load the track. If none exists, create a minimal `processing` track.
3. Download to `TmpDir/{id}.mp3`, and remove it with `defer`.
4. Probe. Title falls back to `TitleFromFilename(original_filename)`, artist to `"Unknown Artist"`.
5. LUFS → energy.
6. If `bpm_source==manual`, keep it. Otherwise a valid TBPM → `tag`. Otherwise decode 60 s from `max(0, dur/2−30)` at 22050 and detect → `measured`.
7. `catalog.Ensure`.
8. Save as `ready`, keeping `tags`, `created_at`, `content_sha256` and `original_filename`, and set `updated_at`.

Any step-4–7 error → `failed` with an `error` message in the form `"<step>: <err>"`, e.g. `"tempo: no tempo detected"`.

- [ ] **Step 1:** Write the failing tests, using a fake `Analyzer` with scripted results:
  - `TestProcess_TaggedTrack`: TBPM `"124"` gives `bpm 124`, `bpm_source tag`, energy from LUFS −12 = 0.67, `ready`, and the artist and album rows exist.
  - `TestProcess_NoTags`: original filename `alex-morgan-no-copyright-music-578487.mp3` gives title `"Alex Morgan"`, artist `"Unknown Artist"`, `artist_id "unknown-artist"`, no album, and `bpm_source measured`.
  - `TestProcess_TempoFailureMarksFailed`: `DecodeMono` returns silence, giving `failed` with an error containing `"no tempo"`.
  - `TestProcess_PreservesManualBPMAndTags`: an existing `manual` 99.0 BPM and tags `[workout]` survive reprocessing.
  - `TestProcess_IgnoresOtherKeys`: `players/x.html` returns nil and leaves the store unchanged.
  - `TestTitleFromFilename`: the table covers all 19 of Marian's filenames (listed in T18 Step 1). For example `silentvoice-morning-fog-263352.mp3` → `"Silentvoice Morning Fog"` and `freesoundserver-…-203603 (1).mp3` → `"Freesoundserver Dancing In The Stardust"`.
- [ ] **Step 2:** Run. Expected: FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run `go test ./...`. Expected: PASS.
- [ ] **Step 5:** Commit `feat: track processor`.

### Task 15: Lambda entrypoints and the audiofile-api SAM stack

**Files:**
- Create:
  - `cmd/api/main.go`: `httpadapter.NewV2(api.New(deps)).ProxyWithContext`, with deps from env `TRACKS_TABLE`, `ARTISTS_TABLE`, `ALBUMS_TABLE`, `PLAYLISTS_TABLE`, `BUCKET`.
  - `cmd/processor/main.go`: an S3 event handler that loops over records and calls `Process` with URL-decoded keys.
  - `layers/ffmpeg/Makefile`: `build-FFmpegLayer` downloads the pinned johnvansickle `ffmpeg-<ver>-arm64-static.tar.xz`, checks the pinned SHA-256, and copies `ffmpeg`/`ffprobe` to `$(ARTIFACTS_DIR)/bin/`.
  - `samconfig.toml`: stack name `audiofile-api`, region `us-west-2`, `resolve_s3 = true`, `capabilities = "CAPABILITY_IAM"`. No email, no IDs.
- Modify: `template.yaml`, `Makefile` (`layer`, `build`, `validate`)
- Test: `internal/api/spec_test.go` (`TestTemplateSecurity`)

`template.yaml` contents, all CloudFormation-named:
- **Parameters:**
  - `AlertEmail` (`NoEcho`).
  - `CognitoDomainPrefix`.
- **Globals:** `provided.al2023`, `arm64`, `Architectures`, `Handler: bootstrap`, `BuildMethod: go1.x` metadata.
- **Cognito:**
  - `UserPool`: `AdminCreateUserConfig.AllowAdminCreateUserOnly: true`, `UsernameAttributes: [email]`, `AutoVerifiedAttributes: [email]`.
  - `UserPoolDomain` (`ManagedLoginVersion: 1`).
  - `ResourceServer`: identifier `audiofile`, scopes `read`/`write`.
  - `CliClient`: no secret, `ALLOW_USER_PASSWORD_AUTH` + `ALLOW_REFRESH_TOKEN_AUTH`.
  - `MachineClient`: secret, `client_credentials`, scopes `audiofile/read audiofile/write`.
  - `McpUsersGroup` named `mcp-users`.
- **HttpApi:** JWT authorizer with issuer `https://cognito-idp.${AWS::Region}.amazonaws.com/${UserPool}` and audience `[CliClient, MachineClient]`. `DefaultRouteSettings` throttle 20/40. `ApiFunction` events cover all 33 routes, already present from T5–T10.
- **Bucket:** `PublicAccessBlockConfiguration` all true, `BucketEncryption` SSE-S3, lifecycle `players/` 1 day, `NotificationConfiguration` to `ProcessorFunction` for `tracks/` + `.mp3`, and an `AWS::Lambda::Permission`. Avoid the circular dependency with SAM's S3 event on the function.
- **Tables:** four `AWS::DynamoDB::Table` with `PAY_PER_REQUEST`, keys and GSIs exactly as T3's `CreateTablesForTest`.
- **ApiFunction** policies:
  - `DynamoDBCrudPolicy` for the four tables.
  - `S3` actions `GetObject`, `PutObject`, `DeleteObject` on `tracks/*` and `players/*`.
- **ProcessorFunction:** 1024 MB, 60 s, `Layers: [FFmpegLayer]`, env `FFMPEG_PATH=/opt/bin/ffmpeg` and `FFPROBE_PATH=/opt/bin/ffprobe`. Policies are `GetObject` on `tracks/*` and CRUD on tracks, artists and albums only.
- **Budget:** `AWS::Budgets::Budget` COST, MONTHLY, $10 USD. Notifications are ACTUAL > 100% and FORECASTED > 100% to `AlertEmail`.
- **Outputs:** `ApiUrl`, `UserPoolId`, `CliClientId`, `MachineClientId`, `CognitoDomain`, `BucketName`, `IssuerUrl`. Mark them `Export` with names `audiofile-api-<Output>` for the other stacks.

- [ ] **Step 1:** Write the failing test `TestTemplateSecurity`. It parses `template.yaml` and asserts:
  - `AllowAdminCreateUserOnly` is true.
  - All four block-public-access flags are true.
  - The budget limit is `10`.
  - Throttle values are 20/40.
  - No literal 12-digit number appears anywhere in the template.
  - `samconfig.toml` contains no `@`.
- [ ] **Step 2:** Run. Expected: FAIL.
- [ ] **Step 3:** Implement the entrypoints, template and layer Makefile.
- [ ] **Step 4:** Run `go test ./...`, `sam validate --lint`, `make layer` and `sam build`. Expected: all succeed, and `file .aws-sam/build/FFmpegLayer/bin/ffprobe` reports `ELF 64-bit LSB executable, ARM aarch64`.
- [ ] **Step 5:** Commit `feat: Lambda entrypoints and audiofile-api SAM stack`.

## Phase B — First AWS contact (STOP before each task)

### Task 16: Create the `audiofile-admin` identity (with Marian)

**Files:** Create `docs/aws-setup.md`, which records the steps taken, with no IDs.

- [ ] **Step 1: STOP.** Ask Marian whether her account uses IAM Identity Center. Use Identity Center if available; otherwise an IAM user with console MFA. Explain the trade-off in two lines and wait.
- [ ] **Step 2:** Give her a permissions policy file, `docs/audiofile-admin-policy.json`, scoped to: CloudFormation, Lambda, API Gateway, Cognito IdP, DynamoDB, S3, IAM role/policy management limited to `arn:aws:iam::*:role/audiofile-*` and `mcp-*` (plus `iam:PassRole` for those), SSM `/audiofile/*`, Budgets, CloudWatch Logs, `sts:GetCallerIdentity`, `bedrock:InvokeModel` on `arn:aws:bedrock:us-west-2::foundation-model/mistral.voxtral-small-24b-2507` only, and `aws-marketplace:Subscribe` + `aws-marketplace:ViewSubscriptions` (needed once, the first time a Mistral model is called; note this in `docs/aws-setup.md`). Walk her through creating `audiofile-admin` with it, in her browser.
- [ ] **Step 3:** Have her run `! aws configure sso --profile audiofile-admin` (or the equivalent for the chosen mechanism) herself.
- [ ] **Step 4:** Run `aws sts get-caller-identity --profile audiofile-admin --query Arn --output text | sed -E 's/[0-9]{12}/<account>/'`. Expected: an ARN that ends in `audiofile-admin` with the account masked.
- [ ] **Step 5:** Commit `docs: audiofile-admin setup`.

> Every SAM stack's role names must start with `audiofile-` or `mcp-` to fit the policy, so set `RoleName`/`FunctionName`-derived prefixes via `!Sub "audiofile-${AWS::StackName}-…"`. Adjust T15's template in this task if needed, and re-run `sam validate`.

### Task 17: Deploy audiofile-api, create Marian's user, scripts, smoke

**Files:**
- Create: `scripts/lib.sh` (stack-output lookup `out <stack> <key>`, `require_token`), `scripts/token.sh`, `scripts/add-user.sh`, `scripts/store-machine-secret.sh`, `scripts/machine-token.sh`, `scripts/smoke.sh`
- Test: `scripts/test/smoke_dryrun.sh`, a shellcheck pass (`shellcheck scripts/*.sh`)

Script contracts:
- **`source scripts/token.sh`:** `read -rs` for the password and `AWS_PROFILE=audiofile-admin aws cognito-idp initiate-auth --auth-flow USER_PASSWORD_AUTH` against `CliClientId`. Export `TOKEN` (the access token) and print only `TOKEN set (expires in 60 min)`. If it's run instead of sourced, refuse with a hint.
- **`scripts/add-user.sh <email>`:** `admin-create-user`, which emails a temporary password, then `admin-add-user-to-group mcp-users`. It prints `user created and added to mcp-users` without echoing the email.
- **`scripts/store-machine-secret.sh`:** `describe-user-pool-client --query UserPoolClient.ClientSecret` piped straight into `ssm put-parameter --type SecureString --name /audiofile/machine-client-secret --overwrite`. The secret is never stored in a variable that gets echoed.
- **`source scripts/machine-token.sh`:** reads the secret from SSM, does `client_credentials` against `https://<CognitoDomain>/oauth2/token`, and exports `MACHINE_TOKEN`.
- **`scripts/smoke.sh`:**
  1. Unauthenticated `GET /health` returns 200.
  2. For all 32 protected routes with placeholder ids, no token returns 401. Print a ✓ table.
  3. Then, with `$TOKEN`:
     - Create a temporary 12 s mp3 click track with ffmpeg in `mktemp -d` and upload it via legacy `/upload`.
     - Poll status until it's ready, for up to 90 s.
     - Create a temporary playlist and exercise every route with real ids.
     - `DELETE` the temporary track and playlist.
     - Every call must return 2xx.
  4. Exit non-zero on the first failure, naming the route. Never touch library tracks or playlists other than its own.

- [ ] **Step 1:** Run `shellcheck scripts/*.sh`. Expected: clean.
- [ ] **Step 2: STOP.**
  - Show Marian `sam deploy --guided` changeset contents (`sam deploy --no-execute-changeset`, then `aws cloudformation describe-change-set` summarized as a resource-type list) and the cost notes.
  - Confirm region `us-west-2`.
  - Ask for the budget email. She types it via `! export ALERT_EMAIL=…` so it never appears in the transcript.
  - Ask for a Cognito domain prefix.
- [ ] **Step 3:** `sam deploy --profile audiofile-admin --parameter-overrides AlertEmail="$ALERT_EMAIL" CognitoDomainPrefix=<prefix>`. Expected: `Successfully created/updated stack - audiofile-api`.
- [ ] **Step 4: STOP.** Marian runs `! scripts/add-user.sh "$MY_EMAIL"` (her email in an env var), receives the temporary password, and sets a permanent one. Provide `scripts/set-password.sh`, which uses `respond-to-auth-challenge NEW_PASSWORD_REQUIRED` with silent reads.
- [ ] **Step 5:** Run `scripts/store-machine-secret.sh`, then `source scripts/token.sh` (Marian runs it via `!`) and `scripts/smoke.sh`. Expected: all ✓ and `smoke: PASS`.
- [ ] **Step 6:** Show the evidence:
  - `aws cognito-idp describe-user-pool --query 'UserPool.AdminCreateUserConfig.AllowAdminCreateUserOnly'` returns `true`.
  - `aws budgets describe-budgets --account-id "$(aws sts get-caller-identity --query Account --output text)" --query 'Budgets[].{Name:BudgetName,Limit:BudgetLimit.Amount}'` returns `10.0`. The account id stays inside the subshell and is never printed.
- [ ] **Step 7:** Commit `feat: deploy scripts and smoke test`.

### Task 18: Library metadata and seeding

**Files:**
- Create: `scripts/draft-library.sh`, `scripts/seed.sh`, `scripts/reset-playlists.sh`, `cmd/genreguess/main.go`, `cmd/genreguess/genre.go`
- Test: `cmd/genreguess/genre_test.go`
- Test: `shellcheck`; `scripts/seed.sh --dry-run`

Marian's 19 files in `~/audio` carry **no ID3 tags at all**: no title, artist, genre, TBPM or copyright. To give the catalog real artists, genres and albums:
- **`scripts/draft-library.sh`** writes `~/audio/library.csv` with header `filename,title,artist,album,genre,genre_source,year`. It never overwrites an existing CSV. **Marian is not required to edit it.**
  - Artist is the Pixabay uploader: the text before the first `-`, with a built-in override map for multi-word uploaders (`cinematic-soul` → `Cinematic Soul`, `alex-morgan` → `Alex Morgan`, `tazdev_music` → `Tazdev Music`, `freesoundserver` → `FreeSoundServer`).
  - Title is the remaining filename words minus filler (`no-copyright-music`, `free-music`, `background-music`, `musicno-copyright`, trailing id). When nothing is left, it becomes `"<Artist> Track <n>"`.
  - Genre is taken from filename keywords, with `genre_source=filename`: `rock` → Rock, `electro-industrial` → Industrial, `piano` → Classical, `cinematic` → Cinematic, `upbeat` → Pop. Otherwise it's left empty for `cmd/genreguess`.
  - Album and year are left empty.
- **`cmd/genreguess`** (Go, run locally with `go run ./cmd/genreguess`, never deployed):
  - Fixed genre list: `Electronic, Industrial, Rock, Pop, Hip Hop, Cinematic, Ambient, Classical`.
  - For each CSV row, ffmpeg cuts 30 s from the middle into a temp `mp3` (mono, 64 kbps).
  - The clip goes to Bedrock Converse (`bedrockruntime`, profile `audiofile-admin`, region `us-west-2`, model `mistral.voxtral-small-24b-2507`) as an `audio` content block (`format: mp3`, bytes), followed by a text block: `"Which one genre best fits this music? Answer with exactly one of: <list>."`. `maxTokens` is 10 and `temperature` is 0.
  - Replies are normalized case-insensitively against the list. Anything else counts as `unknown`.
  - Flags: `--validate` and `--fill`.
  - **`--validate`** runs only the rows with `genre_source=filename` and prints file, expected, guessed and ✓/✗.
    - Expected-sets: Industrial also accepts Electronic, Classical also accepts Ambient, and Cinematic also accepts Ambient.
    - It passes when at least 4 of 5 are correct.
  - **`--fill`** refuses to run unless the last `--validate` passed (a marker file in `~/audio/.genreguess-validated`). It fills only empty genres, with `genre_source=model`.
  - It prints no credentials. Cost is under $0.01.
  - Unit test `TestNormalizeGenre`: `" rock."` → `Rock`, `"Hip-hop"` → `Hip Hop`, `"jazz"` → `unknown`.
  - Unit test `TestBuildConverseInput`: one audio block with format mp3, then one text block containing all 8 genres. Uses a fake client; no AWS.
- **`scripts/seed.sh [--dry-run] [--include-nc]`:**
  1. Run ffprobe on every `~/audio/*.mp3` and print a table of file, title, artist, genre, TBPM and copyright. A `library.csv` row overrides empty tags, and the table marks the source.
  2. Flag NonCommercial when the copyright, comment or license matches `NonCommercial|BY-NC|CC-NC`. Skip those unless `--include-nc` is given.
  3. Compute SHA-256 and drop in-folder duplicates, reporting them.
  4. Fetch `/list` and skip files whose `content_sha256` is already uploaded.
  5. **STOP prompt:** `Upload N files? [y/N]`.
  6. For each file, write a tagged temp copy: `ffmpeg -i in -map 0 -c copy -id3v2_version 3 -metadata title=… … out.mp3` in `mktemp -d`. Originals are never modified. The SHA is computed on the **original** and sent as `content_sha256`. Then `POST /tracks/uploads`, `curl -X PUT` with the returned headers, and poll `/tracks/{id}/status` until `ready` or `failed`, for up to 120 s each.
  7. Print the final table: title, artist, genre, bpm, bpm source, energy. Then print the energy and BPM spread as min, median and max, and the count of tracks with bpm ≥ 120 and energy ≥ 0.6.
- **`scripts/reset-playlists.sh`** deletes every playlist and prints the count.

- [ ] **Step 1:** Write `cmd/genreguess` with its two unit tests (TDD), and `scripts/draft-library.sh`. Run `go test ./cmd/genreguess/` (expected: PASS) and `scripts/draft-library.sh`. Show Marian the drafted CSV for information only; she may glance at it but doesn't have to edit it.
- [ ] **Step 2: STOP.** Ask before the first Bedrock call (it costs under a cent and triggers the one-time Marketplace subscription). Then run `go run ./cmd/genreguess --validate` and show the ✓/✗ table.
  - **If it passes (≥ 4/5),** run `--fill`, show the filled genres, and commit nothing from `~/audio`.
  - **If it fails,** tell Marian, leave the genres blank, and continue.
  - Record the result (score, model, date) in `docs/notes/genre-guess.md`.
- [ ] **Step 3:** Run `scripts/seed.sh --dry-run`. Expected: the ffprobe table, the duplicate `(1)` file reported, and 18 to upload. **STOP:** show the table and wait for Marian's go-ahead.
- [ ] **Step 4:** Run `scripts/seed.sh`. Expected: 18 `ready` and 0 `failed`, and every row has a bpm and an energy.
  - If any track fails or the spread doesn't separate "upbeat", show Marian the numbers before changing anything (spec §6).
  - Half- or double-tempo outliers get fixed via `PATCH bpm` only with Marian's say-so.
- [ ] **Step 5:** Run `scripts/reset-playlists.sh`. Expected: `deleted 0 playlists`.
- [ ] **Step 6:** Commit `feat: library seeding scripts and genre guesser`.

### Task 19: Verify Cognito resource binding (spike, throwaway)

**Files:** Create `docs/notes/resource-binding.md` with the finding, no IDs.

- [ ] **Step 1:** Create a temporary public app client with the CLI: code flow, scopes `openid email`, callback `http://localhost:8765/callback`. Run a tiny local Go program from the scratchpad, outside the repo, that listens on 8765, builds an `/oauth2/authorize` URL with S256 PKCE and `resource=https://example.execute-api.us-west-2.amazonaws.com/v1/mcp`, exchanges the code, decodes the access token **without printing it**, and prints only `aud`, `token_use`, `cognito:groups` and `scope`.
- [ ] **Step 2: STOP.** Marian opens the URL and signs in.
- [ ] **Step 3:** Expected: `aud` equals the resource URL and `cognito:groups` contains `mcp-users`.
  - Also test whether `/oauth2/token` with `grant_type=refresh_token` keeps `aud`.
  - Also check whether `resource` must match a resource-server identifier.
  - Record all three answers.
- [ ] **Step 4:** Delete the temporary client.
- [ ] **Step 5:** If binding fails, **STOP**. Present the KMS fallback (bridge-signed tokens, about $1/month) and wait. Otherwise continue.
- [ ] **Step 6:** Commit `docs: Cognito resource binding finding`.

## Phase C — Auth and MCP hosting (local)

### Task 20: JWT verification

**Files:**
- Create: `internal/jwtverify/verify.go`
- Test: `internal/jwtverify/verify_test.go`

**Interfaces:**
- Produces:
  - `jwtverify.Claims{Subject, ClientID, TokenUse, Scope string; Audience, Groups []string; Expiry time.Time}`.
  - `jwtverify.New(issuer, jwksURL string) *Verifier`, with JWKS cached via `jwx/v3/jwk.Cache` with a 1 h refresh.
  - `func (v *Verifier) Verify(ctx, token, audience, requiredGroup string) (Claims, error)`.
  - Error vars: `ErrInvalid` (bad signature, issuer, expiry or `token_use != "access"`), `ErrAudience` and `ErrGroup`.

- [ ] **Step 1:** Write the failing tests. Generate an RSA key in the test, serve the JWKS via `httptest`, and sign with jwx.
  - A valid token passes.
  - A wrong `aud` gives `ErrAudience`.
  - No `mcp-users` group gives `ErrGroup`.
  - An expired token gives `ErrInvalid`.
  - Another issuer gives `ErrInvalid`.
  - An `HS256` token gives `ErrInvalid`.
  - An ID token (`token_use=id`) gives `ErrInvalid`.
  - A different key with the same `kid` gives `ErrInvalid`.
- [ ] **Step 2:** Run. Expected: FAIL.
- [ ] **Step 3:** Implement. Allow only RS256, with 30 s clock skew.
- [ ] **Step 4:** Run. Expected: PASS.
- [ ] **Step 5:** Commit `feat: JWT verifier`.

### Task 21: OAuth bridge, part 1: metadata, redirect allowlist, client resolution, DCR

**Files:**
- Create: `internal/oauth/config.go`, `internal/oauth/metadata.go`, `internal/oauth/redirect.go`, `internal/oauth/clients.go`, `internal/oauth/register.go`, `internal/oauth/state.go` (TTL-table abstraction with a memory implementation and a Dynamo implementation), `internal/oauth/server.go`
- Test: `internal/oauth/*_test.go`

**Interfaces:**
- Produces:
  - `oauth.Config{Issuer string; CognitoDomain string; CognitoClientID string; ChatGPTCallbackID string; Resources func(ctx) ([]string, error); HTTPClient *http.Client; Now func() time.Time}`. `Issuer` is taken from the request host when empty.
  - `oauth.State` interface: `Put(ctx, kind, id string, v any, ttl time.Duration) error`, `Get(ctx, kind, id string, v any) error` (`ErrNotFound`), `Delete(ctx, kind, id string) error`, and `Take(ctx, kind, id string, v any) error` (get and delete atomically, via a conditional delete in Dynamo). Implemented by `oauth.NewMemoryState()` and `oauth.NewDynamoState(client, table)`, keyed by `pk = kind#id` with a `ttl` attribute.
  - `oauth.New(cfg Config, st State) http.Handler` with routes `GET /.well-known/oauth-authorization-server`, `POST /register`, `GET /authorize`, `GET /callback` and `POST /token`. T22 fills in the last three.
  - `func AllowedRedirect(uri, chatgptCallbackID string) bool`.
  - `func (s *server) resolveClient(ctx, clientID string) (Client, error)` where `Client{ID string; RedirectURIs []string}`.

Allowlist (exact, except for the port):
- `https://claude.ai/api/mcp/auth_callback`
- `https://chatgpt.com/connector_platform_oauth_redirect`
- `https://chatgpt.com/connector/oauth/{ChatGPTCallbackID}` when set
- `http://localhost:{1-65535}/callback` and `http://127.0.0.1:{1-65535}/callback` (Claude Code)
- `http://localhost:6274/oauth/callback` and `http://localhost:6274/oauth/callback/debug` (MCP Inspector, for the lab; an addition to spec §8 that the lab needs)

CIMD: a `client_id` that is an `https://` URL on host `claude.ai` or `chatgpt.com`. Fetch it with a 3 s timeout and a 64 KB limit, require `client_id` equal to the URL, take `redirect_uris`, and cache for 10 min. Any other URL-shaped `client_id` gets `invalid_client`.
DCR: a JSON body with `redirect_uris` (all must pass the allowlist), `token_endpoint_auth_method` `none` (or absent). Store it as kind `client` with a 180-day TTL, refreshed on use. Respond 201 with `client_id` (random 22-char base64url) and the echoed metadata.

- [ ] **Step 1:** Write the failing tests:
  - `TestMetadata`: the JSON has exactly the spec §8 fields and values, with `issuer` equal to `Config.Issuer`.
  - `TestAllowedRedirect`:
    - Accepted: `http://localhost:53122/callback`, `http://127.0.0.1:1/callback` and the claude.ai callback.
    - Rejected: `http://localhost:53122/evil`, `http://evil.com/callback`, `https://claude.ai.evil.com/api/mcp/auth_callback`, `http://localhost:0/callback`, `http://localhost:70000/callback`, and a ChatGPT per-connector URL when no id is configured.
  - `TestRegister`:
    - Good input gives 201.
    - A bad redirect gives 400 `invalid_redirect_uri`.
    - A non-JSON body gives 400.
  - `TestResolveClient_CIMD`: use an `httptest` TLS server, with the allowed-host check made injectable for tests.
    - A matching document resolves.
    - A mismatched `client_id` gives an error.
    - A disallowed host gives an error.
    - A document over 64 KB gives an error.
    - A server that sleeps 5 s gives a timeout error.
- [ ] **Step 2:** Run `go test ./internal/oauth/`. Expected: FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run. Expected: PASS.
- [ ] **Step 5:** Commit `feat: OAuth bridge metadata, redirects, client registration`.

### Task 22: OAuth bridge, part 2: authorize, callback, token

**Files:**
- Create: `internal/oauth/authorize.go`, `internal/oauth/callback.go`, `internal/oauth/token.go`, `internal/oauth/pkce.go`, `cmd/mcpauth/main.go`, `auth/template.yaml`, `auth/samconfig.toml`
- Test: `internal/oauth/flow_test.go`

**Interfaces:**
- Consumes: T21, and `jwtverify.Verifier.Verify` (T20), called on Cognito's access token after the code exchange with `audience` = the resource and `requiredGroup` = `mcp-users`.
- Produces the full flow:
  - **`/authorize`:**
    - Requires `response_type=code`, `client_id`, `redirect_uri`, `code_challenge` with `code_challenge_method=S256`, and `resource` in `Config.Resources`. `state` is optional, `scope` is optional (intersected with `openid email`).
    - An error before the redirect is validated renders a 400 page and **never redirects**. After that, errors redirect with `error=…&iss=…`.
    - Saves `pending` (10 min) with the app params plus the bridge's own PKCE verifier, then 302s to `https://{CognitoDomain}/oauth2/authorize?...&resource=<resource>&state=<pendingID>`.
  - **`/callback`:**
    - Takes `pending` and exchanges the code at Cognito `/oauth2/token` (form, public client, PKCE).
    - Verifies the access token (`aud` = resource, group `mcp-users`). On `ErrGroup` it redirects `error=access_denied`.
    - Stores `code#sha256(code)` (5 min) with the Cognito tokens and the app params, then 302s to the app's `redirect_uri?code=…&state=…&iss=<Issuer>`.
  - **`/token`:**
    - Accepts only `application/x-www-form-urlencoded`.
    - `authorization_code`: `Take`s the code (single use), checks `client_id`, `redirect_uri`, S256 of `code_verifier` and `resource` (if sent), and returns `{access_token, token_type:"Bearer", expires_in, refresh_token, scope}`.
    - `refresh_token`: proxies to Cognito, mapping Cognito's `invalid_grant` to `invalid_grant`.
    - Errors use RFC 6749 JSON with `Cache-Control: no-store`.
  - **`auth/template.yaml`:**
    - Imports from `audiofile-api` exports.
    - An HttpApi resource, defined explicitly with `$default` stage and throttle 10/20, to avoid circular references.
    - A `BridgeClient` (`AWS::Cognito::UserPoolClient`: public, code flow, scopes `openid email`, callback `!Sub https://${BridgeApi}.execute-api.${AWS::Region}.amazonaws.com/callback`).
    - A TTL table and `BridgeFunction`, with env `COGNITO_DOMAIN`, `COGNITO_CLIENT_ID`, `ISSUER_URL` (pool issuer for jwtverify), `STATE_TABLE`, `RESOURCES_SSM_PREFIX=/audiofile/mcp-resources/` and `CHATGPT_CALLBACK_ID` (a parameter, default empty).
    - Policies: CRUD on its table, `ssm:GetParametersByPath` on that prefix.
    - Output `BridgeUrl`.

- [ ] **Step 1:** Write the failing test `TestFullFlow`. Use a fake Cognito `httptest` server that issues RS256 tokens with `aud` = the requested resource and `cognito:groups`. Drive `/authorize` → follow to the fake Cognito → `/callback` → app redirect → `/token`. Assert:
  - The token response is correct, and `iss` is in the app redirect.
  - Code reuse gives `invalid_grant`.
  - A wrong `code_verifier` gives `invalid_grant`.
  - A wrong `redirect_uri` gives `invalid_grant`.
  - A `resource` not registered gives 400, with no redirect.
  - `code_challenge_method=plain` gives a redirect with `invalid_request`.
  - A user without the group gives a redirect with `access_denied`.
  - A JSON body to `/token` gives 400.
  - `refresh_token` round-trips through the fake.
  - Each handler finishes within 100 ms against the fakes. This proxies Claude's 10 s budget.
- [ ] **Step 2:** Run. Expected: FAIL.
- [ ] **Step 3:** Implement. `Resources` reads SSM with a 60 s cache.
- [ ] **Step 4:** Run `go test ./...` and `sam validate --lint -t auth/template.yaml`. Expected: PASS.
- [ ] **Step 5:** Commit `feat: OAuth bridge authorize, callback, token and stack`.

### Task 23: mcp-host template and authorizer

**Files:**
- Create: `cmd/mcpauthorizer/main.go`, `internal/authorizer/authorizer.go`, `mcphost/template.yaml`
- Test: `internal/authorizer/authorizer_test.go`

**Interfaces:**
- Consumes: `jwtverify`.
- Produces:
  - `authorizer.Handle(ctx, v *jwtverify.Verifier, req events.APIGatewayCustomAuthorizerRequest) (events.APIGatewayCustomAuthorizerResponse, error)`.
    - The audience is derived from `req.MethodArn` (`arn:aws:execute-api:{region}:{acct}:{apiId}/{stage}/...`) as `https://{apiId}.execute-api.{region}.amazonaws.com/{stage}/mcp`.
    - A missing, malformed or invalid token or a bad audience returns `errors.New("Unauthorized")`, which API Gateway turns into a 401.
    - `ErrGroup` returns a Deny policy (403).
    - A valid token returns Allow on `arn:…:{apiId}/{stage}/*` with `principalId` = sub.
  - **`mcphost/template.yaml`** (a nested app):
    - Parameters: `ServerName`, `ServerFunctionArn`, `BridgeUrl`, `IssuerUrl`.
    - A REST API with stage `v1` and MethodSettings throttle 20/40.
    - `/mcp` ANY: Lambda proxy to `ServerFunctionArn`, with a TOKEN authorizer (`Authorization` header, 300 s cache).
    - `/.well-known/oauth-protected-resource` and `/.well-known/oauth-protected-resource/mcp` GET: MOCK integration returning `{"resource":"https://${Api}.execute-api.${AWS::Region}.amazonaws.com/v1/mcp","authorization_servers":["${BridgeUrl}"],"scopes_supported":["openid","email"],"bearer_methods_supported":["header"]}`, unauthenticated.
    - A `GatewayResponse` for `UNAUTHORIZED` with header `WWW-Authenticate: 'Bearer resource_metadata="https://${Api}.execute-api.${AWS::Region}.amazonaws.com/v1/.well-known/oauth-protected-resource"'`.
    - A Lambda permission for the API to invoke the server function.
    - `AWS::SSM::Parameter` `/audiofile/mcp-resources/${ServerName}` = the MCP URL.
    - Output `McpUrl`.

- [ ] **Step 1:** Write the failing tests:
  - Audience derivation for a sample ARN.
  - Missing token gives an `"Unauthorized"` error.
  - A wrong-audience token gives `"Unauthorized"`.
  - No group gives a Deny effect.
  - A valid token gives Allow with a wildcard resource.
- [ ] **Step 2:** Run. Expected: FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run `go test ./...` and `sam validate --lint -t mcphost/template.yaml`. Expected: PASS.
- [ ] **Step 5:** Commit `feat: reusable MCP host with gateway auth`.

### Task 24: Throwaway test MCP server

**Files:**
- Create: `internal/mcptest/server.go`, `servers/mcptest/main.go`, `servers/mcptest/template.yaml` (the server function plus an `AWS::Serverless::Application` nesting `../../mcphost/template.yaml`), `servers/mcptest/samconfig.toml` (stack `mcp-test`)
- Test: `internal/mcptest/server_test.go`

**Interfaces:**
- Produces:
  - `mcptest.NewServer() *mcp.Server`, with tools:
    - `list_things`: description `"List the things."`, annotations `ReadOnlyHint: true`, no input, output `{"things":["apple","banana","cherry"]}`.
    - `delete_thing`: description `"Delete a thing by name."`, annotations `DestructiveHint: ptr(true)`, input `{"name": string}` (required), output text `deleted <name>`. It is in-memory only.
  - `mcptest.Handler() http.Handler` = `mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return NewServer() }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})`, mounted at `/mcp`.
  - `servers/mcptest/main.go` serves it via `httpadapter.New` (REST v1 events).
  - Pin `github.com/modelcontextprotocol/go-sdk v1.8.0`.

- [ ] **Step 1:** Write the failing test `TestToolsOverHTTP`. Start `httptest.NewServer(Handler())`, connect with the SDK client's streamable transport, and list tools. Assert:
  - Exactly 2 tools with the names, descriptions and annotations above.
  - `delete_thing` has `DestructiveHint` true and no `ReadOnlyHint`.
  - Calling `delete_thing{name:"apple"}` returns `deleted apple`.
  - Calling it without `name` returns an error result.
- [ ] **Step 2:** Run. Expected: FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run `go test ./...`, `sam validate --lint -t servers/mcptest/template.yaml` and `sam build -t servers/mcptest/template.yaml`. Expected: PASS.
- [ ] **Step 5:** Commit `feat: throwaway test MCP server`.

## Phase D — Deploy, test with apps, report, teach

### Task 25: Deploy mcp-auth and mcp-test

- [ ] **Step 1: STOP.** Show the changesets for `auth/` and `servers/mcptest/`: resource types and expected cost (pennies). Wait for a yes.
- [ ] **Step 2:** `sam deploy` the `auth` stack, then the `mcp-test` stack, passing `BridgeUrl` and `IssuerUrl` from outputs.
- [ ] **Step 3:** Verify with curl and jq, printing no tokens:
  - `POST /v1/mcp` with no token returns 401 with a `WWW-Authenticate` header containing `resource_metadata=`.
  - The protected-resource metadata `resource` equals `McpUrl` exactly.
  - `{BridgeUrl}/.well-known/oauth-authorization-server` has `code_challenge_methods_supported: ["S256"]`.
  - `/authorize` with a bad `redirect_uri` returns 400 and no `Location`.
  - `/authorize` with an unregistered `resource` returns 400.
  - Time each call: under 2 s warm.
- [ ] **Step 4:** Run `source scripts/machine-token.sh && curl -H "Authorization: Bearer $MACHINE_TOKEN" $ApiUrl/tracks?limit=1`. Expected: 200 (proves the machine client path). Output shows only the status code.
- [ ] **Step 5:** Commit `chore: deploy notes for auth and test server` (only if files changed, e.g. `docs/aws-setup.md`).

### Task 26: TEST-APPROVALS.md

**Files:** Create `TEST-APPROVALS.md`.

- [ ] **Step 1:** Write step-by-step sections:
  - **Claude:** Settings › Connectors › Add custom connector, paste `McpUrl`, sign in, then ask "list the things" and "delete the banana".
  - **Claude Code:** `claude mcp add --transport http things <McpUrl>`, then `/mcp` to authenticate.
  - **ChatGPT developer mode:** enable developer mode, create the connector, and copy the callback id. If ChatGPT shows a per-connector callback, Marian runs `sam deploy` for `auth` with `ChatGPTCallbackId`. Use the same two prompts.
  - **Results table:** columns App, Asked before list_things?, Asked before delete_thing?, Options offered, Notes. One row each for Claude web, Claude desktop, Claude Code and ChatGPT, all left blank.
- [ ] **Step 2:** Commit `docs: TEST-APPROVALS`.
- [ ] **Step 3: STOP.** Hand it to Marian. Help her debug the connection live: read the bridge's CloudWatch logs, which never contain tokens. Once she has filled in the table, commit `docs: approval test results`.

### Task 27: Generator dry run

**Files:** Create `DRYRUN-REPORT.md`. Nothing is generated in the repo.

- [ ] **Step 1:** `rm -rf /tmp/server-a-dryrun && npx --yes openapi-mcp-generator@4.1.0 --input openapi.yaml --output /tmp/server-a-dryrun --transport=streamable-http --port=3000`. Expected: it completes.
- [ ] **Step 2:** `cd /tmp/server-a-dryrun && npm install && npm run build`. Record success or the exact errors.
- [ ] **Step 3:** Start it with `API_BASE_URL=<ApiUrl> BEARER_TOKEN_BEARERAUTH="$MACHINE_TOKEN" npm run start:http`. Then use the MCP Inspector CLI (`npx @modelcontextprotocol/inspector --cli http://localhost:3000/mcp --method tools/list`) to list tools and call `listTracks` and `searchTracks` with `bpm_min=120`.
- [ ] **Step 4:** Write the report:
  - Tool count (expect 33) and the full list of names.
  - Five sample descriptions, verbatim.
  - Whether any annotations are present (grep the output).
  - Build and start results.
  - A live call result, as a count only.
  - "Would break on camera": the stateful session map on Lambda, the `BEARER_TOKEN_BEARERAUTH` env var name, the 3.1 schema conversions that failed, the `[DEPRECATED]` and `(Tags:)` decorations, the legacy multipart `/upload` tool, the overlap between list, search and listTracks, and the player's link usefulness.
  - What it would take to host it on mcp-host: LWA layer, `PORT`, and stateless patch size.
- [ ] **Step 5:** Commit `docs: generator dry-run report`.

### Task 28: LEARN-MCP lab (live with Marian)

**Files:** Create `LEARN-MCP.md` and `OAUTH-FLOW-STORYBOARD.md`.

- [ ] **Step 1:** Write `LEARN-MCP.md`. Its three steps are exactly spec §12's (Inspector, apps, login picture), each with what to run or click, what to look for, a plain-language explanation, and a "My explanations" section with three blank bullets. Commit `docs: LEARN-MCP lab`.
- [ ] **Step 2: STOP before step 1 of the lab.**
  - Marian runs `npx @modelcontextprotocol/inspector`, connects to `McpUrl` with OAuth, and lists the tools.
  - Wait for what she sees, then explain: the app asks the server for its tools, the model picks one from the descriptions, and the app calls it.
  - Get her one-sentence explanation, correct it if needed, and save it.
- [ ] **Step 3: STOP before step 2.** Claude and ChatGPT: list, then delete. Explain, get her sentence, save it.
- [ ] **Step 4: STOP before step 3.** Write `OAUTH-FLOW-STORYBOARD.md`: 7 plain-language steps, one line each, for a motion graphic (401 → metadata → login service → Cognito sign-in → code → token → tool call). Review with her, get her sentence, save it.
- [ ] **Step 5:** Commit `docs: LEARN-MCP explanations and OAuth storyboard`.

### Task 29: README and final verification

**Files:** Create or modify `README.md`.

- [ ] **Step 1:** Write the README:
  - Book credit: the base is from Chapter 3 of Marian's book, with a link to https://github.com/marianina8/audiofile. It was extended and published separately, and the original used `dhowden/tag`.
  - The mermaid diagram from spec §4.
  - Prerequisites (Go, SAM, ffmpeg, Docker for `test-dynamo`, the `audiofile-admin` profile).
  - Deploy order: `make layer` → `sam deploy` api → add-user → store-machine-secret → auth → mcp-test.
  - Seeding (`draft-library.sh`, `seed.sh`).
  - Scripts.
  - Teardown: `sam delete` in reverse order (mcp-test, auth, api). Empty the bucket first with `aws s3 rm --recursive`, and note that this deletes the library, which `~/audio` re-seeds.
  - Limits: scan-based search; the API can't distinguish users behind MCP; default URLs change if a stack is recreated.
  - Account hygiene: MFA on root, no long-lived keys.
- [ ] **Step 2:** Run the definition-of-done evidence and paste the real output into the final summary:
  - `go test ./...`
  - `make lint-spec`
  - `scripts/smoke.sh`
  - The sign-up setting
  - The budget
  - `GET /search?bpm_min=0` showing the bpm and energy count equal to the track count
  - Confirmation that TEST-APPROVALS, DRYRUN-REPORT, LEARN-MCP and the storyboard exist and are filled in
  - A secret scan: `git grep -nE '[0-9]{12}|@[a-z0-9-]+\.[a-z]{2,}|eyJ[a-zA-Z0-9_-]{10,}'`, which must show only allowed hits (the `noreply@anthropic.com` trailer and doc URLs)
- [ ] **Step 3:** Commit `docs: README`.
- [ ] **Step 4:** Give the final summary:
  - Endpoint count (33).
  - AWS resources created, by stack.
  - Estimated monthly cost at demo use: Lambda, API Gateway, DynamoDB, S3, Cognito and SSM all within free tier or pennies; the realistic total is under $1 a month.
  - The pre-recording checklist: don't recreate stacks; run `reset-playlists.sh`; make sure `$TOKEN` hasn't expired; keep the ChatGPT callback id; check the energy spread.
