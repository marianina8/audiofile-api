package processor

import (
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var trailingCopy = regexp.MustCompile(`\s*\(\d+\)$`)

// noise phrases removed from titles, longest first.
var noisePhrases = [][]string{
	{"no", "copyright", "music"},
	{"no", "copyright"},
	{"free", "music"},
	{"background", "music"},
	{"music"},
}

// TitleFromFilename makes a best-effort title from an upload filename such as
// "silentvoice-morning-fog-263352.mp3". An empty result becomes "Untitled".
func TitleFromFilename(name string) string {
	name = strings.TrimSuffix(name, filepath.Ext(name))
	name = trailingCopy.ReplaceAllString(name, "")
	raw := strings.FieldsFunc(strings.ToLower(name), func(r rune) bool {
		return r == '-' || r == '_' || r == ' '
	})
	if n := len(raw); n > 0 && allDigits(raw[n-1]) {
		raw = raw[:n-1]
	}
	var toks []string
	for _, t := range raw {
		if t == "musicno" {
			toks = append(toks, "music", "no")
			continue
		}
		toks = append(toks, t)
	}
	for _, ph := range noisePhrases {
		toks = removeRuns(toks, ph)
	}
	for i, t := range toks {
		r, size := utf8.DecodeRuneInString(t)
		toks[i] = string(unicode.ToUpper(r)) + t[size:]
	}
	if len(toks) == 0 {
		return "Untitled"
	}
	return strings.Join(toks, " ")
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

func removeRuns(toks, ph []string) []string {
	var out []string
	for i := 0; i < len(toks); {
		if i+len(ph) <= len(toks) && equal(toks[i:i+len(ph)], ph) {
			i += len(ph)
			continue
		}
		out = append(out, toks[i])
		i++
	}
	return out
}

func equal(a, b []string) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
