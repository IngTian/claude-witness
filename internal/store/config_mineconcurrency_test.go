package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMineConcurrencyParse(t *testing.T) {
	if DefaultConfig().MineConcurrency != DefaultMineConcurrency {
		t.Fatalf("default MineConcurrency=%d want %d", DefaultConfig().MineConcurrency, DefaultMineConcurrency)
	}
	dir := t.TempDir()
	t.Setenv("WITNESS_HOME", dir)
	s, _ := Open()
	defer s.Close()
	// EnsureConfigFile template should carry the knob at the default.
	if got := s.LoadConfig().MineConcurrency; got != DefaultMineConcurrency {
		t.Fatalf("template config MineConcurrency=%d want %d", got, DefaultMineConcurrency)
	}
	// Override + <=0 restores default.
	for _, tc := range []struct {
		line string
		want int
	}{
		{"mine_concurrency = 8", 8},
		{"mine_concurrency = 0", DefaultMineConcurrency},
		{"mine_concurrency = -3", DefaultMineConcurrency},
	} {
		os.WriteFile(filepath.Join(dir, "config.toml"), []byte(tc.line+"\n"), 0o600)
		if got := s.LoadConfig().MineConcurrency; got != tc.want {
			t.Errorf("%q -> MineConcurrency=%d want %d", tc.line, got, tc.want)
		}
	}
}
