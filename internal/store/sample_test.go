package store

import "testing"

// SampleSessions orders by total raw text size, largest first, and honors the limit.
// Size-desc is what makes `witness lens try` deterministic across prompt edits and
// surfaces the meatiest (most chunk-prone) sessions.
func TestSampleSessionsSizeDescending(t *testing.T) {
	s := tempStore(t)
	// small: ~4 chars; medium: ~40; large: ~400 — deliberately unambiguous ordering.
	mustRaw(t, s, "small", "tiny")
	mustRaw(t, s, "medium", repeat("m", 40))
	mustRaw(t, s, "large", repeat("L", 400))

	got, err := s.SampleSessions(3)
	if err != nil {
		t.Fatalf("SampleSessions: %v", err)
	}
	want := []string{"large", "medium", "small"}
	if len(got) != len(want) {
		t.Fatalf("want %d sessions, got %d (%v)", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order wrong at %d: want %q, got %q (full: %v)", i, want[i], got[i], got)
		}
	}

	// Limit is honored, and the limited set is the LARGEST ones.
	top, err := s.SampleSessions(1)
	if err != nil {
		t.Fatalf("SampleSessions(1): %v", err)
	}
	if len(top) != 1 || top[0] != "large" {
		t.Fatalf("SampleSessions(1) should return the single largest session, got %v", top)
	}
}

// SampleSessions on an empty archive returns an empty slice, not an error.
func TestSampleSessionsEmpty(t *testing.T) {
	s := tempStore(t)
	got, err := s.SampleSessions(5)
	if err != nil {
		t.Fatalf("SampleSessions on empty: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("empty archive should return no sessions, got %v", got)
	}
}

// RawBytes sums a session's text length; unknown session → 0.
func TestRawBytes(t *testing.T) {
	s := tempStore(t)
	mustRaw(t, s, "s1", "hello")  // 5
	mustRaw(t, s, "s1", "world!") // 6 → total 11
	if got := s.RawBytes("s1"); got != 11 {
		t.Fatalf("RawBytes want 11, got %d", got)
	}
	if got := s.RawBytes("nope"); got != 0 {
		t.Fatalf("RawBytes(unknown) want 0, got %d", got)
	}
}

func mustRaw(t *testing.T, s *Store, session, text string) {
	t.Helper()
	if err := s.AppendRaw(RawRecord{Session: session, Seq: s.NextSeq(session), Role: "user", Text: text}); err != nil {
		t.Fatalf("AppendRaw: %v", err)
	}
}

func repeat(ch string, n int) string {
	out := make([]byte, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, ch[0])
	}
	return string(out)
}
