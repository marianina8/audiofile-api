// Command draftlibrary writes a draft library CSV from Pixabay filenames. It
// is run by scripts/draft-library.sh, which refuses to overwrite an existing
// CSV. Local tooling only.
package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/marianina8/audiofile-api/internal/processor"
)

// Header is the CSV header shared with cmd/genreguess and scripts/seed.sh.
var Header = []string{"filename", "title", "artist", "album", "genre", "genre_source", "year"}

// uploaderOverrides maps multi-word uploader prefixes to display names.
var uploaderOverrides = []struct{ prefix, name string }{
	{"cinematic-soul", "Cinematic Soul"},
	{"alex-morgan", "Alex Morgan"},
	{"tazdev_music", "Tazdev Music"},
	{"freesoundserver", "FreeSoundServer"},
	{"xhowiexo-madewithlove", "Xhowiexo"}, // "madewithlove" is a label, not part of the title
}

// genreKeywords are checked in order against the title words (not the artist).
// drop marks keywords whose words are removed from the title because they only
// describe the genre (piano stays: "Abstract Piano" is a real title).
var genreKeywords = []struct {
	keyword, genre string
	drop           bool
}{
	{"electro-industrial", "Industrial", true},
	{"rock", "Rock", true},
	{"piano", "Classical", false},
	{"cinematic", "Cinematic", false},
	{"upbeat", "Pop", true},
}

// cleanTitle drops the words of genre-setting keywords and collapses
// consecutive duplicate words. It returns "" when nothing is left.
func cleanTitle(title string) string {
	drop := map[string]bool{}
	for _, k := range genreKeywords {
		if k.drop {
			for _, w := range strings.Split(k.keyword, "-") {
				drop[w] = true
			}
		}
	}
	var out []string
	for _, w := range strings.Fields(title) {
		lw := strings.ToLower(w)
		if drop[lw] || (len(out) > 0 && strings.ToLower(out[len(out)-1]) == lw) {
			continue
		}
		out = append(out, w)
	}
	return strings.Join(out, " ")
}

// splitName returns the uploader display name and the rest of the filename
// stem (without the uploader prefix).
func splitName(stem string) (artist, rest string) {
	lower := strings.ToLower(stem)
	for _, o := range uploaderOverrides {
		if lower == o.prefix || strings.HasPrefix(lower, o.prefix+"-") {
			return o.name, strings.TrimPrefix(stem[len(o.prefix):], "-")
		}
	}
	first, rest, _ := strings.Cut(stem, "-")
	if first == "" {
		return "Unknown", stem
	}
	return strings.ToUpper(first[:1]) + first[1:], rest
}

// genreFor returns a genre from filename keywords, or "".
func genreFor(rest string) string {
	r := strings.ToLower(rest)
	for _, k := range genreKeywords {
		if strings.Contains(r, k.keyword) {
			return k.genre
		}
	}
	return ""
}

// draft builds the CSV rows for the given filenames (sorted by the caller).
func draft(files []string) [][]string {
	rows := make([][]string, 0, len(files))
	n := map[string]int{}
	for _, f := range files {
		stem := strings.TrimSuffix(f, filepath.Ext(f))
		artist, rest := splitName(stem)
		title := ""
		if rest != "" {
			// Reuse the shared token rules (filler phrases, trailing id and
			// "(1)" copy suffix) on the filename minus the uploader.
			title = cleanTitle(processor.TitleFromFilename(rest + ".mp3"))
		}
		if title == "" || title == "Untitled" {
			n[artist]++
			title = fmt.Sprintf("%s Track %d", artist, n[artist])
		}
		genre, src := genreFor(rest), ""
		if genre != "" {
			src = "filename"
		}
		rows = append(rows, []string{f, title, artist, "", genre, src, ""})
	}
	return rows
}

func main() {
	home, _ := os.UserHomeDir()
	dir := flag.String("dir", filepath.Join(home, "audio"), "folder with the mp3 files")
	toJSON := flag.String("to-json", "", "print this library CSV as a JSON array of objects and exit (used by scripts/seed.sh)")
	flag.Parse()
	if *toJSON != "" {
		if err := csvToJSON(*toJSON, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "draftlibrary:", err)
			os.Exit(1)
		}
		return
	}
	matches, err := filepath.Glob(filepath.Join(*dir, "*.mp3"))
	if err != nil || len(matches) == 0 {
		fmt.Fprintln(os.Stderr, "draftlibrary: no mp3 files in", *dir)
		os.Exit(1)
	}
	files := make([]string, len(matches))
	for i, m := range matches {
		files[i] = filepath.Base(m)
	}
	sort.Strings(files)
	if err := writeCSV(os.Stdout, draft(files)); err != nil {
		fmt.Fprintln(os.Stderr, "draftlibrary:", err)
		os.Exit(1)
	}
}

// csvToJSON prints a library CSV as a JSON array of {header: value} objects.
func csvToJSON(path string, w io.Writer) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	all, err := csv.NewReader(f).ReadAll()
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	out := []map[string]string{}
	for i, r := range all {
		if i == 0 {
			continue
		}
		if len(r) != len(all[0]) {
			return fmt.Errorf("%s: row %d has %d fields, want %d", path, i+1, len(r), len(all[0]))
		}
		m := make(map[string]string, len(r))
		for j, h := range all[0] {
			m[h] = r[j]
		}
		out = append(out, m)
	}
	return json.NewEncoder(w).Encode(out)
}

// writeCSV writes the header and rows with RFC 4180 quoting.
func writeCSV(out io.Writer, rows [][]string) error {
	w := csv.NewWriter(out)
	_ = w.Write(Header)
	_ = w.WriteAll(rows)
	return w.Error()
}
