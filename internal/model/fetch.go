// Package model acquires the bundled embedding model (multilingual-e5-small
// ONNX + tokenizer) when it is not already present beside the binary.
//
// Why this exists: witness ships as one static Go binary, but the 470MB model
// is too large to carry in every distribution channel. The self-contained
// Windows zip and a from-source clone DO have the model on disk; an npm install
// of the OpenCode plugin (@witness-ai/opencode) ships only the binaries + prompts
// (the model would ~5x the tarball), so on that channel the model is absent until
// fetched. This package fills that gap on demand.
//
// Acquisition policy (matches the industry norm — Playwright's explicit-command +
// HuggingFace's lazy-on-first-use, NOT a postinstall hook, which pnpm/Bun no
// longer run by default): Ensure() is called from two places —
//   - `witness install` (the explicit-command path, for users who run it), and
//   - the worker's lazy embedder init (the lazy-on-first-use path, which is the
//     ONLY path the npm OpenCode user hits, since they never run install).
//
// It writes into the SAME directory embed.assetsDir() reads from (bundle.Dir),
// so a fetched model is found identically to a bundled one. Integrity is a pinned
// SHA-256 per file (the model repo's git-LFS oid), verified before the file is
// renamed into place — stronger than the size-only guard the shell script used,
// per the research on MITM/corruption slipping past a size check.
package model

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// asset is one file we fetch, pinned to an immutable content hash.
type asset struct {
	name   string // filename on disk (also the completion-check target)
	url    string // absolute download URL
	sha256 string // lowercase hex; the git-LFS oid of the pinned revision
	size   int64  // expected byte length (a cheap pre-hash sanity check)
}

// pinnedRevision is the exact HuggingFace commit the hashes below were taken
// from. Pinning the revision (not `main`) makes the download reproducible and
// keeps the SHA-256s valid even if upstream publishes a new model — a floating
// ref would make every hash check fail the day the repo updates.
const pinnedRevision = "614241f622f53c4eeff9890bdc4f31cfecc418b3"

// assets is the model file set. Hashes verified against the repo's git-LFS
// pointers at pinnedRevision (huggingface.co/intfloat/multilingual-e5-small).
var assets = []asset{
	{
		name:   "model.onnx",
		url:    "https://huggingface.co/intfloat/multilingual-e5-small/resolve/" + pinnedRevision + "/onnx/model.onnx",
		sha256: "ca456c06b3a9505ddfd9131408916dd79290368331e7d76bb621f1cba6bc8665",
		size:   470268510,
	},
	{
		name:   "tokenizer.json",
		url:    "https://huggingface.co/intfloat/multilingual-e5-small/resolve/" + pinnedRevision + "/tokenizer.json",
		sha256: "0b44a9d7b51c3c62626640cda0e2c2f70fdacdc25bbbd68038369d14ebdf4c39",
		size:   17082730,
	},
}

// SkipEnv, when set to a truthy value, disables all network fetching — the
// airgapped / metered-connection / bring-your-own-model escape hatch (mirrors
// PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD). Point WITNESS_ASSETS at a pre-placed model
// dir alongside this to run fully offline.
const SkipEnv = "WITNESS_SKIP_MODEL_DOWNLOAD"

// Present reports whether every asset already exists in dir with the right size.
// Cheap (stat only, no hashing) — used to no-op Ensure when the model is bundled.
func Present(dir string) bool {
	for _, a := range assets {
		fi, err := os.Stat(filepath.Join(dir, a.name))
		if err != nil || fi.Size() != a.size {
			return false
		}
	}
	return true
}

// logf is the progress sink. A 470MB download must not be silent (the research's
// "no silent truncation / surprise" rule); callers pass a logger that writes
// where the user will see it (stderr for install, slog for the worker).
type logf func(format string, args ...any)

// Ensure makes the embedding model available in dir, downloading any missing or
// mismatched file. It is idempotent: an already-complete, hash-valid model is a
// no-op. Returns an error only if the model is genuinely unavailable afterward
// (download failed, or skipped-and-absent) so callers can surface a clear remedy.
//
// dir is the resolved assets directory (embed.assetsDir() / bundle.Dir) — the
// SAME place embed.New reads from, so a fetched model is indistinguishable from a
// bundled one and survives across witness upgrades (it's not under node_modules).
func Ensure(dir string, log logf) error {
	if log == nil {
		log = func(string, ...any) {}
	}
	if Present(dir) {
		return nil
	}
	if v := os.Getenv(SkipEnv); truthy(v) {
		return fmt.Errorf("embedding model not present in %s and %s is set; "+
			"place model.onnx + tokenizer.json there or point WITNESS_ASSETS at a copy", dir, SkipEnv)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create model dir: %w", err)
	}
	log("witness: fetching embedding model (~470MB, one time) into %s", dir)
	for _, a := range assets {
		dst := filepath.Join(dir, a.name)
		if fileOK(dst, a) { // already valid (partial set, or a re-run after one file)
			log("witness:   have %s", a.name)
			continue
		}
		if err := fetchOne(a, dst, log); err != nil {
			return fmt.Errorf("fetch %s: %w", a.name, err)
		}
	}
	log("witness: embedding model ready")
	return nil
}

// fetchOne downloads a single asset to a temp file, verifies size + SHA-256, and
// only then renames it into place — so an interrupted or corrupted download never
// leaves a truncated file that a later run would trust as "present" (the atomic
// temp+rename + hash discipline the research flagged as the correct pattern).
func fetchOne(a asset, dst string, log logf) error {
	log("witness:   downloading %s ...", a.name)
	tmp := dst + ".part"
	_ = os.Remove(tmp)

	client := &http.Client{Timeout: 30 * time.Minute}
	resp, err := client.Get(a.url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("http %d from %s", resp.StatusCode, a.url)
	}

	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), resp.Body)
	closeErr := f.Close()
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	if n != a.size {
		_ = os.Remove(tmp)
		return fmt.Errorf("size mismatch: got %d bytes, want %d (incomplete or wrong file)", n, a.size)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != a.sha256 {
		_ = os.Remove(tmp)
		return fmt.Errorf("sha256 mismatch: got %s, want %s (corrupted or tampered download)", got, a.sha256)
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	log("witness:   ok %s (%d bytes)", a.name, n)
	return nil
}

// fileOK reports whether an existing file matches the expected size AND hash.
// Used to skip a file already fetched on a previous (possibly interrupted) run
// without re-downloading it.
func fileOK(path string, a asset) bool {
	fi, err := os.Stat(path)
	if err != nil || fi.Size() != a.size {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false
	}
	return hex.EncodeToString(h.Sum(nil)) == a.sha256
}

func truthy(v string) bool {
	switch v {
	case "1", "true", "TRUE", "True", "yes", "on":
		return true
	}
	return false
}
