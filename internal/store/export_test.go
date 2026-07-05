package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

// seedRow writes one raw row so an export has content + a live WAL to consolidate.
func seedRow(t *testing.T, st *Store) {
	t.Helper()
	if err := st.AppendRaw(RawRecord{
		TS: "2026-01-01T00:00:00Z", Session: "s1", Seq: 1, Role: "user", Text: "hello",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

// TestExportProducesConsistentSnapshot is the core contract: export writes a
// single plain .db (no -wal/-shm), it is a valid witness.db that opens and
// contains the data, and it can be produced while the worker would be writing.
func TestExportProducesConsistentSnapshot(t *testing.T) {
	t.Setenv("WITNESS_HOME", t.TempDir())
	st, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	seedRow(t, st)

	dst := filepath.Join(t.TempDir(), "snap.db")
	if err := st.Export(dst, false); err != nil {
		t.Fatalf("export: %v", err)
	}

	// Single consistent file: no WAL/SHM sidecars beside the snapshot.
	for _, sfx := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(dst + sfx); err == nil {
			t.Errorf("snapshot has %s sidecar; not a clean single-file export", sfx)
		}
	}
	// Snapshot is 0600 (private growth data).
	if fi, err := os.Stat(dst); err != nil {
		t.Fatalf("stat snapshot: %v", err)
	} else if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("snapshot perms = %o, want 600", perm)
	}

	// The snapshot is a valid witness.db containing the seeded row.
	db, err := sql.Open("sqlite", dst)
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow("SELECT count(*) FROM raw WHERE session='s1'").Scan(&n); err != nil {
		t.Fatalf("query snapshot: %v", err)
	}
	if n != 1 {
		t.Errorf("snapshot row count = %d, want 1 (data missing from export)", n)
	}
}

// TestExportRefusesOverwriteWithoutForce guards against silently clobbering a
// prior backup; --force must remove-then-write.
func TestExportRefusesOverwriteWithoutForce(t *testing.T) {
	t.Setenv("WITNESS_HOME", t.TempDir())
	st, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	seedRow(t, st)

	dst := filepath.Join(t.TempDir(), "snap.db")
	if err := st.Export(dst, false); err != nil {
		t.Fatalf("first export: %v", err)
	}
	// Second export without force must fail and leave the existing file intact.
	if err := st.Export(dst, false); err == nil {
		t.Fatal("export over an existing file without --force should error")
	}
	// With force it succeeds.
	if err := st.Export(dst, true); err != nil {
		t.Fatalf("export --force over existing: %v", err)
	}
}

// TestExportEmptyPath rejects a blank destination.
func TestExportEmptyPath(t *testing.T) {
	t.Setenv("WITNESS_HOME", t.TempDir())
	st, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Export("", false); err == nil {
		t.Fatal("export with empty path should error")
	}
}
