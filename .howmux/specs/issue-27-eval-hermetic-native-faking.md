# Design Specification: Eval Hermetic External-Tool Faking for Native Evals

**Issue**: #27  
**Title**: Eval: hermetic external-tool faking for native evals (PATH-injected gh fake, record calls)  
**Status**: Approved  
**Closes #27**

## Overview

This specification addresses the non-hermetic nature of native agent evaluations in the eval framework. Currently, when `howmux eval <agent>` runs without Docker (the default), agents can execute real `gh` commands that mutate actual GitHub state—creating PRs, issues, and editing labels. This is unsafe (test runs affect production) and non-reproducible (results depend on network state and real repo contents).

The solution makes native evals hermetic by intercepting external CLI tool invocations via PATH injection, using the same fake `gh` shim that already exists for the Docker sandbox path, and recording all tool calls for inspection.

## Problem Statement

### Current Behavior

`invokeAgentNative` in `internal/eval/runner.go` (~line 769):
- Spawns `kiro-cli chat --agent <name> ...` with inherited environment (no `cmd.Env` override)
- Agent prompts include raw shell commands like `gh pr create`, `gh issue edit`, `gh issue view`
- These commands execute against the real GitHub CLI, hitting the real repository
- Native evals are not isolated and can mutate production state

### Existing Fake Tool (Unusable on Native Path)

`internal/eval/sandbox/testdata/github-cli-mock/gh`:
- Bash shim that logs calls and returns canned JSON responses
- Currently hardcoded to write to `/tmp/gh-mock.log` (collision risk)
- Only installed in Docker containers via `SetupGitHubMocking`
- Attempts to install by replacing `.kiro/skills/github-cli/` which doesn't exist
- Correct shape, wrong distribution mechanism for native execution

## Solution Approach

Make native evals hermetic by:

1. **Share the fake `gh` shim** outside the sandbox package so the core eval package can use it
2. **Inject fake tools on PATH** in `invokeAgentNative` so `gh` resolves to the fake, not the real binary
3. **Surface recorded calls** on `CaseResult` so test results include external interaction records

This approach:
- Decouples faking from Docker—works on native invocation
- Reuses existing fake logic (no new fake implementations)
- Makes call recording first-class data on results
- Does NOT add call assertions (deferred to follow-up issue)
- Does NOT affect Docker sandbox behavior except to share the shim source

## Concurrency Analysis

**No concurrency concerns**: This change touches only the eval runner's command setup (`invokeAgentNative`) and the `CaseResult` type, which is populated sequentially per test case. The fake tool shim is stateless (writes to a per-invocation log file). No shared mutable state is accessed from multiple goroutines.

## Architecture

### Component Responsibilities

#### 1. Fake Tool Manager (`internal/eval/faketools.go`)

New file that encapsulates fake tool setup logic:
- `SetupFakeTools(baseDir string) (toolsDir string, callLogPath string, cleanup func(), error)`
  - Creates a temporary directory under `baseDir` for fake tool binaries
  - Materializes the fake `gh` script into `toolsDir/gh` with execute permissions
  - Creates a per-invocation call log file
  - Returns paths and a cleanup function
  - Thread-safe (creates unique temp dirs per invocation)

#### 2. Shared Fake `gh` Shim

Move from `internal/eval/sandbox/testdata/github-cli-mock/gh` to a shared location accessible by both sandbox and core eval:
- **Option A** (recommended): Embed in `internal/eval/faketools.go` as a string constant
- **Option B**: Move to `internal/eval/testdata/faketools/gh` and use `//go:embed`

The shim must:
- Accept an environment variable `HOWMUX_EVAL_CALL_LOG` specifying the log file path
- Log each invocation as a structured line: `<timestamp> <full-argv>`
- Return canned JSON responses matching the Docker sandbox behavior

#### 3. Modified `invokeAgentNative` (`internal/eval/runner.go`)

Transform from inheriting parent environment to hermetic execution:

```go
func invokeAgentNative(agent, prompt string) (string, CostInfo, *ErrorContext, error) {
    // ... existing timeout setup ...

    // Set up fake tools and isolated workspace
    tempBase, err := os.MkdirTemp("", "howmux-eval-native-*")
    if err != nil {
        return "", CostInfo{}, nil, fmt.Errorf("creating temp workspace: %w", err)
    }
    defer os.RemoveAll(tempBase)

    toolsDir, callLogPath, cleanup, err := SetupFakeTools(tempBase)
    if err != nil {
        return "", CostInfo{}, nil, fmt.Errorf("setting up fake tools: %w", err)
    }
    defer cleanup()

    // Create per-case working directory
    workspaceDir := filepath.Join(tempBase, "workspace")
    if err := os.MkdirAll(workspaceDir, 0755); err != nil {
        return "", CostInfo{}, nil, fmt.Errorf("creating workspace: %w", err)
    }

    cmd := exec.CommandContext(ctx, "kiro-cli", "chat", "--agent", agent, "--no-interactive", "--trust-all-tools")
    cmd.Stdin = strings.NewReader(prompt)
    cmd.Dir = workspaceDir  // Isolated working directory

    // Build hermetic environment
    env := os.Environ()
    env = prependPathEnv(env, toolsDir)  // Fake tools first on PATH
    env = append(env, fmt.Sprintf("HOWMUX_EVAL_CALL_LOG=%s", callLogPath))
    cmd.Env = env

    // ... existing execution logic ...

    // After execution, read recorded calls
    recordedCalls, _ := readCallLog(callLogPath)

    // ... return result with recordedCalls ...
}

// prependPathEnv prepends dir to PATH in env slice
func prependPathEnv(env []string, dir string) []string {
    // Implementation details: find PATH entry, prepend dir with os-specific separator
}

// readCallLog parses the call log file into structured entries
func readCallLog(path string) ([]string, error) {
    // Implementation details: read lines, return as slice
}
```

#### 4. Enhanced `CaseResult` (`internal/eval/types.go`)

Add field to hold recorded external calls:

```go
type CaseResult struct {
    CaseName      string           `json:"case_name"`
    ActualOutput  string           `json:"actual_output"`
    Scores        []CriterionScore `json:"scores"`
    AgentCost     CostInfo         `json:"agent_cost"`
    JudgeCost     CostInfo         `json:"judge_cost"`
    ErrorContext  *ErrorContext    `json:"error_context,omitempty"`
    ExternalCalls []ExternalCall   `json:"external_calls,omitempty"` // NEW
}

// ExternalCall represents a recorded external tool invocation
type ExternalCall struct {
    Timestamp string   `json:"timestamp"`
    Tool      string   `json:"tool"`      // e.g., "gh"
    Args      []string `json:"args"`      // parsed argv
    RawLine   string   `json:"raw_line"`  // full log line for debugging
}
```

The `ExternalCall` struct provides both machine-parseable fields and the raw line for debugging.

### Data Flow

1. **Test invocation**: `howmux eval <agent>` calls `invokeAgentNative`
2. **Setup phase**: `SetupFakeTools` creates temp dir, materializes fake `gh`, returns paths
3. **Environment construction**: `invokeAgentNative` prepends fake tools dir to PATH, sets `HOWMUX_EVAL_CALL_LOG`
4. **Execution**: `kiro-cli chat --agent <name>` runs with hermetic env and isolated workspace
5. **Agent shell commands**: When agent runs `gh pr create`, the fake `gh` is invoked (PATH resolution)
6. **Call recording**: Fake `gh` appends structured line to `$HOWMUX_EVAL_CALL_LOG`
7. **Post-execution**: Runner reads call log, parses into `[]ExternalCall`
8. **Result population**: `CaseResult.ExternalCalls` is populated with parsed records
9. **JSON output**: Results written to `.howmux/evals/results/<timestamp>/<agent>.json` include `external_calls` array

## Relevant Files

### Files to Create

- `internal/eval/faketools.go` — Fake tool setup and call log parsing
- `internal/eval/faketools_test.go` — Unit tests for fake tool setup and hermetic invocation

### Files to Modify

- `internal/eval/runner.go` — `invokeAgentNative` gains PATH injection and workspace isolation
- `internal/eval/types.go` — `CaseResult` gains `ExternalCalls` field and `ExternalCall` type

### Files to Reference (No Changes)

- `internal/eval/sandbox/testdata/github-cli-mock/gh` — Source of truth for fake `gh` behavior (will be copied/embedded into faketools.go)
- `.kiro/agents/krew-lead-prompt.md` — Documents what `gh` commands agents invoke (step 8: `gh pr create`, step 10: `gh issue edit`, etc.)

## Team Orchestration

**Single-phase implementation** with tasks executed in sequence by the builder:

1. **Task 1**: Share the fake `gh` shim (create `faketools.go`, embed the shim)
2. **Task 2**: Inject fake tools on PATH in native invocation (modify `invokeAgentNative`)
3. **Task 3**: Surface recorded calls on result (modify `types.go`, wire call log parsing into runner)
4. **Task 4**: Add hermetic invocation test (new test in `faketools_test.go`)

Tasks 1–3 are interdependent (each builds on the previous). Task 4 validates the complete implementation.

## Step-by-Step Task Breakdown

### Task 1: Share the fake `gh` shim outside the sandbox package

**Implementation Steps**:
1. Create `internal/eval/faketools.go`
2. Embed the fake `gh` script as a Go string constant or `//go:embed` directive
3. Update the shim to use `$HOWMUX_EVAL_CALL_LOG` instead of hardcoded `/tmp/gh-mock.log`
4. Implement `SetupFakeTools(baseDir string) (toolsDir, callLogPath string, cleanup func(), error)` that:
   - Creates a unique temp directory under `baseDir` for tools
   - Writes the fake `gh` script to `toolsDir/gh` with mode `0755`
   - Creates a call log file and returns its path
   - Returns a cleanup function that removes the temp directory
5. Implement `readCallLog(path string) ([]ExternalCall, error)` that:
   - Reads the log file line by line
   - Parses each line into `ExternalCall` struct (timestamp, tool="gh", args, raw_line)
   - Returns slice of parsed calls (empty slice if file doesn't exist or is empty)

**Acceptance Criteria**:
- The fake `gh` script is embedded in `internal/eval/faketools.go` and available without importing `internal/eval/sandbox`
- The shim writes each call to a log file whose path is provided via `HOWMUX_EVAL_CALL_LOG` environment variable
- Each logged line includes a timestamp and the full argv (e.g., `2024-09-11T10:56:01Z gh pr create --repo owner/repo --title "test"`)
- `SetupFakeTools` creates a unique temp directory, materializes the fake `gh`, and returns the tools dir path and call log path
- `readCallLog` parses the call log into `[]ExternalCall` with timestamp, tool, args, and raw_line fields
- Concurrent calls to `SetupFakeTools` produce isolated temp directories (no collision)
- **Verification**: `grep -n "SetupFakeTools" internal/eval/faketools.go` returns the function signature
- **Verification**: `grep -n "HOWMUX_EVAL_CALL_LOG" internal/eval/faketools.go` returns the env var reference in the embedded shim

### Task 2: Inject the fake tools on PATH in native invocation

**Implementation Steps**:
1. In `invokeAgentNative` (internal/eval/runner.go), before creating the `exec.Command`:
   - Create a per-case temp base directory: `tempBase, err := os.MkdirTemp("", "howmux-eval-native-*")`
   - Defer cleanup: `defer os.RemoveAll(tempBase)`
   - Call `SetupFakeTools(tempBase)` to get `toolsDir`, `callLogPath`, and `cleanup`
   - Defer the cleanup function
   - Create a per-case working directory: `workspaceDir := filepath.Join(tempBase, "workspace")`
2. Set `cmd.Dir = workspaceDir` so the agent runs in the isolated workspace
3. Build the command environment:
   - Start with current environment: `env := os.Environ()`
   - Implement `prependPathEnv(env []string, dir string) []string` helper that:
     - Finds the `PATH=` entry in the env slice
     - Prepends `dir` to the PATH value with the OS-specific separator (`:` on Unix, `;` on Windows)
     - Returns the modified env slice
   - Call `env = prependPathEnv(env, toolsDir)`
   - Append the call log env var: `env = append(env, fmt.Sprintf("HOWMUX_EVAL_CALL_LOG=%s", callLogPath))`
4. Set `cmd.Env = env`
5. After execution completes (before returning), read the call log:
   - `recordedCalls, _ := readCallLog(callLogPath)` (ignore errors—missing log means no calls)
6. Store `recordedCalls` in a variable to be returned (wired in Task 3)

**Acceptance Criteria**:
- `invokeAgentNative` sets `cmd.Env` to the current environment with the fake tools directory **prepended** to `PATH`
- `invokeAgentNative` sets `cmd.Dir` to a per-case temporary working directory under `tempBase` (created fresh per invocation)
- The call-log env var `HOWMUX_EVAL_CALL_LOG` is set in `cmd.Env` pointing at the per-case log file
- The temp base directory and all contents are cleaned up after invocation (via `defer`)
- `prependPathEnv` correctly handles PATH on both Unix (`:` separator) and Windows (`;` separator)
- **Verification**: `grep -n "cmd.Env" internal/eval/runner.go` returns the line where `cmd.Env = env` is set in `invokeAgentNative`
- **Verification**: `grep -n "cmd.Dir" internal/eval/runner.go` returns the line where `cmd.Dir = workspaceDir` is set in `invokeAgentNative`
- **Verification**: `grep -n "HOWMUX_EVAL_CALL_LOG" internal/eval/runner.go` returns the line where the env var is appended

### Task 3: Surface recorded calls on the result

**Implementation Steps**:
1. In `internal/eval/types.go`, add the `ExternalCall` type above `CaseResult`:
   ```go
   // ExternalCall represents a recorded external tool invocation
   type ExternalCall struct {
       Timestamp string   `json:"timestamp"`
       Tool      string   `json:"tool"`
       Args      []string `json:"args"`
       RawLine   string   `json:"raw_line"`
   }
   ```
2. Add the `ExternalCalls` field to `CaseResult`:
   ```go
   ExternalCalls []ExternalCall `json:"external_calls,omitempty"`
   ```
   Insert it after `ErrorContext` to preserve field order
3. In `invokeAgentNative` (internal/eval/runner.go), after reading `recordedCalls`:
   - The function signature must change to return the calls. Update to:
     ```go
     func invokeAgentNative(agent, prompt string) (string, CostInfo, *ErrorContext, []ExternalCall, error)
     ```
   - Return `recordedCalls` as the fourth return value: `return result, cost, nil, recordedCalls, nil` (success case)
   - Update error returns to include `nil` for the calls: `return "", CostInfo{}, errorContext, nil, err`
4. In `invokeAgent` (the caller of `invokeAgentNative`), update the call site:
   - Change from `output, cost, errCtx, err := invokeAgentNative(...)` to:
     ```go
     output, cost, errCtx, externalCalls, err := invokeAgentNative(...)
     ```
   - Store the returned `externalCalls` in a variable
   - Return it as an additional return value from `invokeAgent`
5. In the eval loop that calls `invokeAgent` and populates `CaseResult`:
   - Capture the additional return value: `output, agentCost, errCtx, externalCalls, err := invokeAgent(...)`
   - Set `result.ExternalCalls = externalCalls` when constructing the `CaseResult`

**Acceptance Criteria**:
- `CaseResult` in `internal/eval/types.go` has a new field `ExternalCalls []ExternalCall` with JSON tag `external_calls,omitempty`
- The `ExternalCall` type is defined with `Timestamp`, `Tool`, `Args`, and `RawLine` fields
- The recorded calls are populated by the runner from the call log after invocation
- The recorded calls are written into the per-run results JSON (`.howmux/evals/results/<timestamp>/<agent>.json`) alongside `ActualOutput`
- Existing result JSON fields (`case_name`, `actual_output`, `scores`, `agent_cost`, `judge_cost`, `error_context`) are preserved (backward compatible)
- Results JSON for cases with no external calls omits the `external_calls` field (JSON `omitempty`)
- **Verification**: `grep -n "ExternalCalls" internal/eval/types.go` returns both the type definition and the field in `CaseResult`
- **Verification**: `grep -n "external_calls" internal/eval/types.go` returns the JSON tag on the `CaseResult.ExternalCalls` field

### Task 4: Add hermetic invocation test

**Implementation Steps**:
1. Create `internal/eval/faketools_test.go`
2. Implement test `TestInvokeAgentNative_HermeticExecution` that:
   - Creates a minimal test agent config (or uses a real agent like `architect` if available in test context)
   - Constructs a prompt that will cause the agent to shell out to `gh` (e.g., "Create a GitHub issue with title 'test'")
   - Calls `invokeAgentNative` with this agent and prompt
   - Verifies that no real GitHub API calls were made (no network required for test to pass)
   - Reads the returned `externalCalls` and asserts:
     - At least one call is recorded
     - The recorded call tool is `"gh"`
     - The args include expected subcommands (e.g., `["issue", "create"]`)
   - Verifies that the call log file path was valid and contained parseable entries
3. Implement test `TestSetupFakeTools_Isolation` that:
   - Calls `SetupFakeTools` twice concurrently (goroutines)
   - Verifies both return different `toolsDir` paths (no collision)
   - Verifies both fake `gh` scripts are executable
   - Calls cleanup functions and verifies temp dirs are removed
4. Add table-driven tests for `prependPathEnv`:
   - Test Unix-style PATH (`:` separator): `PATH=/usr/bin:/bin` + prepend `/fake` → `PATH=/fake:/usr/bin:/bin`
   - Test Windows-style PATH (`;` separator): `PATH=C:\Windows;C:\bin` + prepend `C:\fake` → `PATH=C:\fake;C:\Windows;C:\bin`
   - Test empty PATH: `PATH=` + prepend `/fake` → `PATH=/fake`
   - Test missing PATH entry: `env = []string{"USER=test"}` + prepend `/fake` → `env = []string{"USER=test", "PATH=/fake"}`

**Acceptance Criteria**:
- A test proves that when a native invocation runs a command that shells out to `gh pr create`, the fake is used (no real PR is created) and the call is recorded in the log file
- The test does not require network access or Docker
- The test does not require a real `kiro-cli` agent (use a minimal mock agent or skip if agent infrastructure is unavailable in test context—document the skip reason)
- `TestSetupFakeTools_Isolation` verifies concurrent calls produce isolated temp directories
- `prependPathEnv` is tested with Unix and Windows PATH separators
- **Verification**: `go test ./internal/eval/ -run TestInvokeAgentNative_HermeticExecution` passes
- **Verification**: `go test ./internal/eval/ -run TestSetupFakeTools_Isolation` passes
- **Verification**: `go test ./internal/eval/` (all eval tests) passes with no regressions

## Validation Commands

```bash
# Build verification
go build ./...

# Test suite
go test ./internal/eval/    # new hermetic-invocation test passes, no Docker/network

# Behavioral: an eval of an agent that calls gh must not create a real PR/issue
# and must record the call (verify via the results JSON ExternalCalls field).

# Code inspection verification
grep -n "cmd.Env" internal/eval/runner.go        # PATH injection present in invokeAgentNative
grep -n "cmd.Dir" internal/eval/runner.go        # workspace isolation present in invokeAgentNative
grep -n "HOWMUX_EVAL_CALL_LOG" internal/eval/    # call-log wiring present (faketools.go and runner.go)
grep -n "ExternalCalls\|ExternalCall" internal/eval/types.go  # new types defined

# Integration test (manual)
# 1. Run: howmux eval architect --test-case "krew-lead-pr-creation"
# 2. Inspect: .howmux/evals/results/<latest>/<agent>.json
# 3. Verify: "external_calls" array is present and contains recorded gh invocations
# 4. Verify: No real PR or issue was created on GitHub
```

## Out of Scope (This Issue)

- **Call assertions**: Adding `ExpectedCalls` field to `TestCase` and scoring logic to compare expected vs actual calls — deferred to follow-up issue
- **Faking other tools**: Jira (`jtk`/`atlassian-cli`), Asana, or other external services — deferred to follow-up issue (this establishes the mechanism with `gh` only)
- **Docker sandbox changes**: No changes to the Docker sandbox behavior except potentially sharing the shim source if convenient (e.g., if both paths embed the same script)
- **Prompts**: No changes to agent prompts (`.kiro/agents/*-prompt.md`)

## Success Criteria

After implementation:
1. `howmux eval <agent>` runs natively (no Docker) without mutating real GitHub state
2. All `gh` commands invoked by agents during eval are intercepted and logged
3. Results JSON includes `external_calls` array with timestamp, tool, args, and raw_line for each call
4. Tests pass and prove hermetic execution (no network, no real API calls)
5. The mechanism is reusable for adding other fake tools (Jira, Asana) in future work

## Risks and Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| PATH prepending breaks on Windows | Windows evals fail to find tools | `prependPathEnv` handles `;` separator, test both platforms |
| Agent prompts change to bypass shell | Fake tools not invoked | Out of scope—agents are expected to call tools via shell per current prompts |
| Concurrent eval runs collide on log file | Call logs corrupted or lost | Use per-invocation temp directories and unique log file paths (no shared `/tmp/gh-mock.log`) |
| Real `gh` is still invoked if PATH injection fails | Non-hermetic execution | Test verifies PATH injection; fail fast if `SetupFakeTools` errors |

## Notes

- The fake `gh` shim returns canned JSON that matches expected response shapes for `issue create/view/list/edit`, `pr create/view/list`, `auth`, `api`, and `release` subcommands. This is sufficient for current agent prompts.
- Call log format is line-based for simplicity: `<ISO8601-timestamp> <tool> <arg1> <arg2> ...`. Parsing splits on whitespace after timestamp and tool name. Args with spaces must be quoted in the shim's log output.
- The `ExternalCall` struct's `Args` field is a slice of strings for machine-parseable assertions in follow-up work. The `RawLine` field preserves the original log line for debugging.
- Cleanup of temp directories is handled via `defer` to ensure isolation even on test failures.
- This implementation does NOT change the Docker sandbox path's behavior. The sandbox can continue using `SetupGitHubMocking` or can be refactored to use `SetupFakeTools` in a follow-up if desired—no regression either way.

---

**Dependencies**: None (self-contained change)  
**Estimated Effort**: 4-6 hours (Task 1: 1h, Task 2: 1.5h, Task 3: 1h, Task 4: 1.5h)  
**Testing Strategy**: Unit tests for fake tool setup, isolation, and PATH injection; integration test proves hermetic execution with call recording
