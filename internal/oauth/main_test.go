package oauth

import (
	"log/slog"
	"os"
	"testing"
)

// TestMain silences the bridge's outcome logs so test output stays clean.
func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.DiscardHandler))
	os.Exit(m.Run())
}
