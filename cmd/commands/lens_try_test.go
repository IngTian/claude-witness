package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/IngTian/witness/internal/store"
)

// writeLensFile writes a candidate lens file and returns its path.
func writeLensFile(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "cand.md")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// seedRaw opens a store in a fresh WITNESS_HOME, sets the runner, and appends raw for
// one session so the command reaches (or is stopped before) the mining step.
func seedTryStore(t *testing.T, runner string) {
	t.Helper()
	t.Setenv("WITNESS_HOME", t.TempDir())
	s, err := store.Open()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.SetConfigString("runner", runner); err != nil {
		t.Fatalf("SetConfigString runner: %v", err)
	}
	if err := s.AppendRaw(store.RawRecord{Session: "s1", Seq: 0, Role: "user", Text: "hello"}); err != nil {
		t.Fatalf("AppendRaw: %v", err)
	}
	_ = s.Close()
}

const validLensBody = "# name: cand\n## EXTRACT\nmine growth\n## REVIEW\nsynth\n"

// The Claude-only guard: an OpenCode-resolved runner is refused BEFORE any runner is
// opened (so no LLM call, and the OpenCode Close-sweep hazard is never risked).
func TestLensTryRefusesNonClaudeRunner(t *testing.T) {
	seedTryStore(t, store.RunnerOpenCode)
	err := cmdLensTry(writeLensFile(t, validLensBody), 1, "", false)
	if err == nil {
		t.Fatalf("expected `lens try` to refuse a non-claude runner")
	}
	if !strings.Contains(err.Error(), "claude runner") {
		t.Fatalf("error should explain the claude-only restriction, got: %v", err)
	}
}

// A missing EXTRACT section is a usage error surfaced before any runner work.
func TestLensTryRejectsNoExtractFile(t *testing.T) {
	seedTryStore(t, store.RunnerClaude)
	err := cmdLensTry(writeLensFile(t, "# name: x\n## REVIEW\nonly review\n"), 1, "", false)
	if err == nil || !strings.Contains(err.Error(), "EXTRACT") {
		t.Fatalf("expected an EXTRACT-required error, got: %v", err)
	}
}

// A missing file surfaces the read error (not a nil-lens panic).
func TestLensTryMissingFile(t *testing.T) {
	seedTryStore(t, store.RunnerClaude)
	if err := cmdLensTry(filepath.Join(t.TempDir(), "nope.md"), 1, "", false); err == nil {
		t.Fatalf("expected an error for a missing file")
	}
}

// --session validation: an unknown session id is rejected before mining. (Runner is
// claude, but validation happens before the runner opens, so no LLM call is made.)
func TestLensTryRejectsUnknownSession(t *testing.T) {
	seedTryStore(t, store.RunnerClaude)
	err := cmdLensTry(writeLensFile(t, validLensBody), 1, "does-not-exist", false)
	if err == nil || !strings.Contains(err.Error(), "no raw turns") {
		t.Fatalf("expected an unknown-session error, got: %v", err)
	}
}
