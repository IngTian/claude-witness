package commands

import (
	"strings"
	"testing"
)

// --all means the ENTIRE backlog; combining it with a time bound is contradictory,
// so cmdDistillBackfill rejects it before doing any work. (A bounded backfill is
// just `distill start --since ...`, the background path.)
func TestDistillBackfillRejectsTimeBounds(t *testing.T) {
	for _, tc := range []struct{ since, until string }{
		{"7d", ""},
		{"", "2026-07-01"},
		{"2026-06-01", "2026-07-01"},
	} {
		err := cmdDistillBackfill(true, tc.since, tc.until)
		if err == nil {
			t.Fatalf("--all with since=%q until=%q should error", tc.since, tc.until)
		}
		if !strings.Contains(err.Error(), "cannot be combined") {
			t.Fatalf("unexpected error for since=%q until=%q: %v", tc.since, tc.until, err)
		}
	}
}
