package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// ReservedLensName reports whether a lens name is reserved and may not be taken by
// a registered lens. Two names are reserved (both defined in types.go, the single
// source of truth):
//   - LensDefault ("default") — the always-on built-in lens's identity. A second
//     lens under this name would share the built-in's (session,'default') watermark
//     and observation key, corrupting the backbone lens's data (two prompts writing
//     Lens='default', one progress row, cross-contaminated dedup).
//   - ProfileUnified ("unified") — the cross-lens profile summary's filename stem; a
//     per-lens summary under this name would clobber the unified portrait.
//
// This is the ONE piece of legitimate default-lens specialness that lives at the
// identity layer: default is not treated differently by the engine (every lens is
// just a prompt + a name), but its name is protected so no registered lens can
// impersonate it. The check is on the sanitized name (registry filesystem key),
// case-FOLDED: the reserved identities collide with the built-ins on the case-
// insensitive filesystems witness's primary platforms use (macOS APFS, Windows
// NTFS), where profile/Default.md and profile/default.md are the SAME file. A case-
// sensitive check would let `register Default` through, and its per-lens summary
// would then silently clobber the built-in's profile — exactly the impersonation
// this guard exists to prevent. Folding closes that bypass on every platform.
func ReservedLensName(name string) bool {
	n := strings.ToLower(sanitize(name))
	return n == ProfileUnified || n == LensDefault
}

// LensesDir is the central lens registry: <root>/lenses/<name>/ (each a directory of
// lens.json + extract.md + review.md, issue #75). Lenses live here (not in repos) so
// the same definition is shared across all sessions.
func (s *Store) LensesDir() string { return filepath.Join(s.Root, "lenses") }

// lensFileNames are the on-disk files of a lens directory. Duplicated from
// internal/lens (which the store must not import — store is the bottom of the stack)
// as small string literals; keep them in sync. lensConfigFile is the presence probe
// for RegisteredLenses.
const (
	lensConfigFile  = "lens.json"
	lensExtractFile = "extract.md"
	lensReviewFile  = "review.md"
)

// RegisterLens copies a lens definition DIRECTORY into the registry under `name`,
// creating/overwriting <root>/lenses/<name>/ with the source's lens.json (optional),
// extract.md (required — the mining prompt), and review.md (optional). srcDir is the
// user's authored directory (issue #75: a lens is a directory, not one parsed file);
// only the three known files are copied, so stray files in the source dir are ignored.
//
// It is lossless under SELF-REGISTER (srcDir == the registry dir, i.e. the user edited
// the registered copy in place and re-registered it): ALL source files are read into
// memory BEFORE anything is removed, so the wipe can't delete a not-yet-read source
// file. And it stages into a sibling .tmp dir then atomically renames into place, so a
// concurrent worker read never sees a half-built lens directory.
func (s *Store) RegisterLens(name, srcDir string) error {
	if ReservedLensName(name) {
		return fmt.Errorf("lens name %q is reserved (the always-on built-in lens or the cross-lens summary); choose another name", name)
	}
	info, err := os.Stat(srcDir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("lens source %q must be a directory holding %s + %s (+ optional %s); the single-file lens format was replaced (issue #75)", srcDir, lensExtractFile, lensReviewFile, lensConfigFile)
	}
	// Read EVERY source file into memory up front — before any destination mutation — so
	// a self-register (srcDir == dest) can't lose review.md/lens.json to the wipe below.
	extract, err := os.ReadFile(filepath.Join(srcDir, lensExtractFile))
	if err != nil {
		return fmt.Errorf("lens source is missing %s (the mining prompt): %w", lensExtractFile, err)
	}
	if strings.TrimSpace(string(extract)) == "" {
		return fmt.Errorf("lens source %s is empty (the mining prompt is required)", lensExtractFile)
	}
	files := map[string][]byte{lensExtractFile: extract}
	for _, fn := range []string{lensReviewFile, lensConfigFile} { // both optional
		if data, rerr := os.ReadFile(filepath.Join(srcDir, fn)); rerr == nil {
			files[fn] = data
		} else if !os.IsNotExist(rerr) {
			return fmt.Errorf("read %s: %w", fn, rerr)
		}
	}
	// Stage into a sibling .tmp dir, fully build it, then swap. Removing the target
	// immediately before the rename means Rename lands on a non-existent path (atomic on
	// POSIX and Windows), so a concurrent reader sees either the old dir or the new one —
	// never a half-built one.
	dir := filepath.Join(s.LensesDir(), sanitize(name))
	tmp := dir + ".tmp"
	if err := os.RemoveAll(tmp); err != nil {
		return err
	}
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		return err
	}
	for fn, data := range files {
		if err := os.WriteFile(filepath.Join(tmp, fn), data, 0o600); err != nil {
			_ = os.RemoveAll(tmp)
			return err
		}
	}
	if err := os.RemoveAll(dir); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	if err := os.Rename(tmp, dir); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	return nil
}

// DeregisterLens removes a lens definition from the registry (no-op if absent).
// (It does not touch config; disable the lens separately if it was enabled.)
func (s *Store) DeregisterLens(name string) error {
	return os.RemoveAll(filepath.Join(s.LensesDir(), sanitize(name)))
}

// RegisteredLenses lists the names of lenses in the registry (dirs holding an
// extract.md — the one required file, so the presence probe never misses a lens that
// simply has no lens.json or review.md).
func (s *Store) RegisteredLenses() []string {
	entries, err := os.ReadDir(s.LensesDir())
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(s.LensesDir(), e.Name(), lensExtractFile)); err == nil {
			names = append(names, e.Name())
		}
	}
	return names
}

// LegacyFormatLenses lists registry directories that hold the OLD single-file lens.md
// (pre-#75) but NOT the new extract.md — i.e. lenses that a pre-#75 install registered
// and that upgraded silently out of RegisteredLenses() (which now probes extract.md). A
// user (especially one who had an ENABLED old-format lens) needs a loud, actionable
// pointer to re-register, since we deliberately do NOT auto-migrate (parsing the old
// ## EXTRACT/## REVIEW split is exactly what #75 removed). Returns nil when there are
// none (the overwhelmingly common case), so callers can cheaply gate their warning.
func (s *Store) LegacyFormatLenses() []string {
	entries, err := os.ReadDir(s.LensesDir())
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(s.LensesDir(), e.Name())
		_, newErr := os.Stat(filepath.Join(dir, lensExtractFile))
		_, oldErr := os.Stat(filepath.Join(dir, "lens.md"))
		if os.IsNotExist(newErr) && oldErr == nil {
			names = append(names, e.Name())
		}
	}
	return names
}

// SetLensModel updates a registered lens's per-lens model in its lens.json (issue #75),
// creating the file if absent. phase selects the field: "extract" → extract_model,
// "review" → review_model. An empty value CLEARS the field (the lens then rides the
// global stage model). This is the safe struct round-trip that replaced hand-editing
// header directives: read → set one field → marshal → atomic write, so no text surgery
// can corrupt the file. It does NOT touch extract.md/review.md.
func (s *Store) SetLensModel(name, phase, model string) error {
	if !slices.Contains(s.RegisteredLenses(), name) {
		return fmt.Errorf("lens %q is not registered (run: witness lens register %s <dir>)", name, name)
	}
	dir := filepath.Join(s.LensesDir(), sanitize(name))
	path := filepath.Join(dir, lensConfigFile)
	// Read-modify-write the existing lens.json (preserving other fields); an absent file
	// starts from an empty config.
	var raw map[string]any
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &raw); err != nil {
			return fmt.Errorf("parse %s: %w", lensConfigFile, err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if raw == nil {
		raw = map[string]any{}
	}
	var field string
	switch phase {
	case "extract":
		field = "extract_model"
	case "review":
		field = "review_model"
	default:
		return fmt.Errorf("unknown lens model phase %q (want extract|review)", phase)
	}
	if strings.TrimSpace(model) == "" {
		delete(raw, field) // clear → ride the global stage model
	} else {
		raw[field] = strings.TrimSpace(model)
	}
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	return writeAtomic(path, out)
}
