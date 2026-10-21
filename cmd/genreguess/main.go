// Command genreguess asks a Bedrock audio model for the genre of each track in
// ~/audio/library.csv. It is a local, one-off tool and is never deployed.
//
//	go run ./cmd/genreguess --validate   # check the model on filename-labelled rows
//	go run ./cmd/genreguess --fill       # fill empty genres (needs a passing --validate)
package main

import (
	"context"
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
)

const (
	region        = "us-west-2"
	markerName    = ".genreguess-validated"
	passThreshold = 4
	clipSeconds   = 30
)

func main() {
	home, _ := os.UserHomeDir()
	dir := flag.String("dir", filepath.Join(home, "audio"), "folder holding the mp3 files")
	csvPath := flag.String("csv", "", "library CSV (default <dir>/library.csv)")
	validate := flag.Bool("validate", false, "test the model on rows whose genre came from the filename")
	fill := flag.Bool("fill", false, "fill empty genres (requires a passing --validate)")
	flag.Parse()
	if *csvPath == "" {
		*csvPath = filepath.Join(*dir, "library.csv")
	}
	if *validate == *fill {
		fmt.Fprintln(os.Stderr, "usage: genreguess --validate | --fill  [--dir D] [--csv F]")
		os.Exit(2)
	}
	if err := run(context.Background(), *dir, *csvPath, *validate); err != nil {
		fmt.Fprintln(os.Stderr, "genreguess:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, dir, csvPath string, validate bool) error {
	marker := filepath.Join(dir, markerName)
	if !validate {
		if _, err := os.Stat(marker); err != nil {
			return errors.New("--fill needs a passing --validate first (no marker file)")
		}
	}
	header, rows, err := readCSV(csvPath)
	if err != nil {
		return err
	}
	col := indexOf(header)
	for _, c := range []string{"filename", "genre", "genre_source"} {
		if _, ok := col[c]; !ok {
			return fmt.Errorf("%s: missing column %q", csvPath, c)
		}
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return errors.New("ffmpeg is required")
	}

	// Profile comes from AWS_PROFILE (default audiofile-admin); credentials
	// are resolved by the SDK and never printed.
	profile := os.Getenv("AWS_PROFILE")
	if profile == "" {
		profile = "audiofile-admin"
	}
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region), config.WithSharedConfigProfile(profile))
	if err != nil {
		return fmt.Errorf("load AWS config: %w", err)
	}
	client := bedrockruntime.NewFromConfig(cfg)

	if validate {
		return runValidate(ctx, client, dir, rows, col, marker)
	}
	return runFill(ctx, client, dir, csvPath, header, rows, col)
}

func runValidate(ctx context.Context, c Converser, dir string, rows [][]string, col map[string]int, marker string) error {
	_ = os.Remove(marker) // a stale pass must never survive a new run
	correct, total := 0, 0
	fmt.Printf("%-60s %-12s %-12s\n", "file", "expected", "guessed")
	for _, r := range rows {
		if r[col["genre_source"]] != "filename" {
			continue
		}
		total++
		expected := r[col["genre"]]
		guessed := guessFile(ctx, c, filepath.Join(dir, r[col["filename"]]))
		mark := "✗"
		if Accepts(expected, guessed) {
			mark = "✓"
			correct++
		}
		fmt.Printf("%-60s %-12s %-12s %s\n", r[col["filename"]], expected, guessed, mark)
	}
	fmt.Printf("score: %d/%d (model %s)\n", correct, total, ModelID)
	if total == 0 || correct < passThreshold {
		fmt.Println("validation FAILED: leave genres blank")
		return errors.New("validation failed")
	}
	if err := os.WriteFile(marker, []byte(fmt.Sprintf("%d/%d %s %s\n", correct, total, ModelID, time.Now().UTC().Format(time.RFC3339))), 0o600); err != nil {
		return err
	}
	fmt.Println("validation passed; --fill is now allowed")
	return nil
}

func runFill(ctx context.Context, c Converser, dir, csvPath string, header []string, rows [][]string, col map[string]int) error {
	filled := 0
	for _, r := range rows {
		if r[col["genre"]] != "" {
			continue
		}
		g := guessFile(ctx, c, filepath.Join(dir, r[col["filename"]]))
		fmt.Printf("%-60s %s\n", r[col["filename"]], g)
		if g == Unknown || strings.HasPrefix(g, "error") {
			continue
		}
		r[col["genre"]] = g
		r[col["genre_source"]] = "model"
		filled++
	}
	if err := writeCSV(csvPath, header, rows); err != nil {
		return err
	}
	fmt.Printf("filled %d genres in %s\n", filled, csvPath)
	return nil
}

// guessFile cuts a clip and asks the model. Failures are reported as the
// string "error: ..." so one bad file does not abort the run.
func guessFile(ctx context.Context, c Converser, path string) string {
	clip, err := cutClip(ctx, path)
	if err != nil {
		return "error: " + err.Error()
	}
	g, err := Guess(ctx, c, clip)
	if err != nil {
		return "error: " + err.Error()
	}
	return g
}

// cutClip returns 30 s from the middle of the file as mono 64 kbps mp3 bytes.
// The temp file is removed before returning.
func cutClip(ctx context.Context, path string) ([]byte, error) {
	dur, err := duration(ctx, path)
	if err != nil {
		return nil, err
	}
	start := dur/2 - clipSeconds/2
	if start < 0 {
		start = 0
	}
	tmp, err := os.MkdirTemp("", "genreguess")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	out := filepath.Join(tmp, "clip.mp3")
	cmd := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-loglevel", "error",
		"-ss", strconv.FormatFloat(start, 'f', 2, 64), "-t", strconv.Itoa(clipSeconds),
		"-i", path, "-map", "0:a", "-ac", "1", "-b:a", "64k", "-c:a", "libmp3lame", out)
	if b, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("ffmpeg: %v: %s", err, strings.TrimSpace(string(b)))
	}
	return os.ReadFile(out)
}

func duration(ctx context.Context, path string) (float64, error) {
	b, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-show_entries", "format=duration",
		"-of", "default=nw=1:nk=1", path).Output()
	if err != nil {
		return 0, fmt.Errorf("ffprobe %s: %w", filepath.Base(path), err)
	}
	return strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
}

func readCSV(path string) ([]string, [][]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	all, err := csv.NewReader(f).ReadAll()
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(all) == 0 {
		return nil, nil, fmt.Errorf("%s is empty", path)
	}
	for i, r := range all[1:] {
		if len(r) != len(all[0]) {
			return nil, nil, fmt.Errorf("%s: row %d has %d fields, want %d", path, i+2, len(r), len(all[0]))
		}
	}
	return all[0], all[1:], nil
}

// writeCSV replaces the file atomically (temp file in the same folder, then rename).
func writeCSV(path string, header []string, rows [][]string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".library-*.csv")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	w := csv.NewWriter(tmp)
	if err := w.Write(header); err != nil {
		return err
	}
	if err := w.WriteAll(rows); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func indexOf(header []string) map[string]int {
	m := make(map[string]int, len(header))
	for i, h := range header {
		m[h] = i
	}
	return m
}
