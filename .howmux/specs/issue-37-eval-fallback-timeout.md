# Design Specification: Eval Fallback Detection + Timeout Adjustment

**Issue:** #37  
**Title:** Eval: fail loud on agent-resolution fallback + raise default invocation timeout  
**Closes:** #37

## Problem Statement

The eval system has two defects that make scores untrustworthy for measuring prompt improvements:

1. **Silent Fallback Scoring**: When `kiro-cli --agent <name>` cannot resolve an agent, it prints an error to stderr (`no agent with name <name> found`), falls back to a default client, and exits 0. The runner treats exit 0 as success and scores the fallback's output as if it were the target agent, producing meaningless scores instead of reporting an error.

2. **Inadequate Default Timeout**: The current 2-minute timeout (`internal/eval/runner.go:821`) is too low for real agents. Architect agents take ~2-3.5 minutes per case (explore codebase → write full spec), so most cases hit the timeout and score 0 / "agent failed" — not because the prompt is bad, but because the harness killed them prematurely.

These defects were exposed during native eval testing in PR #36, which fixed the underlying agent-resolution bug but revealed these runner weaknesses.

## Solution Approach

### Task 1: Agent Fallback Detection

Implement a **post-invocation fallback detector** that inspects captured stderr for kiro-cli's agent-resolution failure signals. When detected, fail the case explicitly (populate `ErrorContext`) rather than scoring the fallback output.

**Design Decisions:**
- **Pure string-based detection**: Use a simple helper function over the captured stderr string, eliminating dependency on kiro-cli being present at test time
- **Multiple signal patterns**: Check for both `no agent with name` and `Falling back` patterns to be robust against kiro-cli message variations
- **Unit-testable**: Extract the detection logic as a pure function (`detectAgentFallback(stderr string) bool`) that can be tested without spawning processes
- **Actionable error messages**: When fallback is detected, return an error that names the agent and describes the resolution failure

**Placement:**
- Core logic: `internal/eval/runner.go` — new helper function `detectAgentFallback(stderr string) bool`
- Invocation point: `invokeAgentNative` function, immediately after `cmd.Run()` returns
- Tests: `internal/eval/runner_test.go` or `internal/eval/faketools_test.go` — unit tests for the helper

### Task 2: Timeout Adjustment

Raise the default timeout from 2 minutes to 5 minutes to accommodate real agent runtimes, while preserving the `HOWMUX_EVAL_TIMEOUT` environment variable override.

**Design Decisions:**
- **5-minute default**: Based on observed ~2-3.5 minute architect runtimes, 5 minutes provides headroom without being wasteful
- **Preserve override mechanism**: `HOWMUX_EVAL_TIMEOUT` continues to take precedence
- **Document the reasoning**: Add an inline comment referencing the observed runtime data

**Placement:**
- `internal/eval/runner.go:821` — change `timeout := 2 * time.Minute` to `timeout := 5 * time.Minute`
- Add comment: `// Default 5-minute timeout accommodates real agent runs (~2-3.5 min/case observed for architect)`

## Relevant Files

- **Modified:**
  - `internal/eval/runner.go` — add `detectAgentFallback()` helper, integrate into `invokeAgentNative()`, raise default timeout
  
- **Created:**
  - `internal/eval/runner_test.go` (or extend existing) — unit tests for fallback detection

- **Read-only (for context):**
  - `internal/eval/types.go` — `ErrorContext` structure
  - `internal/eval/faketools_test.go` — test patterns for eval helpers

## Team Orchestration

This is a single-developer change with no cross-component dependencies. Tasks can be executed sequentially:

1. **Task 1 (Fallback Detection)** — No dependencies, can run first
2. **Task 2 (Timeout Adjustment)** — No dependencies, can run in parallel with Task 1

Both tasks are independent and touch non-overlapping sections of `runner.go`.

## Step-by-Step Task Breakdown

### Task 1: Implement Agent Fallback Detection

**Objective:** Detect when kiro-cli falls back to a default client instead of resolving the named agent, and fail the case with a clear error.

**Implementation Steps:**

1. **Create the detection helper** in `internal/eval/runner.go`:
   ```go
   // detectAgentFallback inspects kiro-cli stderr for agent-resolution failure signals.
   // Returns true if fallback was detected (agent could not be resolved).
   func detectAgentFallback(stderr string) bool {
       lowerStderr := strings.ToLower(stderr)
       // Check for kiro-cli's agent-resolution failure messages
       return strings.Contains(lowerStderr, "no agent with name") ||
              strings.Contains(lowerStderr, "falling back")
   }
   ```

2. **Integrate into `invokeAgentNative`** (after line ~910 where stderr is captured):
   ```go
   // After cmd.Run() and stderr capture (~line 910):
   
   // Detect agent-resolution fallback before checking other errors
   if detectAgentFallback(string(stderr)) {
       errCtx := &ErrorContext{
           Command:     fmt.Sprintf("kiro-cli chat --agent %s --no-interactive --trust-all-tools", agent),
           WorkingDir:  workspaceDir,
           Environment: envVars,
           Stderr:      string(stderr),
           ExitCode:    0, // kiro-cli exits 0 even when falling back
       }
       return "", CostInfo{}, errCtx, nil, fmt.Errorf("agent resolution failed: kiro-cli could not resolve agent '%s' and fell back to default client (see stderr for details)", agent)
   }
   ```

3. **Create unit tests** for the fallback detector:
   - Test case: stderr with `"no agent with name foo found"` → returns true
   - Test case: stderr with `"Falling back to default client"` → returns true
   - Test case: stderr with normal agent output → returns false
   - Test case: empty stderr → returns false
   - Test file location: `internal/eval/runner_test.go` (create if doesn't exist, or add to existing)

**Acceptance Criteria:**

- [ ] `detectAgentFallback(stderr string) bool` helper exists and is a pure function over the stderr string
- [ ] The helper checks for both `"no agent with name"` and `"falling back"` patterns (case-insensitive)
- [ ] `invokeAgentNative` calls `detectAgentFallback` immediately after capturing stderr
- [ ] When fallback is detected, `invokeAgentNative` returns an error (not nil) and populates `ErrorContext` with the full stderr
- [ ] The error message is actionable: includes the agent name and states it could not be resolved
- [ ] Unit tests exist for `detectAgentFallback` covering true/false cases
- [ ] All unit tests pass: `go test ./internal/eval/`
- [ ] A genuinely successful agent invocation (no fallback signal) is unaffected — no false positives

**Dependencies:** None

---

### Task 2: Raise Default Invocation Timeout

**Objective:** Increase the default per-invocation timeout from 2 minutes to 5 minutes to accommodate real agent runtimes, while preserving the environment variable override.

**Implementation Steps:**

1. **Update the default timeout constant** in `invokeAgentNative` (line ~821):
   ```go
   // Before:
   timeout := 2 * time.Minute
   
   // After:
   // Default 5-minute timeout accommodates real agent runs (~2-3.5 min/case observed for architect)
   timeout := 5 * time.Minute
   ```

2. **Verify the override logic is unchanged** (should already exist):
   ```go
   timeoutStr := os.Getenv("HOWMUX_EVAL_TIMEOUT")
   timeout := 5 * time.Minute // new default
   if timeoutStr != "" {
       if parsedTimeout, err := time.ParseDuration(timeoutStr); err == nil {
           timeout = parsedTimeout
       }
   }
   ```

**Acceptance Criteria:**

- [ ] The default timeout in `invokeAgentNative` is `5 * time.Minute` (was `2 * time.Minute`)
- [ ] An inline comment exists documenting the rationale: references observed ~2-3.5 min/case runtime
- [ ] The `HOWMUX_EVAL_TIMEOUT` environment variable override behavior is preserved (env var still wins)
- [ ] Grep verification: `grep -nE "2 \* time.Minute" internal/eval/runner.go` returns zero matches (or only unrelated occurrences)
- [ ] Grep verification: `grep -nE "5 \* time.Minute" internal/eval/runner.go` finds the new default timeout line

**Dependencies:** None

---

## Validation Commands

### Build and Vet
```bash
go build ./...
go vet ./internal/eval/
```

### Unit Tests
```bash
go test ./internal/eval/          # All eval tests including new fallback-detection tests
go test -v ./internal/eval/ -run TestDetectAgentFallback  # Specific test for the new helper
```

### Code Inspection
```bash
# Verify fallback detection patterns are present
grep -nE "no agent with name|Falling back|detectAgentFallback" internal/eval/runner.go

# Verify timeout was changed from 2m to 5m
grep -nE "2 \* time.Minute|5 \* time.Minute" internal/eval/runner.go
```

### Behavioral Validation (Requires kiro-cli)
```bash
# Test that a bogus agent fails loudly (not scored):
./howmux eval nonexistent-agent --no-sandbox

# Expected behavior:
# - Clear "could not resolve agent" failure message
# - No scoring output (case should fail, not be scored)
# - Exit code != 0
```

## Out of Scope

- The agent-resolution fix itself (already addressed in PR #36)
- Adding planner eval cases, a committed baseline run, or CI wiring for evals (separate follow-ups)
- Adjusting container-mode timeouts (this only affects native execution)

## Implementation Notes

### Why Post-Invocation Detection?

We cannot prevent kiro-cli from falling back (that's a kiro-cli internal behavior), so we detect it after the fact by inspecting stderr. This approach:
- Requires no kiro-cli code changes
- Works regardless of why the fallback happened (missing `.kiro/`, renamed agent, corrupted config)
- Is testable without spawning kiro-cli

### Why String-Based Detection?

Parsing stderr strings is simple, fast, and avoids dependencies on kiro-cli's exit codes (which are 0 even on fallback). The patterns `"no agent with name"` and `"falling back"` are stable and unlikely to change.

### Why 5 Minutes?

Based on PR #36 data:
- Architect cases: ~2-3.5 minutes/case
- With 2-minute timeout: most cases killed prematurely
- With 5-minute timeout: cases complete successfully

5 minutes provides 40-100% headroom over observed runtimes while remaining practical for eval runs.

### False Positive Risk

The fallback detector could false-positive if:
- An agent legitimately outputs text containing "no agent with name" or "falling back"

Mitigation: We inspect **stderr**, not stdout. Agent output goes to stdout. kiro-cli's diagnostic messages go to stderr. This separation makes false positives extremely unlikely.

## Success Metrics

After implementation:
- A misconfigured/missing agent fails the eval case immediately with a clear error message (no silent scoring)
- Real agents that take 2-3.5 minutes complete successfully instead of timing out
- Eval scores become trustworthy for measuring prompt improvements (both bugs fixed)

## Timeline Estimate

- **Task 1 (Fallback Detection):** ~30 minutes (simple helper + integration + tests)
- **Task 2 (Timeout Adjustment):** ~10 minutes (constant change + comment)
- **Testing & Validation:** ~20 minutes (run tests, behavioral validation)

**Total:** ~1 hour
