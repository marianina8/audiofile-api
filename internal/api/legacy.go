package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/marianina8/audiofile-api/internal/model"
)

const (
	// maxLegacyFileBytes is the largest file POST /upload accepts, sized to fit
	// the Lambda payload cap. maxLegacyBodyBytes leaves room for multipart overhead.
	maxLegacyFileBytes = 4500000
	maxLegacyBodyBytes = 4600000
)

func tooLarge(w http.ResponseWriter) {
	writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large",
		"file exceeds the 4500000 byte limit of this route; use POST /tracks/uploads for larger files")
}

// readLegacyFile returns the filename and bytes of the multipart "file" part.
// It reports the status and message to respond with when ok is false.
func readLegacyFile(w http.ResponseWriter, r *http.Request) (name string, data []byte, ok bool) {
	body := http.MaxBytesReader(w, r.Body, maxLegacyBodyBytes)
	r.Body = body
	mr, err := r.MultipartReader()
	if err != nil {
		badRequest(w, "expected a multipart/form-data body")
		return "", nil, false
	}
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			badRequest(w, "missing file field")
			return "", nil, false
		}
		if err != nil {
			return "", nil, partError(w, err)
		}
		if part.FormName() != "file" {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(part, maxLegacyFileBytes+1))
		if err != nil {
			return "", nil, partError(w, err)
		}
		if len(data) > maxLegacyFileBytes {
			tooLarge(w)
			return "", nil, false
		}
		return part.FileName(), data, true
	}
}

// partError writes the response for a failed multipart read and returns false.
func partError(w http.ResponseWriter, err error) bool {
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		tooLarge(w)
	} else {
		badRequest(w, "invalid multipart body")
	}
	return false
}

func (s *server) uploadAudio(w http.ResponseWriter, r *http.Request) {
	name, data, ok := readLegacyFile(w, r)
	if !ok {
		return
	}
	if !strings.HasSuffix(strings.ToLower(name), ".mp3") {
		badRequest(w, "filename must end in .mp3")
		return
	}

	id := s.NewID()
	key := "tracks/" + id + ".mp3"
	sum := sha256.Sum256(data)
	ts := s.Now().UTC().Format(time.RFC3339)
	t := model.Track{
		ID: id, Status: model.StatusProcessing, S3Key: key,
		OriginalFilename: name, SizeBytes: int64(len(data)), ContentSHA256: hex.EncodeToString(sum[:]),
		CreatedAt: ts, UpdatedAt: ts,
	}
	// The track row goes first so the processor triggered by the blob finds it.
	if err := s.Store.PutTrack(r.Context(), t); err != nil {
		internalError(w)
		return
	}
	if err := s.Blob.Put(r.Context(), key, mp3Type, data); err != nil {
		internalError(w)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, id)
}

func (s *server) getAudio(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		badRequest(w, "id is required")
		return
	}
	t, err := s.Store.GetTrack(r.Context(), id)
	if err != nil {
		storeError(w, err, "audio")
		return
	}
	body, err := model.AudioFromTrack(t).JSON()
	if err != nil {
		internalError(w)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, body)
}

func (s *server) listAudio(w http.ResponseWriter, r *http.Request) {
	tracks, err := s.Store.AllTracks(r.Context())
	if err != nil {
		internalError(w)
		return
	}
	sort.Slice(tracks, func(i, j int) bool {
		if tracks[i].CreatedAt != tracks[j].CreatedAt {
			return tracks[i].CreatedAt < tracks[j].CreatedAt
		}
		return tracks[i].ID < tracks[j].ID
	})
	list := make([]model.Audio, 0, len(tracks))
	for _, t := range tracks {
		list = append(list, model.AudioFromTrack(t))
	}
	b, err := json.MarshalIndent(list, "", "    ")
	if err != nil {
		internalError(w)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
}
