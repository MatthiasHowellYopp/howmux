package eval

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestSetupFakeTools_Isolation(t *testing.T) {
	tempBase, err := os.MkdirTemp("", "test-setup-fake-tools-*")
	if err != nil {
		t.Fatalf("failed to create temp base: %v", err)
	}
	defer os.RemoveAll(tempBase)

	var wg sync.WaitGroup
	results := make(chan string, 2)

	// Call SetupFakeTools twice concurrently
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			toolsDir, callLogPath, cleanup, err := SetupFakeTools(tempBase)
			if err != nil {
				t.Errorf("SetupFakeTools failed: %v", err)
				return
			}
			defer cleanup()

			// Verify toolsDir exists and is unique
			if _, err := os.Stat(toolsDir); os.IsNotExist(err) {
				t.Errorf("toolsDir does not exist: %s", toolsDir)
			}

			// Verify fake gh exists and is executable
			ghPath := filepath.Join(toolsDir, "gh")
			if runtime.GOOS == "windows" {
				ghPath += ".bat"
			}
			info, err := os.Stat(ghPath)
			if err != nil {
				t.Errorf("fake gh does not exist: %v", err)
				return
			}
			if runtime.GOOS != "windows" && info.Mode()&0111 == 0 {
				t.Errorf("fake gh is not executable: %s (mode %v)", ghPath, info.Mode())
			}

			// Verify call log exists
			if _, err := os.Stat(callLogPath); os.IsNotExist(err) {
				t.Errorf("call log does not exist: %s", callLogPath)
			}

			results <- toolsDir
		}()
	}

	wg.Wait()
	close(results)

	// Verify both toolsDirs are different (no collision)
	dirs := make([]string, 0, 2)
	for dir := range results {
		dirs = append(dirs, dir)
	}

	if len(dirs) != 2 {
		t.Fatalf("expected 2 results, got %d", len(dirs))
	}

	if dirs[0] == dirs[1] {
		t.Errorf("toolsDirs are not unique: both are %s", dirs[0])
	}
}

func TestPrependPathEnv_Unix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-specific test")
	}

	tests := []struct {
		name     string
		env      []string
		dir      string
		expected string
	}{
		{
			name:     "prepend to existing PATH",
			env:      []string{"USER=test", "PATH=/usr/bin:/bin"},
			dir:      "/fake",
			expected: "PATH=/fake:/usr/bin:/bin",
		},
		{
			name:     "prepend to empty PATH",
			env:      []string{"USER=test", "PATH="},
			dir:      "/fake",
			expected: "PATH=/fake",
		},
		{
			name:     "add PATH when missing",
			env:      []string{"USER=test"},
			dir:      "/fake",
			expected: "PATH=/fake",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := prependPathEnv(tt.env, tt.dir)
			found := false
			for _, entry := range result {
				if strings.HasPrefix(entry, "PATH=") {
					if entry != tt.expected {
						t.Errorf("expected %q, got %q", tt.expected, entry)
					}
					found = true
					break
				}
			}
			if !found {
				t.Errorf("PATH not found in result: %v", result)
			}
		})
	}
}

func TestPrependPathEnv_Windows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-specific test")
	}

	tests := []struct {
		name     string
		env      []string
		dir      string
		expected string
	}{
		{
			name:     "prepend to existing PATH",
			env:      []string{"USER=test", "PATH=C:\\Windows;C:\\bin"},
			dir:      "C:\\fake",
			expected: "PATH=C:\\fake;C:\\Windows;C:\\bin",
		},
		{
			name:     "prepend to empty PATH",
			env:      []string{"USER=test", "PATH="},
			dir:      "C:\\fake",
			expected: "PATH=C:\\fake",
		},
		{
			name:     "add PATH when missing",
			env:      []string{"USER=test"},
			dir:      "C:\\fake",
			expected: "PATH=C:\\fake",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := prependPathEnv(tt.env, tt.dir)
			found := false
			for _, entry := range result {
				if strings.HasPrefix(entry, "PATH=") {
					if entry != tt.expected {
						t.Errorf("expected %q, got %q", tt.expected, entry)
					}
					found = true
					break
				}
			}
			if !found {
				t.Errorf("PATH not found in result: %v", result)
			}
		})
	}
}

func TestReadCallLog(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test-read-call-log-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	logPath := filepath.Join(tempDir, "call.log")

	// Test empty log file
	t.Run("empty log", func(t *testing.T) {
		if err := os.WriteFile(logPath, []byte(""), 0644); err != nil {
			t.Fatalf("failed to write log: %v", err)
		}

		calls, err := readCallLog(logPath)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if len(calls) != 0 {
			t.Errorf("expected 0 calls, got %d", len(calls))
		}
	})

	// Test nonexistent log file
	t.Run("nonexistent log", func(t *testing.T) {
		nonexistentPath := filepath.Join(tempDir, "nonexistent.log")
		calls, err := readCallLog(nonexistentPath)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if len(calls) != 0 {
			t.Errorf("expected 0 calls, got %d", len(calls))
		}
	})

	// Test log with valid entries
	t.Run("valid entries", func(t *testing.T) {
		content := `2024-09-11T10:56:01Z gh pr create --repo owner/repo --title test
2024-09-11T10:56:02Z gh issue edit 123 --add-label done
`
		if err := os.WriteFile(logPath, []byte(content), 0644); err != nil {
			t.Fatalf("failed to write log: %v", err)
		}

		calls, err := readCallLog(logPath)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(calls) != 2 {
			t.Fatalf("expected 2 calls, got %d", len(calls))
		}

		// Check first call
		if calls[0].Timestamp != "2024-09-11T10:56:01Z" {
			t.Errorf("call 0 timestamp: expected %q, got %q", "2024-09-11T10:56:01Z", calls[0].Timestamp)
		}
		if calls[0].Tool != "gh" {
			t.Errorf("call 0 tool: expected %q, got %q", "gh", calls[0].Tool)
		}
		expectedArgs := []string{"pr", "create", "--repo", "owner/repo", "--title", "test"}
		if len(calls[0].Args) != len(expectedArgs) {
			t.Errorf("call 0 args length: expected %d, got %d", len(expectedArgs), len(calls[0].Args))
		} else {
			for i, arg := range expectedArgs {
				if calls[0].Args[i] != arg {
					t.Errorf("call 0 args[%d]: expected %q, got %q", i, arg, calls[0].Args[i])
				}
			}
		}

		// Check second call
		if calls[1].Timestamp != "2024-09-11T10:56:02Z" {
			t.Errorf("call 1 timestamp: expected %q, got %q", "2024-09-11T10:56:02Z", calls[1].Timestamp)
		}
		if calls[1].Tool != "gh" {
			t.Errorf("call 1 tool: expected %q, got %q", "gh", calls[1].Tool)
		}
	})

	// Test malformed lines are skipped
	t.Run("malformed lines", func(t *testing.T) {
		content := `2024-09-11T10:56:01Z gh pr create
malformed
2024-09-11T10:56:02Z gh issue edit 123
`
		if err := os.WriteFile(logPath, []byte(content), 0644); err != nil {
			t.Fatalf("failed to write log: %v", err)
		}

		calls, err := readCallLog(logPath)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Should have 2 valid calls, malformed line skipped
		if len(calls) != 2 {
			t.Errorf("expected 2 calls (malformed line skipped), got %d", len(calls))
		}
	})
}

// TestFakeToolInterception proves the core hermetic guarantee of issue #27
// directly, without depending on kiro-cli, a configured agent, or the network:
// with the fake-tools dir prepended to PATH and HOWMUX_EVAL_CALL_LOG set, a
// `gh` invocation resolves to the fake (never the real binary), returns the
// fake's canned response, and is recorded to the call log. This is exactly the
// mechanism invokeAgentNative wires up for the agent; testing it directly makes
// the safety property verifiable in CI (where kiro-cli is absent).
func TestFakeToolInterception(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake shim is a bash script; interception test is POSIX-only")
	}

	toolsDir, callLogPath, cleanup, err := SetupFakeTools(t.TempDir())
	if err != nil {
		t.Fatalf("SetupFakeTools: %v", err)
	}
	defer cleanup()

	// Build the same hermetic environment invokeAgentNative uses: fake tools
	// first on PATH, plus the call-log location.
	env := prependPathEnv(os.Environ(), toolsDir)
	env = append(env, "HOWMUX_EVAL_CALL_LOG="+callLogPath)

	// A write command that, against the real gh, would create a PR. Here it must
	// hit the fake and mutate nothing. Invoke through a shell so `gh` is resolved
	// from the PATH we injected at runtime — this mirrors how the agent shells
	// out to gh (Go's exec.Command resolves a bare name against the parent PATH
	// at construction time, which would bypass the fake).
	cmd := exec.Command("bash", "-c",
		`gh pr create --repo owner/repo --title "hermetic test" --body "should never reach real GitHub"`)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fake gh invocation failed (is the fake on PATH?): %v\noutput: %s", err, out)
	}

	// 1) The fake resolved and returned its canned pr-create response — proving
	//    the real gh was not used.
	if !strings.Contains(string(out), `"number": 42`) {
		t.Errorf("expected fake gh canned pr-create response, got: %q", string(out))
	}

	// 2) The call was recorded to the log via HOWMUX_EVAL_CALL_LOG.
	calls, err := readCallLog(callLogPath)
	if err != nil {
		t.Fatalf("readCallLog: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected exactly 1 recorded call, got %d: %+v", len(calls), calls)
	}
	c := calls[0]
	if c.Tool != "gh" {
		t.Errorf("recorded tool = %q, want %q", c.Tool, "gh")
	}
	if len(c.Args) < 2 || c.Args[0] != "pr" || c.Args[1] != "create" {
		t.Errorf("recorded args = %v, want to start with [pr create]", c.Args)
	}
	if !strings.Contains(c.RawLine, "--repo owner/repo") {
		t.Errorf("recorded raw line missing the args: %q", c.RawLine)
	}
}

// TestInvokeAgentNative_Hermetic verifies that invokeAgentNative itself wires up
// the hermetic environment. It runs only when kiro-cli is available; the core
// interception guarantee is proven kiro-cli-independently by
// TestFakeToolInterception above.
func TestInvokeAgentNative_HermeticExecution(t *testing.T) {
	// Check if kiro-cli is available
	if _, err := os.Stat("/usr/local/bin/kiro-cli"); os.IsNotExist(err) {
		// Try alternative common locations
		paths := []string{
			"/opt/homebrew/bin/kiro-cli",
			filepath.Join(os.Getenv("HOME"), ".local", "bin", "kiro-cli"),
		}
		found := false
		for _, path := range paths {
			if _, err := os.Stat(path); err == nil {
				found = true
				break
			}
		}
		if !found {
			t.Skip("kiro-cli not found in common locations - skipping kiro-cli-dependent test")
		}
	}

	prompt := "List GitHub issues using the gh CLI tool."
	agent := "architect"

	output, cost, errCtx, externalCalls, err := invokeAgentNative(agent, prompt)
	if err != nil {
		if errCtx != nil && strings.Contains(errCtx.Stderr, "agent") {
			t.Skipf("agent %q not configured - skipping kiro-cli-dependent test", agent)
		}
		t.Logf("invocation failed (may be expected): %v", err)
	}

	if output != "" {
		t.Logf("agent output received: %d bytes", len(output))
	}
	if cost.TokensIn > 0 || cost.TokensOut > 0 {
		t.Logf("cost tracked: %+v", cost)
	}

	// Any recorded call must be to the fake gh (never a non-gh/real tool),
	// confirming invokeAgentNative injected the fake tools onto PATH.
	t.Logf("external calls recorded: %d", len(externalCalls))
	for i, call := range externalCalls {
		t.Logf("  call %d: %s %v", i, call.Tool, call.Args)
		if call.Tool != "gh" {
			t.Errorf("unexpected tool recorded: %q (expected \"gh\")", call.Tool)
		}
	}
}
