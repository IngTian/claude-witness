package distill

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/IngTian/witness/internal/store"
)

func TestNewRunnerResolves(t *testing.T) {
	if _, err := NewRunner(store.Config{Runner: "bogus"}); err == nil {
		t.Fatal("unknown runner must fail closed")
	}
	for _, name := range []string{"", "claude", "opencode"} {
		r, err := NewRunner(store.Config{Runner: name})
		if err != nil {
			t.Fatalf("runner %q: %v", name, err)
		}
		// Close must tolerate a runner that was never Opened (no work this drain).
		if err := r.Close(); err != nil {
			t.Fatalf("Close on unopened %q runner: %v", name, err)
		}
	}
}

// TestOpenCodeRunnerCloseSweepsDistillSessions is the regression guard for the
// review.go leak: the manual review path used to defer only the server's Close()
// and never the self-traffic cleanup sweep the worker did, leaking witness-distill
// sessions back into the pending queue. Now the sweep lives in Runner.Close(), so
// EVERY caller (worker AND review) gets it. We prove Close() runs the sweep by
// pointing WITNESS_OPENCODE_DB at a temp OpenCode DB holding a witness-distill
// session and asserting Close() removes it. A zero-value server stands in for an
// opened one (its cmd is nil, so Close() short-circuits the process teardown and
// proceeds to the sweep) — the sweep is what this test exercises.
func TestOpenCodeRunnerCloseSweepsDistillSessions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		CREATE TABLE session (id text PRIMARY KEY, title text NOT NULL, agent text, time_created integer NOT NULL);
		INSERT INTO session VALUES ('distill1', 'witness-distill', 'witness-distill', 1000);
		INSERT INTO session VALUES ('userwork', 'real work', 'build', 1000);
	`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	t.Setenv("WITNESS_OPENCODE_DB", path)

	// server non-nil (zero value) so Close() gets past the "never opened" guard and
	// runs the sweep; cmd==nil makes the server teardown a no-op.
	r := &openCodeRunner{cfg: store.Config{Runner: "opencode"}, server: &OpenCodeServer{}}
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var distillLeft, userLeft int
	_ = db.QueryRow(`SELECT COUNT(*) FROM session WHERE id = 'distill1'`).Scan(&distillLeft)
	_ = db.QueryRow(`SELECT COUNT(*) FROM session WHERE id = 'userwork'`).Scan(&userLeft)
	if distillLeft != 0 {
		t.Fatalf("Close did not sweep the witness-distill session (leak regressed): %d left", distillLeft)
	}
	if userLeft != 1 {
		t.Fatalf("Close wrongly removed real user work: %d left, want 1", userLeft)
	}
}
