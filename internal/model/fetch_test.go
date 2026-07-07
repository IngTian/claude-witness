package model

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// withTestAssets swaps the package-level `assets` for a tiny in-memory set served
// by a local httptest server, so tests never touch the network or the real 470MB
// model. Returns the dir a caller should Ensure into and the raw bytes by name.
func withTestAssets(t *testing.T) (dir string, bodies map[string][]byte) {
	t.Helper()
	bodies = map[string][]byte{
		"model.onnx":     []byte("fake-model-bytes"),
		"tokenizer.json": []byte("fake-tokenizer"),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := filepath.Base(r.URL.Path)
		b, ok := bodies[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(srv.Close)

	sum := func(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
	saved := assets
	t.Cleanup(func() { assets = saved })
	assets = []asset{
		{name: "model.onnx", url: srv.URL + "/model.onnx", sha256: sum(bodies["model.onnx"]), size: int64(len(bodies["model.onnx"]))},
		{name: "tokenizer.json", url: srv.URL + "/tokenizer.json", sha256: sum(bodies["tokenizer.json"]), size: int64(len(bodies["tokenizer.json"]))},
	}
	return t.TempDir(), bodies
}

func TestEnsureDownloadsAndIsIdempotent(t *testing.T) {
	dir, bodies := withTestAssets(t)

	if Present(dir) {
		t.Fatal("Present should be false before any download")
	}
	if err := Ensure(dir, nil); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if !Present(dir) {
		t.Fatal("Present should be true after Ensure")
	}
	for name, want := range bodies {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if string(got) != string(want) {
			t.Errorf("%s: content mismatch", name)
		}
	}
	// No .part temp files must survive a successful fetch (atomic rename).
	for _, a := range assets {
		if _, err := os.Stat(filepath.Join(dir, a.name+".part")); !os.IsNotExist(err) {
			t.Errorf("%s.part should not exist after success", a.name)
		}
	}
	// Second call is a no-op (idempotent) — must not error.
	if err := Ensure(dir, nil); err != nil {
		t.Fatalf("second Ensure should be a no-op, got: %v", err)
	}
}

func TestEnsureRejectsHashMismatch(t *testing.T) {
	dir, _ := withTestAssets(t)
	// Corrupt the pinned hash so the verified bytes no longer match.
	assets[0].sha256 = "0000000000000000000000000000000000000000000000000000000000000000"

	err := Ensure(dir, nil)
	if err == nil {
		t.Fatal("Ensure must fail on sha256 mismatch")
	}
	// The mismatched file must NOT be left on disk (no .part, no final file), so a
	// later run re-fetches rather than trusting a corrupted download.
	if _, statErr := os.Stat(filepath.Join(dir, assets[0].name)); !os.IsNotExist(statErr) {
		t.Error("corrupted file must not be renamed into place")
	}
	if _, statErr := os.Stat(filepath.Join(dir, assets[0].name+".part")); !os.IsNotExist(statErr) {
		t.Error(".part must be cleaned up on hash mismatch")
	}
}

func TestEnsureSkipEnvBlocksDownloadWhenAbsent(t *testing.T) {
	dir, _ := withTestAssets(t)
	t.Setenv(SkipEnv, "1")

	err := Ensure(dir, nil)
	if err == nil {
		t.Fatalf("Ensure must error when %s is set and the model is absent", SkipEnv)
	}
	if Present(dir) {
		t.Fatal("nothing should have been downloaded with skip set")
	}
}

func TestEnsureSkipEnvNoopWhenPresent(t *testing.T) {
	dir, bodies := withTestAssets(t)
	// Pre-place valid files, then set skip: Ensure must succeed without any fetch.
	for name, b := range bodies {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv(SkipEnv, "1")
	if err := Ensure(dir, nil); err != nil {
		t.Fatalf("Ensure should no-op (model present) even with skip set: %v", err)
	}
}

func TestEnsureReFetchesTruncatedFile(t *testing.T) {
	dir, bodies := withTestAssets(t)
	// Simulate a truncated/garbage leftover of the right NAME but wrong content.
	if err := os.WriteFile(filepath.Join(dir, "model.onnx"), []byte("truncated"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, nil); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "model.onnx"))
	if string(got) != string(bodies["model.onnx"]) {
		t.Error("truncated file should have been re-fetched to the correct content")
	}
}
