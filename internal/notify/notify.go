// Package notify delivers best-effort macOS system notifications via
// osascript. It has exactly one entry point, Notify, intended to inform the
// user that a background task (e.g. a PR review) has completed.
package notify

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// execCommand is an injectable seam over exec.Command, mirroring the
// runReviewToolFunc/fetchDiffFunc pattern in internal/review/runner.go, so
// tests can substitute a fake implementation instead of shelling out for
// real.
var execCommand = exec.Command

// goos is an injectable seam over runtime.GOOS, so tests can force both the
// darwin and non-darwin branches of Notify without build tags or actually
// running on different platforms.
var goos = runtime.GOOS

// escapeAppleScriptString escapes backslashes and double quotes so that s
// can be safely interpolated into a double-quoted AppleScript string
// literal.
//
// This is required because Notify's inputs (a repo owner/name and PR number,
// see internal/review/runner.go) are not fully trusted: a crafted or unusual
// GitHub repository name is attacker-adjacent input that ends up inside a
// shell-invoked osascript -e string. Without escaping, an embedded `"` could
// close the AppleScript string early and an embedded `\` could alter the
// meaning of the characters that follow, letting a malicious value break out
// of the "display notification" string literal and potentially inject
// additional AppleScript. Escaping backslashes first, then quotes, prevents
// a crafted input from turning an escaped quote into an unescaped one.
func escapeAppleScriptString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}

// Notify displays a macOS system notification with the given title and
// message via `osascript -e 'display notification ... with title ...'`.
//
// Platform scope: macOS is the only supported target today. If goos is not
// "darwin", Notify returns nil immediately — this is a documented,
// intentional no-op, not an error, so callers on other platforms need no
// special-casing.
//
// Concurrency: Notify runs synchronously. It does not spawn its own
// goroutine, even though the underlying osascript subprocess call may take
// a nontrivial amount of time. If a caller needs non-blocking behavior (for
// example, to avoid adding latency to a request-handling code path), it is
// the caller's responsibility to invoke Notify inside its own `go func() {
// ... }()`.
//
// Notify never panics. A failure to execute osascript is wrapped and
// returned as an error.
func Notify(title, message string) error {
	if goos != "darwin" {
		return nil
	}

	script := fmt.Sprintf(
		`display notification "%s" with title "%s"`,
		escapeAppleScriptString(message),
		escapeAppleScriptString(title),
	)

	cmd := execCommand("osascript", "-e", script)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("notify: osascript failed: %w", err)
	}
	return nil
}
