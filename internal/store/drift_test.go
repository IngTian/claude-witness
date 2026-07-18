package store

import "testing"

// SetDrift persists a per-(session,lens) drift stamp that survives (it's a real
// column, not in-memory), upserts when no progress row exists yet, and is scoped to
// exactly the (session,lens) pair — a sibling lens on the same session is untouched.
func TestSetDriftPersistsPerLens(t *testing.T) {
	s := tempStore(t)

	// No row yet → no drift.
	if got := s.DriftAt("s", LensDefault); got != "" {
		t.Fatalf("absent pair: want '', got %q", got)
	}
	// SetDrift on a pair with no progress row must create one (upsert), not silently no-op.
	if err := s.SetDrift("s", LensDefault); err != nil {
		t.Fatalf("SetDrift: %v", err)
	}
	if got := s.DriftAt("s", LensDefault); got == "" {
		t.Fatal("SetDrift must stamp a drift even when no progress row existed yet")
	}
	// A drift-only row reads distilled=0 — identical to absent, so it never falsely
	// marks turns as mined.
	if got := s.DistilledCount("s", LensDefault); got != 0 {
		t.Fatalf("a drift-only row must leave distilled at 0, got %d", got)
	}
	// A sibling lens on the same session is independent.
	if got := s.DriftAt("s", "codereview"); got != "" {
		t.Fatalf("a sibling lens must not inherit a drift stamp, got %q", got)
	}
}

// A clean re-mine clears a stale drift: ResetRetry (called on every non-failed lens in
// the commit path) blanks drift_at, so a session that drifted once but later re-mined
// successfully stops counting as drifted. This is the recovery half of #69 Part 2.
func TestResetRetryClearsDrift(t *testing.T) {
	s := tempStore(t)
	if err := s.SetDrift("s", LensDefault); err != nil {
		t.Fatalf("SetDrift: %v", err)
	}
	if got := s.DriftAt("s", LensDefault); got == "" {
		t.Fatal("precondition: drift should be stamped")
	}
	s.ResetRetry("s", LensDefault)
	if got := s.DriftAt("s", LensDefault); got != "" {
		t.Fatalf("ResetRetry must clear a stale drift stamp, got %q", got)
	}
}

// Stats.Drifted counts DISTINCT sessions currently sitting at a drift (any lens),
// independent of the active-lens set, and drops as sessions recover.
func TestStatsDriftedCount(t *testing.T) {
	s := tempStore(t)
	if st := s.Stats(nil); st.Drifted != 0 {
		t.Fatalf("no drift yet: want 0, got %d", st.Drifted)
	}

	// Two lenses drift on one session, one lens drifts on another → 2 distinct sessions.
	if err := s.SetDrift("s1", LensDefault); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDrift("s1", "codereview"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDrift("s2", LensDefault); err != nil {
		t.Fatal(err)
	}
	if st := s.Stats(nil); st.Drifted != 2 {
		t.Fatalf("two distinct drifted sessions expected, got %d", st.Drifted)
	}

	// Clearing ONE of s1's two lenses must not un-count s1 (its other lens still drifts).
	s.ResetRetry("s1", LensDefault)
	if st := s.Stats(nil); st.Drifted != 2 {
		t.Fatalf("s1 must still count while its codereview lens drifts, got %d", st.Drifted)
	}
	// Clearing s1's remaining drifted lens drops it; s2 remains.
	s.ResetRetry("s1", "codereview")
	if st := s.Stats(nil); st.Drifted != 1 {
		t.Fatalf("only s2 should remain drifted, got %d", st.Drifted)
	}
	// Drifted is NOT gated on the active-lens set: even with an unrelated active lens,
	// a persisted drift on 'default' still counts (a drift is a recorded past fact).
	if st := s.Stats([]string{"someOtherLens"}); st.Drifted != 1 {
		t.Fatalf("Drifted must not be scoped to active lenses, got %d", st.Drifted)
	}
}
