package commands

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// The Claude Code capture path rides on a NAME CONTRACT: install writes hook
// commands like `'<shim>' capture` / `session-start` / `session-end`, the shim
// forwards those tokens verbatim to the binary, and the binary must have a cobra
// command of that exact name — otherwise the hook fires, the binary errors with
// "unknown command", and capture silently stops while every unit test stays green
// (the refactor renamed all these commands with zero test tying the two sides).
//
// This locks the contract: every subcommand token install emits, plus the two
// tokens spawned internally (`worker` via spawnDetached, `mcp` via the MCP
// registration), must resolve to a registered command on the real root.
func TestHookCommandTokensAreRegisteredCommands(t *testing.T) {
	root := newRootCmd()

	// Run the contract for BOTH invocation forms. Shell form (Unix) carries the
	// token as the trailing word of the command string; exec form (Windows)
	// carries it in Args. Either way the token must be a real cobra command.
	for _, tc := range []struct {
		name string
		inv  hookInvocation
	}{
		{"shell form (unix shim)", shellInvocation("/repo/hooks/witness.sh")},
		{"exec form (windows exe)", execInvocation(`C:\witness\witness.exe`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			emitted := map[string]bool{}
			for _, spec := range witnessHookSpecs(tc.inv) {
				for _, h := range spec.Entry.Hooks {
					emitted[hookToken(t, h)] = true
				}
			}
			// Sanity: install must emit the three hook entry points we expect. If the
			// hook wiring changes, update this list deliberately (that's the point).
			for _, want := range []string{"session-start", "capture", "session-end"} {
				if !emitted[want] {
					t.Errorf("witnessHookSpecs no longer emits %q — hook wiring changed", want)
				}
			}
			// Every emitted token, plus the tokens the binary spawns for itself
			// (`worker` via spawnDetached, `mcp` via MCP registration), must resolve.
			tokens := map[string]bool{"worker": true, "mcp": true}
			for tok := range emitted {
				tokens[tok] = true
			}
			for tok := range tokens {
				assertRegistered(t, root, tok)
			}
		})
	}
}

// hookToken extracts the subcommand token from an emitted hook, handling both
// forms: exec form (Windows) puts it in Args; shell form (Unix) puts it as the
// trailing word of the `'<shim>' <token>` command string.
func hookToken(t *testing.T, h hookCmd) string {
	t.Helper()
	if len(h.Args) > 0 {
		return h.Args[len(h.Args)-1]
	}
	fields := strings.Fields(h.Command)
	if len(fields) < 2 {
		t.Fatalf("hook command %q has no subcommand token", h.Command)
	}
	return fields[len(fields)-1]
}

// assertRegistered fails if cobra's root cannot resolve name to a real command.
func assertRegistered(t *testing.T, root *cobra.Command, name string) {
	t.Helper()
	cmd, _, err := root.Find([]string{name})
	if err != nil {
		t.Errorf("hook token %q does not resolve to a registered command: %v", name, err)
		return
	}
	if cmd == nil || cmd.Name() != name {
		got := "<nil>"
		if cmd != nil {
			got = cmd.Name()
		}
		t.Errorf("hook token %q resolved to %q, not a command of that name (Find fell back to root)", name, got)
	}
}
