# audiofile (book, Chapter 3) → audiofile-api

`audiofile-api` is **not a fork** of [marianina8/audiofile](https://github.com/marianina8/audiofile). It's a new repo built from the same idea, so a file-by-file `git diff` isn't meaningful. This page maps what the book had to what exists now.

## At a glance

| | Book `audiofile` | `audiofile-api` |
|---|---|---|
| What it is | CLI + local HTTP API (Chapter 3 example) | Deployed music API on AWS + OAuth bridge + MCP hosting |
| Runs on | `go run cmd/api/main.go`, port 8000 (`-p` flag) | AWS Lambda behind API Gateway (HTTP API), deployed with SAM |
| Routes | 3 | 34 (the 3 book routes + 30 new + public `/play/{pid}`) |
| Auth | none | Cognito JWT on every route except `/health` and `/play/{pid}` |
| Storage | flat files in `~/audiofile/<id>/` (audio + `metadata.json`) | S3 (audio, player pages) + DynamoDB (`tracks`, `artists`, `albums`, `playlists`) |
| Tag reading | `github.com/dhowden/tag` | `ffprobe` (in a Lambda layer) |
| Transcription | AssemblyAI (`extractors/transcript`) | **removed** |
| New metadata | none | `bpm` (tag or measured), `energy` (0–1 from loudness), duration, status, user tags |
| Processing | goroutine inside the upload handler | separate Lambda triggered by S3 upload |
| CLI | `cmd/cli` (upload/get/list commands, parser) | **not carried over**; scripts in `scripts/` instead |

## The three book routes: kept, same shapes

| Route | Book behaviour | Now |
|---|---|---|
| `POST /upload` | multipart field `file`, saves it, returns the new id as the body | Same field and same plain-text id response. Writes to S3 and processing runs automatically. **Changes:** files must be `.mp3`; over ~4.5 MB returns `413` (Lambda request limit) and points to `POST /tracks/uploads` |
| `GET /request?id=` | returns the `Audio` JSON, pretty-printed with 4 spaces; missing id → 500 | Same `Audio` shape, same indent. **Changes:** missing id → `400`, unknown id → `404` (JSON error) |
| `GET /list` | array of `Audio` | Same shape, all tracks (any status), sorted oldest first |

### `Audio` JSON shape (unchanged field names)

```json
{
    "Id": "…",
    "Path": "tracks/<id>.mp3",
    "Metadata": {
        "tags": {
            "title": "", "album": "", "artist": "", "album_artist": "",
            "composer": "", "genre": "", "year": 0, "lyrics": "", "comment": ""
        },
        "transcript": ""
    },
    "Status": "Initiating | Complete",
    "Error": []
}
```

Differences in values (not shape):
- `Path` is now the S3 key (`tracks/<id>.mp3`) instead of a local file path.
- `transcript` is always `""` (transcription removed).
- `Status`: a track still being processed shows `"Initiating"`; ready **or** failed shows `"Complete"` (failed carries its message in `Error`, as the book did).

## Book files → where they went

| Book file | Now |
|---|---|
| `models/audio.go`, `metadata.go`, `tags.go` | Same types in `internal/model/legacy.go` (exact field and JSON names) + new `Track`, `Artist`, `Album`, `Playlist` models |
| `services/metadata/handler_upload.go` | `internal/api/legacy.go` (upload) + `internal/processor/` (the background work) |
| `services/metadata/handler_getbyid.go` | `internal/api/legacy.go` (`/request`) |
| `services/metadata/handler_list.go` | `internal/api/legacy.go` (`/list`) |
| `services/metadata/metadata.go` (mux, server) | `internal/api/server.go` + `routes.go` (route table shared with the OpenAPI spec and SAM template) |
| `storage/flatfile.go` | `internal/store/` (DynamoDB + in-memory test store) and `internal/blob/` (S3) |
| `extractors/tags/tags.go` | `internal/audio/probe.go` (ffprobe) |
| `extractors/transcript/transcript.go` | removed |
| `internal/interfaces/*.go` | replaced by `store.Store` and `blob.Blob` interfaces |
| `cmd/api/main.go` | `cmd/api/main.go` (Lambda entrypoint) |
| `cmd/cli/**` | not carried over |
| `audio/beatdoctor.mp3` | not included (CC BY-NC); used only as a local BPM check |

## What's entirely new

- **30 new endpoints:** tracks (presigned uploads, get/update/delete, stream link, status), search (by text, artist, genre, year, BPM, energy), artists, albums, genres, tags, playlists (create, edit, add, remove, reorder), and short **player links** that play a playlist in the browser.
- **Audio analysis:** ffmpeg layer, Go tempo detector, loudness-based energy.
- **OpenAPI 3.1 spec** with a test that fails if a route is missing from the spec or the SAM template.
- **Security:** Cognito (self sign-up off, `mcp-users` group), per-route scopes, throttling, private S3, least-privilege IAM, $10 budget alert.
- **MCP infrastructure:** OAuth bridge for Claude and ChatGPT (`auth/`), reusable MCP hosting (`mcphost/`), a test MCP server (`servers/mcptest/`), and tooling to generate, patch and host an MCP server from the spec (`servers/generated/`, `scripts/generate-server.sh`).
- **Scripts:** seeding, smoke test, tokens, user setup, playlist reset, deploy helpers.
- **Docs:** README, LEARN-MCP, OAUTH-FLOW-STORYBOARD, TEST-APPROVALS, DRYRUN-REPORT.

## Decision for you

- [x] Approve (Marian, 2026-10-22): the book routes keep their shapes, with the value changes listed above (`Path`, `transcript`, `Status` mapping, `.mp3`-only, 4.5 MB cap, 400/404 codes).
- [x] Nothing to keep closer to the book.
