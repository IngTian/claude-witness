//go:build !windows

package commands

// resolveClaudeInstall (Unix) keeps the long-standing behavior: hooks call the
// in-repo witness.sh shim in shell form. The shim locates the per-OS binary,
// exports CLAUDE_PLUGIN_ROOT, sets GOMLX_BACKEND=go, and enforces the recursion
// guard — and it gives a `go run` dev fallback so a fresh checkout works before
// `make build`. Nothing is copied; the install points at the working copy. This
// is deliberately NOT unified with the Windows path (there is no portable shell
// on Windows to run the shim) — see install_windows.go.
func resolveClaudeInstall() (hookInvocation, error) {
	shim, err := repoShim()
	if err != nil {
		return hookInvocation{}, err
	}
	return shellInvocation(shim), nil
}
