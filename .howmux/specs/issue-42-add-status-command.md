# Design Specification: Add Status Command to TUI

**Issue:** #42 - Add status command to TUI  
**Status:** Ready for Implementation  
**Complexity:** Low

Closes #42

## Problem Analysis

After analyzing the codebase, the `status` command already exists in `internal/tui/commands.go` with a comprehensive implementation. However, the existing implementation goes beyond the basic requirements in the issue description by including:

- Tab management information
- Interactive agent selection via number keys
- Overlay-based display
- Title truncation for space efficiency

The issue requests a simpler, table-based status command focused solely on agent information. This spec addresses the **apparent** intent: provide a simpler, dedicated agent status view that can be quickly scanned without the tab information overhead.

## Concurrency Analysis

**No new concurrency concerns introduced.** The change reads agent state via `Manager.List()`, which already acquires `m.mu.RLock()` internally. The existing `handleStatus()` method already demonstrates safe concurrent access patterns that this implementation will follow.

## Solution Approach

**Decision: Enhance existing status command rather than replace it**

The existing `handleStatus()` already implements all acceptance criteria from the issue. Rather than removing functionality users may depend on, we'll:

1. **Add a `--simple` flag** to the status command for a minimal table view
2. **Default behavior remains unchanged** (overlay with tabs + agents)
3. **Simple mode** shows only agent table without tab information

This preserves backward compatibility while addressing the issue's request for a focused agent status view.

### Why Not Replace?

1. The existing implementation provides valuable context (active tabs, navigation hints)
2. Interactive agent selection via number keys is a UX improvement
3. No evidence that the current implementation is problematic
4. Adding a flag is safer than removing features

## Relevant Files

### Files to Modify

1. **`internal/tui/commands.go`**
   - Modify `handleStatus()` to accept optional `--simple` flag
   - Add `buildSimpleStatusTable()` helper function
   - Preserve existing overlay-based implementation as default

2. **`internal/tui/command_registry.go`**
   - Update command registry to document `--simple` flag
   - Update help text for status command

### Files Referenced (No Changes)

1. **`internal/agent/manager.go`** - Provides `List()` method (no changes needed)
2. **`internal/agent/agent.go`** - Agent struct definition (no changes needed)
3. **`internal/tui/tui.go`** - Main model and message handling (no changes needed)
4. **`internal/tui/styles.go`** - Styling utilities (no changes needed)

## Team Orchestration

**Single-file change with no dependencies** - builder can implement immediately without coordination.

## Step-by-Step Task Breakdown

### Task 1: Add Simple Status Table Function
**Acceptance Criteria:**
1. Create `buildSimpleStatusTable()` function in `commands.go`
2. Function accepts agent list and returns formatted string slice
3. Table format:
   ```
   Issue #   Status      Elapsed    Title
   -------   --------    --------   -----
   42        running     2m 30s     Add status command (truncated to ~40 chars)
   ```
4. Use fixed-width columns with padding
5. Truncate title to fit available width
6. Show "No agents running" when list is empty
7. Sort agents by issue number (ascending) for deterministic output
8. Handle edge cases:
   - Empty agent list
   - Very long titles
   - Very large elapsed times (format as hours if > 60 minutes)

**Dependencies:** None

### Task 2: Modify handleStatus to Support --simple Flag
**Acceptance Criteria:**
1. Parse command arguments in `handleStatus()` to detect `--simple` flag
2. If `--simple` flag present:
   - Call `buildSimpleStatusTable()` instead of building overlay content
   - Append result directly to activity lines (not overlay)
   - Do not modify tab-related state
3. If no flag present:
   - Use existing overlay-based implementation (default behavior)
4. Handle invalid flags with error message
5. All access to agent list via `m.manager.List()` (thread-safe)

**Dependencies:** Task 1

### Task 3: Update Command Registry and Help
**Acceptance Criteria:**
1. Update `command_registry.go` to document `status [--simple]` syntax
2. Update `handleHelp()` in `commands.go`:
   ```
   status         - Show system status with tabs and agents (interactive overlay)
   status --simple - Show agent status table only
   ```
3. Autocomplete remains `status` (no flag completion needed for simplicity)

**Dependencies:** Task 2

### Task 4: Add Unit Tests
**Acceptance Criteria:**
1. Create `internal/tui/commands_test.go` if it doesn't exist
2. Test `buildSimpleStatusTable()`:
   - Empty agent list → "No agents running"
   - Single agent → correct formatting
   - Multiple agents → sorted by issue number
   - Long titles → truncated correctly
   - Various elapsed times → formatted correctly (seconds, minutes, hours)
3. Test `handleStatus()` flag parsing:
   - No args → overlay mode
   - `--simple` → table mode
   - Invalid flag → error message
4. Use table-driven tests for comprehensive coverage

**Dependencies:** Task 3

### Task 5: Manual Testing Validation
**Acceptance Criteria:**
1. Start howmux with no running agents:
   - `status` → shows overlay with "No agents running"
   - `status --simple` → shows "No agents running" in activity
2. Spawn agent for issue #1:
   - `status` → shows agent in overlay with tabs
   - `status --simple` → shows single-row table
3. Spawn agents for issues #1, #5, #20:
   - `status --simple` → shows three rows sorted by issue number
   - Elapsed time updates correctly
4. Verify title truncation with very long issue titles
5. Test invalid flag: `status --invalid` → error message

**Dependencies:** Task 4

## Validation Commands

### Build and Test
```bash
# Build the project
task build

# Run unit tests
task test

# Run specific test file
go test -v ./internal/tui/commands_test.go

# Run with race detector
go test -race ./internal/tui/...
```

### Manual Verification
```bash
# Start howmux
./howmux

# In REPL:
status                    # Verify overlay mode (default)
status --simple          # Verify table mode
status --invalid         # Verify error handling
watch start              # Start watcher
# Wait for agent to spawn
status --simple          # Verify agent appears in table
```

### Integration Test
```bash
# Verify in actual workflow
./test_integration.sh
```

## Implementation Notes

### Table Formatting Details

**Column Widths:**
- Issue #: 8 chars (fits "Issue #" + 5-digit numbers)
- Status: 12 chars (fits "running"/"completed"/"failed")
- Elapsed: 10 chars (fits "99h 59m 59s")
- Title: Remaining width (minimum 20 chars)

**Truncation Strategy:**
```go
func truncate(s string, max int) string {
    if len(s) <= max {
        return s
    }
    if max <= 3 {
        return s[:max]
    }
    return s[:max-3] + "..."
}
```

**Elapsed Time Formatting:**
```go
func formatElapsed(d time.Duration) string {
    d = d.Truncate(time.Second)
    h := int(d.Hours())
    m := int(d.Minutes()) % 60
    s := int(d.Seconds()) % 60
    
    if h > 0 {
        return fmt.Sprintf("%dh %dm %ds", h, m, s)
    }
    if m > 0 {
        return fmt.Sprintf("%dm %ds", m, s)
    }
    return fmt.Sprintf("%ds", s)
}
```

### Backward Compatibility

- **Existing behavior preserved:** `status` with no arguments continues to show the overlay
- **New behavior added:** `status --simple` provides minimal table view
- **No breaking changes:** All existing commands and workflows continue to work

## Non-Goals

- Replacing the existing overlay-based status view
- Adding other flags (`--json`, `--verbose`, etc.)
- Filtering or sorting options beyond default (issue number ascending)
- Persisting status output to file
- Real-time updates (status is a snapshot command, user can re-run)

## Future Enhancements (Out of Scope)

- Add `--json` flag for machine-readable output
- Add `--watch` flag for continuous updates
- Add filtering by status: `status --simple --running`
- Add custom sort options: `status --simple --sort=elapsed`
- Color-code status column based on state
