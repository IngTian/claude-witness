package opencode_test

// Architecture guard (issue #73-C6): the OpenCode platform adapter must NOT import
// the Claude adapter. They are PEERS — neither is a base package. OpenCode used to
// import claude solely to borrow RenderTranscript; that made Claude a de-facto base
// (a third runtime wanting the standard transcript format would have had to import
// claude too). RenderTranscript now lives in the leaf `internal/platform` package,
// so this test fails if the peer→peer import creeps back.
//
// A static source scan (not a build-time dep check) so the failure names the exact
// file, matching the acceptance_test.go guard style.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenCodeDoesNotImportClaude(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// This test lives in internal/platform/opencode; scan that dir's own .go files.
	const forbidden = "internal/platform/claude"
	err = filepath.Walk(wd, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			// Don't descend into sibling/nested dirs beyond this package's own files.
			if path != wd {
				return filepath.SkipDir
			}
			return nil
		}
		// Only production sources declare the imports we guard; skip _test.go (incl.
		// this file, whose `forbidden` literal would otherwise match itself).
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(src), forbidden) {
			t.Errorf("%s imports %q — the OpenCode adapter must not depend on the Claude adapter (they are peers; use the shared internal/platform helpers instead)", filepath.Base(path), forbidden)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
