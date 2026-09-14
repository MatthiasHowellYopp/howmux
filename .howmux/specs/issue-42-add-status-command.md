# Design Specification: Add Status Command to TUI

**Issue:** #42  
**Closes:** #42

## Summary

The existing `status` command displays detailed system information including tabs, navigation help, and running/stopped agents. However, based on the issue description, users want a simpler, focused view that shows only active agents with their key details. This specification details the changes needed to streamline the `status` command output.

## Solution Approach

The current `status` command in `internal/tui/commands.go` already shows running agents, but it's embedded within a broader system status overlay that includes tabs, navigation instructions, and other contextual information. The issue requests a simplified command focused solely on agents.

**Analysis of Current Implementation:**
- The `handleStatus()` method displays an overlay with tab information, navigation help, and agent details
- Running agents are already tracked and displayed with issue number, title (truncated), status, and elapsed time
- The "No agents running" case is already handled
- Agent data is already available via `m.manager.List()` and filtered by status
- The display already uses proper table formatting with truncation

**Design Decision:**
Given that the current implementation already provides the requested functionality (shows active agents, includes issue number/title/status/elapsed time, handles empty state, and uses clean formatting), we should preserve this implementation while ensuring it meets all acceptance criteria explicitly stated in the issue.

However, we should verify that the command strictly adheres to the simplicity requirement in the issue. If the issue specifically wants *only* agent information without tab/navigation context, we may need to create a streamlined version.

**Interpretation:** Based on the issue's emphasis on "simple enhancement" and specific data points (issue number, title, status, elapsed time), we will create a focused version of the status command that shows only agent information, removing the tab and navigation sections that are tangential to the core request.

## Relevant Files

### Files to Modify:
1. **internal/tui/commands.go** - Modify `handleStatus()` to simplify output
2. **internal/tui/commands_test.go** - Add tests for the simplified status command

### Files for Reference (no changes needed):
- **internal/agent/manager.go** - Provides agent state via `List()` method
- **internal/tui/command_registry.go** - Status command already registered
- **internal/tui/tui.go** - Overlay rendering and model structure

## Concurrency Analysis

The `status` command reads agent state from `m.manager`, which is an `*agent.Manager`. This manager protects its agent map with `sync.RWMutex`:

```go
type Manager struct {
    mu     sync.RWMutex
    agents map[string]*Agent
    // ...
}
```

The `List()` method already properly acquires `m.mu.RLock()` before reading the agents map:

```go
func (m *Manager) List() []*Agent {
    m.mu.RLock()
    defer m.mu.RUnlock()
    // ... safe read
}
```

**Concurrency Assessment:** No new concurrency concerns. The existing `handleStatus()` already safely reads agent state through the lock-guarded `List()` method. The simplified version will use the same safe accessor pattern.

## Team Orchestration

This is a single-component change focused on the TUI layer. No coordination needed between teams or services.

**Implementation approach:**
- Modify the existing `handleStatus()` method to produce a focused, agent-only output
- The overlay display mechanism remains unchanged
- All agent data fetching remains unchanged
- Add unit tests to verify the simplified output format

## Step-by-Step Task Breakdown

### Task 1: Simplify Status Command Output
**File:** `internal/tui/commands.go`

**Changes:**
1. Modify `handleStatus()` to remove tab information and navigation sections
2. Keep only the "Running Agents" and "Stopped Agents" sections
3. Ensure the overlay title reflects the focused purpose (e.g., "Agent Status")
4. Maintain existing formatting for agent entries (number selection, truncation, elapsed time)
5. Preserve the "No agents running" case when the agent list is empty

**Acceptance Criteria:**
- `status` command displays only agent information (no tabs, no navigation help)
- Output includes issue number, truncated title, status, and elapsed time for each agent
- When no agents are running, display shows "No agents running" message
- Table formatting remains clean and readable with proper truncation
- Running agents are numbered 1-9 for interactive selection (existing behavior)
- Stopped agents are listed without numbers (existing behavior)
- Agent list is sorted by issue number for deterministic ordering (existing behavior)

**Dependencies:** None

**Estimated Duration:** 15-20 minutes

---

### Task 2: Add Unit Tests for Simplified Status Command
**File:** `internal/tui/commands_test.go`

**Changes:**
1. Add test case: `TestHandleStatus_NoAgents` - verifies "No agents running" message appears
2. Add test case: `TestHandleStatus_WithRunningAgents` - verifies running agents display with correct fields
3. Add test case: `TestHandleStatus_WithStoppedAgents` - verifies stopped agents display correctly
4. Add test case: `TestHandleStatus_MixedAgentStates` - verifies combined display of running and stopped
5. Each test should verify:
   - Overlay activation with correct title
   - Presence of required fields (issue number, title, status, elapsed time)
   - Absence of tab/navigation sections
   - Proper handling of empty states

**Acceptance Criteria:**
- Test coverage for all agent state combinations (none, running only, stopped only, mixed)
- Tests verify the simplified output structure (no tab/navigation sections)
- Tests confirm required fields are present in output
- Tests pass with `go test ./internal/tui/...`
- Tests use table-driven approach for clarity and maintainability

**Dependencies:** Task 1 (requires simplified implementation to test against)

**Estimated Duration:** 20-25 minutes

---

### Task 3: Verify Integration and Output Quality
**Manual verification steps**

**Changes:**
1. Build the application: `go build -o howmux ./cmd/howmux`
2. Run the TUI and execute `status` command with no agents
3. Start an agent (via `watch start` or manual spawn) and verify `status` output
4. Stop an agent and verify both running and stopped agents display correctly
5. Confirm truncation works correctly with long issue titles
6. Verify elapsed time updates are accurate

**Acceptance Criteria:**
- Status command produces clean, readable output in actual terminal
- Truncation works correctly at various terminal widths
- Elapsed time displays in human-readable format (seconds truncated)
- Interactive number selection still works for running agents (1-9)
- No regression in existing overlay behavior (ESC to close, etc.)

**Dependencies:** Task 1, Task 2

**Estimated Duration:** 10-15 minutes

## Validation Commands

### Build and Test
```bash
# Build the application
go build -o howmux ./cmd/howmux

# Run unit tests
go test ./internal/tui/... -v -run TestHandleStatus

# Run full test suite to catch regressions
go test ./internal/tui/... -v

# Verify no other tests broken
go test ./... -v
```

### Manual Testing
```bash
# Start the TUI
./howmux

# In the REPL, test the status command:
> status
# Expected: Overlay showing agent status (or "No agents running" if empty)

# Start watcher to create agents
> watch start
# Wait for an agent to spawn

# Check status again
> status
# Expected: Shows running agent with issue #, title, status, elapsed time

# Press a number (1-9) to navigate to agent output
# Expected: Switches to agent tab

# Stop an agent
> stop <issue-number>

# Check status again
> status
# Expected: Shows stopped agent in separate section
```

### Code Quality
```bash
# Check formatting
gofmt -l internal/tui/commands.go internal/tui/commands_test.go
# Expected: No output (files already formatted)

# Run linter if available
golangci-lint run ./internal/tui/...
```

## Success Metrics

1. **Functional:**
   - Status command displays agent information only
   - All required fields present (issue #, title, status, elapsed time)
   - "No agents running" case handled correctly
   - Clean table formatting maintained

2. **Quality:**
   - Unit tests pass
   - No regressions in other commands
   - Code follows existing style conventions
   - Proper error handling maintained

3. **User Experience:**
   - Output is immediately readable
   - Truncation works correctly
   - Elapsed time is human-readable
   - Interactive selection (1-9) still functional

## Notes

- The current implementation already has most of the requested functionality
- The main change is simplifying the output by removing tab/navigation information
- Existing overlay mechanism handles the display correctly
- Agent state management and locking are already properly implemented
- The interactive number selection (1-9) is a nice UX feature worth preserving
- Sorting by issue number provides deterministic, predictable ordering
