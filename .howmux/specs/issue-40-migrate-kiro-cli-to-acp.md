# Design Specification: Migrate all kiro-cli interaction to ACP

**Issue:** #40  
**Title:** Migrate all kiro-cli interaction to ACP (pipeline + eval)  
**Closes:** #40

## Solution Approach

Migrate the two remaining direct-exec paths (eval runner and agent pipeline) to use the Agent Communication Protocol (ACP) instead of `exec.Command` with stdout/stderr scraping. The planning tab already uses ACP successfully and serves as the reference implementation.

### High-Level Strategy

1. **Phase 1 (Eval Runner):** Migrate `internal/eval/runner.go::invokeAgentNative` to use `internal/acp` client, assembling scored output from streamed text chunks. Lowest risk — isolated, single-shot execution.

2. **Phase 2 (Agent Pipeline):** Migrate `internal/agent/manager.go` kiro-cli spawns to use ACP sessions. Replace stdout capture with `SessionUpdate` event streaming. Map ACP turn-end/error to existing retry state machine.

3. **Phase 3 (Phase Detection):** Replace `internal/tui/output_view.go::detectPhaseTransition` ANSI/text matching with structured `ToolCall`/`Plan` event handling from `SessionUpdate`.

4. **Phase 4 (Testing):** Add live-guarded integration test for `internal/acp` with full turn execution, plus unit tests for stream→sink adaptation.

### Key Benefits

- **Structured events replace text scraping:** `SessionUpdate` delivers discrete `ToolCall`, `ToolCallUpdate`, and `Plan` events — no more ANSI stripping or regex-on-narrative phase detection.
- **Granular tool trust:** `kiro-cli acp` supports `--trust-tools <names>` in addition to `--trust-all-tools`.
- **One transport to maintain:** All three paths (planning, pipeline, eval) go through the same `internal/acp` client.
- **Correct timestamp feature:** Phase detection becomes "a tool-call/plan event arrived" — fixes the class of bug from PR #35.

### Confirmed Findings

Live probe confirmed:
- Headless `kiro-cli acp` turns complete successfully
- Agent does its own file/shell I/O (zero client `ReadTextFile`/`WriteTextFile` callbacks)
- Stubbed fs/terminal handlers are NOT a blocker (never invoked)
- Structured phase events flow over ACP via `SessionUpdate`

### Concurrency Considerations

The watcher runs multiple agents in parallel. `KiroACPClient` appears single-session — each agent will need its own client instance (one per worktree). The existing `internal/acp/client.go` creates a new kiro-cli subprocess per client, so N parallel clients are safe.

## Relevant Files

### Phase 1: Eval Runner (ACP Migration)
- **`internal/eval/runner.go`** — Migrate `invokeAgentNative` (line 845) from `exec.Command` to ACP client
- **`internal/acp/client.go`** — Existing ACP client (reference implementation)
- **`internal/acp/types.go`** — ACP interfaces and types

### Phase 2: Agent Pipeline (ACP Migration)
- **`internal/agent/manager.go`** — Replace `kiro-cli chat` spawns (lines ~230, ~480) with ACP sessions
- **`internal/agent/output_capture.go`** — Adapt to consume `SessionUpdate` events instead of stdout pipes
- **`internal/acp/client.go`** — May need event→sink adapter for ring buffer streaming

### Phase 3: Phase Detection (Event-Based)
- **`internal/tui/output_view.go`** — Replace `detectPhaseTransition` (line 189) with `ToolCall`/`Plan` event handling
- **`internal/acp/types.go`** — Extend `StreamingResponse` if needed to carry structured event types

### Phase 4: Testing
- **`internal/acp/client_test.go`** — New file: live-guarded integration test
- **`internal/agent/manager_test.go`** — Update tests for ACP-based spawning
- **`internal/eval/runner_test.go`** — Update tests for ACP-based invocation

### Supporting Files
- **`internal/logging/logger.go`** — Used for debug logging throughout ACP interactions
- **`internal/tui/planning_tab.go`** — Reference implementation for ACP streaming (lines 540-690)

## Team Orchestration

All implementation tasks are sequential due to dependencies:

- **Task 1** must complete first (eval runner migration establishes patterns)
- **Task 2** depends on Task 1 (reuses patterns for agent pipeline)
- **Task 3** depends on Task 2 (phase detection requires ACP event stream from pipeline)
- **Task 4** runs after all migrations complete (tests the integrated system)

Tasks 1-3 are single-builder work (same subsystem, shared patterns). Task 4 can run in parallel once Tasks 1-3 are complete.

## Step-by-Step Task Breakdown

### Task 1: Migrate Eval Runner to ACP

**Goal:** Replace `invokeAgentNative` direct exec with ACP client.

**Files:**
- `internal/eval/runner.go`

**Implementation Steps:**

1. In `invokeAgentNative` (line 845):
   - Create `acp.ConnectionConfig` with:
     - `Agent: agent` (parameter passed in)
     - `Cwd: workspaceDir` (the isolated workspace)
     - `RequestTimeout: timeout` (from `HOWMUX_EVAL_TIMEOUT` or 5min default)
   - Create `acp.NewClient(config)`
   - Call `client.Connect(ctx)`
   - On connection failure, return with `ErrorContext` (same pattern as existing `cmd.Run()` failure)

2. Build the prompt string (existing `prompt` parameter)

3. Create `acp.MessageRequest`:
   ```go
   req := &acp.MessageRequest{
       Message:        prompt,
       Streaming:      false, // Eval needs complete output, not incremental
       ResponseFormat: "text",
       Timeout:        timeout,
   }
   ```

4. Call `client.SendMessage(ctx, req)` (not `StreamMessage` — eval wants the full result)

5. Assemble the output string from response:
   - Extract `response.Message`
   - Apply existing `stripANSISequences` function
   - Call existing `estimateCost(prompt, result)`

6. Handle errors:
   - Map ACP connection/send errors to `ErrorContext` struct
   - Preserve timeout detection (`ctx.Err() == context.DeadlineExceeded`)
   - Preserve agent fallback detection (check `response.Error` for resolution failure)

7. Clean up: `client.Close()` (defer)

8. Return existing `(result, cost, errorContext, recordedCalls, err)` tuple

**Acceptance Criteria:**
1. `invokeAgentNative` uses `internal/acp` client instead of `exec.Command`
2. Timeout behavior preserved: `HOWMUX_EVAL_TIMEOUT` honored, returns timeout error on exceed
3. Agent fallback detection preserved: detects and reports when kiro-cli can't resolve agent
4. Error context preserved: `ErrorContext` struct populated with command, working dir, environment, stderr (from ACP error), exit code
5. Output format unchanged: returns stripped text, cost estimate, error context, recorded calls, error
6. Fake tools integration unchanged: `SetupFakeTools` and call log reading work as before
7. **Verification:** `./howmux eval architect --no-sandbox` completes and produces a scored result JSON
8. **Regression:** Existing eval tests pass without modification

### Task 2: Migrate Agent Pipeline to ACP

**Goal:** Replace `kiro-cli chat` spawns in `manager.go` with ACP sessions.

**Files:**
- `internal/agent/manager.go`
- `internal/agent/output_capture.go`

**Implementation Steps:**

1. In `Manager.Spawn` (line ~230):
   - After creating worktree and log file, create `acp.ConnectionConfig`:
     - `Agent: "krew-lead"`
     - `Cwd: worktreePath` (the worktree directory)
     - `RequestTimeout: 60 * time.Second` (reasonable default for agent turns)
   - Create `acp.NewClient(config)` and store in `Agent` struct (add new field `acpClient *acp.KiroACPClient`)
   - Call `client.Connect(ctx)` with background context
   - On connection failure, return error (cleanup worktree/log file same as existing spawn failure)

2. Build the prompt string (existing format):
   ```go
   prompt := fmt.Sprintf("Process issue #%d from repo %s. Worktree name: %s. You are already in the worktree directory — all file operations happen here. Skip worktree creation (step 2).", issueNumber, repo, worktreeName)
   ```

3. Create `acp.MessageRequest`:
   ```go
   req := &acp.MessageRequest{
       Message:        prompt,
       Streaming:      true, // Agents produce long-running output
       ResponseFormat: "text",
       Timeout:        0, // No timeout (agents can run for hours)
   }
   ```

4. Call `client.StreamMessage(ctx, req)` to get `<-chan *acp.StreamingResponse`

5. Spawn goroutine to consume stream and feed existing sinks:
   - Read from stream channel in loop
   - For each `StreamingResponse`:
     - If `Type == "text"`: write `Content` to `agentLogFile`, `outputCapture`, and optional console (same as existing `io.MultiWriter`)
     - If `Type == "error"`: write `Error` to sinks, break loop
     - If `Type == "done"`: break loop
   - Store `ToolCall`, `Plan`, and other structured events in a new `Agent.events []acp.StreamingResponse` field for phase detection (Task 3)

6. Replace existing `monitorAgent` goroutine:
   - Wait for stream channel to close (instead of `cmd.Wait()`)
   - Existing progress ticker (60s "still working" log) remains unchanged
   - Call `client.Close()` when stream ends
   - Determine exit code from stream outcome:
     - `Type == "done"` → exit code 0
     - `Type == "error"` → exit code 1
     - Channel closed without done → exit code 1

7. Map stream outcome to existing retry logic:
   - Call `m.HandleExit(agent.ID, exitCode)` (existing function)
   - Retry state machine unchanged (retries on non-zero exit)
   - PR verification and labeling unchanged

8. Update `retryAgent` function (line ~480) with same ACP pattern

**Acceptance Criteria:**
1. `Manager.Spawn` uses `internal/acp` client instead of `exec.Command`
2. Agent output captured: text chunks from `StreamingResponse` written to log file, ring buffer, optional console
3. Structured events stored: `ToolCall`, `Plan` events stored in `Agent.events` field (for Task 3)
4. Exit code mapping: stream "done" → 0, "error"/closed → 1
5. Retry behavior preserved: `HandleExit` logic unchanged, retries on non-zero exit
6. PR verification unchanged: `github.PRExistsForIssue` and cleanup logic work as before
7. **Verification:** A labeled issue is processed end-to-end (issue → spec → build → validate → PR) over ACP
8. **Concurrency verified:** Two agents running in parallel (separate issues) each get their own ACP client and complete without interference
9. **Regression:** TUI output view still shows agent progress (text chunks appear in real-time)

**Concurrency Acceptance Criteria (Critical):**
- Each agent in `Manager.agents` map has its own `acpClient *acp.KiroACPClient` instance
- `KiroACPClient` spawns a separate `kiro-cli acp` subprocess per agent (verified via `ps` during parallel runs)
- No shared ACP client state between agents
- **Verification via concurrent test:** Spawn 2 agents simultaneously for different issues; both complete successfully without blocking/errors

### Task 3: Event-Based Phase Detection

**Goal:** Replace ANSI/text matching in `output_view.go` with structured event handling.

**Files:**
- `internal/tui/output_view.go`
- `internal/agent/manager.go` (to expose events)

**Implementation Steps:**

1. Extend `Agent` struct in `manager.go`:
   - Add field `events []acp.StreamingResponse` (populated in Task 2 stream consumer)
   - Add lock-guarded getter `GetEvents() []acp.StreamingResponse` (reader must acquire `m.mu.RLock()`)

2. Extend `Manager`:
   - Add method `GetAgentEvents(id string) []acp.StreamingResponse` (calls `agent.GetEvents()` under lock)

3. In `output_view.go`, replace `detectPhaseTransition` (line 189):
   - Remove ANSI/keyword-based heuristic
   - New function signature: `detectPhaseTransitionEvent(agent *agent.Agent, line string) (isPhase bool, eventType string)`
   - For each line in agent output:
     - Check if an `acp.StreamingResponse` with `Type == "tool_call"` or `Type == "plan"` arrived at the same timestamp (within 1 second)
     - If yes: return `(true, resp.Type)`
     - If no: return `(false, "")`

4. Update timestamp injection logic (line ~165):
   - When `detectPhaseTransitionEvent` returns `true`:
     - Use event timestamp from `StreamingResponse.Timestamp`
     - Cache key becomes `fmt.Sprintf("%d:%s:%s", issueNumber, eventType, lineContent)` (event type added for uniqueness)
   - Format timestamp same as before: `[YYYY-MM-DD HH:MM:SS]`

5. Add event type indicator to timestamp display (optional enhancement):
   - `[YYYY-MM-DD HH:MM:SS] 🔧` for `tool_call`
   - `[YYYY-MM-DD HH:MM:SS] 📋` for `plan`

**Acceptance Criteria:**
1. `detectPhaseTransition` removed and replaced with `detectPhaseTransitionEvent`
2. Phase detection uses `ToolCall` and `Plan` events from `acp.StreamingResponse`
3. Timestamps derived from event timestamps (not `time.Now()` on arbitrary matches)
4. Event type stored in cache key (prevents collision between different event types on same line)
5. **Verification:** Timestamps appear on agent runs at actual tool calls and plans (not on narrative text containing "read"/"check")
6. **Regression:** Timestamp cache still keyed by content hash (preserves stable timestamps across buffer wraps)
7. **Concurrency:** All access to `Agent.events` is lock-guarded via `Manager.GetAgentEvents`

**Concurrency Acceptance Criteria:**
- `Manager.GetAgentEvents(id)` acquires `m.mu.RLock()` before reading `agent.events`
- Writer (Task 2 stream consumer) acquires `m.mu.Lock()` before appending to `agent.events`
- **Add a concurrent test:** Spawn agent, call `GetAgentEvents` from TUI render loop while stream consumer is writing; verify no data race via `go test -race`

### Task 4: ACP Integration Tests

**Goal:** Add automated test coverage for `internal/acp`.

**Files:**
- `internal/acp/client_test.go` (new file)
- `internal/agent/manager_test.go` (update existing)
- `internal/eval/runner_test.go` (update existing)

**Implementation Steps:**

1. Create `internal/acp/client_test.go`:
   - Live-guarded integration test:
     ```go
     func TestACPClientLiveTurn(t *testing.T) {
         if os.Getenv("HOWMUX_ACP_INTEGRATION_TEST") == "" {
             t.Skip("Set HOWMUX_ACP_INTEGRATION_TEST=1 to run live ACP test")
         }
         // Create client, connect, send prompt, verify response
     }
     ```
   - Test cases:
     - Connection succeeds
     - Session creation succeeds
     - Prompt sends and stream completes
     - `done` event arrives
     - Text chunks accumulate into coherent output
   - Use a simple agent (e.g., `architect`) and trivial prompt ("List the files in this directory")

2. Unit tests for stream→sink adaptation:
   - Mock stream channel producing `StreamingResponse` events
   - Verify text chunks written to output sink
   - Verify `tool_call` and `plan` events captured
   - Verify `error` and `done` events terminate stream correctly

3. Update `internal/agent/manager_test.go`:
   - Add test for ACP-based spawn (mocked stream)
   - Verify `Agent.acpClient` field populated
   - Verify stream consumer writes to output capture
   - Verify exit code mapping (done→0, error→1)

4. Update `internal/eval/runner_test.go`:
   - Add test for ACP-based `invokeAgentNative` (mocked client)
   - Verify output assembly from response
   - Verify timeout handling
   - Verify error context population

**Acceptance Criteria:**
1. Live integration test in `internal/acp/client_test.go` (skipped by default, runnable with env flag)
2. Live test completes a full turn: connect → session → prompt → stream → done
3. Unit tests cover stream→sink adaptation (text, events, termination)
4. Updated tests in `manager_test.go` verify ACP spawn behavior
5. Updated tests in `runner_test.go` verify ACP invocation behavior
6. **Verification:** `go test $(go list ./... | grep -v eval/sandbox)` passes
7. **Verification:** `HOWMUX_ACP_INTEGRATION_TEST=1 go test ./internal/acp` completes successfully

## Validation Commands

```bash
# Build verification
go build ./cmd/howmux

# Unit tests (no sandbox, no live ACP)
go test $(go list ./... | grep -v eval/sandbox)

# Phase 1 behavioral verification
./howmux eval architect --no-sandbox

# Phase 2 behavioral verification (end-to-end agent run)
# (requires labeled GitHub issue in test repo)
./howmux
> watch start
# Observe agent completes issue → PR via ACP

# Phase 3 behavioral verification
# (observe timestamps appear at tool calls/plans, not on narrative text)
./howmux
# Check output view during agent run

# Phase 4 live integration test
HOWMUX_ACP_INTEGRATION_TEST=1 go test ./internal/acp -v

# Race detector (verify concurrent agent access is safe)
go test -race ./internal/agent
go test -race ./internal/tui
```

## Open Items

1. **Session stability:** The standalone-binary probe initially failed `NewSession` with "peer disconnected before response" and a background `creds_agent` MCP crash. Watch for session fragility under the real pipeline. This may be environmental (same root cause as recurring dirty-agent-file churn). If it recurs, investigate ACP connection retry logic.

2. **Multiple parallel agents:** The watcher can run multiple agents in parallel. Each agent needs its own `acp.KiroACPClient` instance. Verify via concurrent test (Task 2 acceptance criteria).

3. **Turn-end detection:** Define how ACP session completion maps to manager retry counters. Current approach: stream "done" event → exit code 0, "error"/closed → exit code 1. Existing `HandleExit` logic handles both.

4. **Event buffering:** `Agent.events` field grows unbounded as agent runs. Consider ring buffer or event expiration if agents produce many tool calls. Not a blocker for initial migration (agents typically have <100 tool calls per run).

5. **Planning tab convergence:** After this migration, all three paths (planning, pipeline, eval) use ACP. Consider extracting common stream→sink adapter from `planning_tab.go` into `internal/acp` for reuse. Not required for this issue, but good follow-up.

## Notes

- The planning tab implementation in `internal/tui/planning_tab.go` (lines 540-690) is the reference for ACP streaming patterns.
- The existing `internal/acp/client.go` already handles connection, session, and streaming — Tasks 1-2 are primarily wiring this client into the eval and pipeline paths.
- Phase detection (Task 3) is the largest architectural change: replacing heuristic text matching with structured event handling. This is the correct fix for the timestamp feature bug (PR #35).
- All ACP errors should populate `ErrorContext` struct (same pattern as existing `exec.Command` error handling) to maintain debugging visibility.
