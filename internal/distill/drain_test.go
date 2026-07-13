package distill

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"testing"

	"github.com/IngTian/witness/internal/lens"
	"github.com/IngTian/witness/internal/store"
)

// safeMiner is a concurrency-safe fake miner for the parallel-drain tests: it
// records how many times each transcript was mined (guarded), so a race detector
// run catches any accidental shared-state write in the MAP phase.
type safeMiner struct {
	mu    sync.Mutex
	calls int
}

func (m *safeMiner) run(_ context.Context, _, _, input string) (string, error) {
	m.mu.Lock()
	m.calls++
	m.mu.Unlock()
	// Marshal so newlines/quotes in the transcript are escaped (a raw string concat
	// would emit invalid JSON and parse to a "quiet session" — a test-only trap).
	arr := []minedObs{{Dimension: "thinking", Observation: "obs-for:" + input, Evidence: "e", Poignancy: 3}}
	b, _ := json.Marshal(arr)
	return string(b), nil
}

func drainWorker(s *store.Store, run MineFunc) *Worker {
	return &Worker{
		Store:    s,
		Embedder: fakeEmbedder{},
		Lenses:   []*lens.Lens{{Name: "default", Global: true, Extract: "mine", Dimensions: []string{"thinking"}}},
		Config:   store.Config{},
		Run:      run,
	}
}

func TestEffectiveConcurrency(t *testing.T) {
	// Not safe → always 1, whatever the ask.
	if got := EffectiveConcurrency(8, false); got != 1 {
		t.Fatalf("unsafe runner must clamp to 1, got %d", got)
	}
	// Safe, want<1 → floor at 1.
	if got := EffectiveConcurrency(0, true); got != 1 {
		t.Fatalf("want<1 must floor to 1, got %d", got)
	}
	// Safe, small want → honored (GOMAXPROCS on CI is >= 2).
	if got := EffectiveConcurrency(2, true); got != 2 {
		t.Fatalf("safe runner should honor want=2 (GOMAXPROCS>=2), got %d", got)
	}
}

// The drain contract (ported from the old cmd-level drainQueue tests, now that the
// loop lives in the engine): every pending job attempted at most once per run,
// jobs arriving mid-drain picked up, and termination even if a job stays pending.
func TestDrainProcessesArrivalsOnceAndTerminates(t *testing.T) {
	s := newStore(t)
	w := drainWorker(s, (&safeMiner{}).run)
	// Real L0 so mining does work; "stuck" stays in the synthetic pending set forever.
	for _, sess := range []string{"A", "B", "stuck"} {
		capture(t, s, sess, "user", "turn-"+sess)
	}

	pendingSet := map[string]bool{"A": true, "B": true, "stuck": true}
	pending := func() []string {
		out := []string{}
		for k := range pendingSet {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}
	var order []string
	onCommit := func(session string) {
		order = append(order, session)
		if session == "A" {
			pendingSet["C"] = true // a new job arrives mid-drain
		}
		if session != "stuck" {
			delete(pendingSet, session) // normal jobs clear; "stuck" never does
		}
	}

	w.Drain(context.Background(), DrainOpts{Conc: 1, Pending: pending, OnCommit: onCommit})

	counts := map[string]int{}
	for _, sess := range order {
		counts[sess]++
	}
	for _, sess := range []string{"A", "B", "C", "stuck"} {
		if counts[sess] != 1 {
			t.Errorf("%s processed %d times, want exactly 1", sess, counts[sess])
		}
	}
}

func TestDrainStopsAfterBudget(t *testing.T) {
	s := newStore(t)
	w := drainWorker(s, (&safeMiner{}).run)
	for _, sess := range []string{"A", "B", "C"} {
		capture(t, s, sess, "user", "turn-"+sess)
	}
	pendingSet := map[string]bool{"A": true, "B": true, "C": true}
	pending := func() []string {
		out := []string{}
		for k := range pendingSet {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}
	var order []string
	processed := w.Drain(context.Background(), DrainOpts{
		Conc: 1, Max: 1, Pending: pending,
		OnCommit: func(session string) { order = append(order, session); delete(pendingSet, session) },
	})
	if processed != 1 || len(order) != 1 || order[0] != "A" {
		t.Fatalf("processed=%d order=%v, want exactly the first job", processed, order)
	}
	if !pendingSet["B"] || !pendingSet["C"] {
		t.Fatalf("budgeted drain should leave remaining jobs queued: %#v", pendingSet)
	}
}

// The parallel path (Conc>1) must uphold the SAME attempt-once contract and be
// race-free. Run with `go test -race` to exercise the concurrent MAP phase.
func TestDrainParallelAttemptsEachOnce(t *testing.T) {
	s := newStore(t)
	m := &safeMiner{}
	w := drainWorker(s, m.run)
	const n = 12
	sessions := map[string]bool{}
	for i := 0; i < n; i++ {
		id := string(rune('a' + i))
		capture(t, s, id, "user", "turn-"+id)
		sessions[id] = true
	}
	pending := func() []string {
		out := []string{}
		for k := range sessions {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}
	var mu sync.Mutex
	committed := map[string]int{}
	processed := w.Drain(context.Background(), DrainOpts{
		Conc:    4,
		Pending: pending,
		OnCommit: func(session string) {
			mu.Lock()
			committed[session]++
			delete(sessions, session)
			mu.Unlock()
		},
	})
	if processed != n {
		t.Fatalf("expected %d committed, got %d", n, processed)
	}
	for id, c := range committed {
		if c != 1 {
			t.Fatalf("%s committed %d times, want exactly 1", id, c)
		}
	}
	// All sessions distilled: watermark advanced, observations written for each.
	obs, _ := s.ReadObservations("")
	if len(obs) != n {
		t.Fatalf("expected %d observations (one per session), got %d", n, len(obs))
	}
}
