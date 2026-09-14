# Design Specification: Add Timestamps to Agent Tab Output

**Issue**: #30  
**Title**: Add timestamps to agent tab output for major workflow phases  
**Created**: 2026-09-14

## Problem Statement

Agent output in individual agent tabs currently displays as a continuous stream with only an elapsed time at the end. Users cannot determine:
- When specific workflow phases began
- How long individual phases took
- Whether an agent is stuck or working on a long task

The krew-lead agent workflow has distinct phases (Read Issue → Delegate to Architect → Read Spec → Execute Tasks → QA Loop → Push Changes → Create PR), but these boundaries are not timestamped.

## Solution Approach

Add display-layer timestamp injection that:
1. Detects major workflow phase boundaries in agent output
2. Injects fixed timestamps in `[YYYY-MM-DD HH:MM:SS]` format at phase boundaries
3. Stores timestamps separately from output capture to prevent log file pollution
4. Renders timestamps only in the TUI display layer (`internal/tui/output_view.go`)
5. Preserves scroll position during redraw
6. Uses distinct visual styling for clarity

### Architectural Decisions

**Why display-layer injection over capture-layer?**
- Timestamps are UI enhancement, not data
- Log files remain clean for programmatic analysis
- Output capture mechanism stays unchanged
- Single source of truth for timestamp detection logic

**Why fixed timestamps per line?**
- Prevents timestamp drift on redraws
- Maintains temporal consistency across viewport operations
- Requires mapping output line → timestamp

**Data Structure Choice:**
- Store timestamps in a map: `map[string]time.Time` where key is `<issue-number>:<line-index>`
- Alternative considered: extend OutputCapture with timestamp slice (rejected: violates display-only constraint)
- Timestamp cache belongs in `OutputView` struct, scoped to display lifecycle

## Relevant Files

### Files to Modify

1. **`internal/tui/output_view.go`** (Primary Implementation)
   - Add `lineTimestamps map[string]time.Time` field to `OutputView` struct
   - Implement `detectPhaseTransition(line string) bool` helper
   - Modify `refreshContent()` to inject timestamps on phase boundaries
   - Add timestamp formatting helper: `formatTimestamp(t time.Time) string`
   - Preserve existing scroll position logic

2. **`internal/config/themes.go`** (Theme Support)
   - Add `Timestamp string` field to `Theme.Colors` struct
   - Update `validateTheme()` to validate timestamp color
   - Update `getDefaultTheme()` with default timestamp color (`#888888` - muted gray)
   - Update validation in `colorFields` map

3. **`internal/tui/styles.go`** (Display Styling)
   - Add `Timestamp lipgloss.Style` field to `Styles` struct
   - Initialize in `NewStyles()` using theme's timestamp color
   - Fallback to `TextMuted` if timestamp color not defined (backwards compatibility)

4. **`cmd/howmux/templates/howmux/themes/*.yaml`** (All Theme Files)
   - Add `timestamp: "#888888"` to default.yaml
   - Add `timestamp: "#CCCCCC"` to light.yaml
   - Add `timestamp: "#FFAA00"` to high-contrast.yaml

### Files to Reference (No Changes)

- **`internal/agent/output_capture.go`** - Understand output capture mechanism
- **`internal/agent/manager.go`** - Understand how agent output flows
- **`.kiro/agents/krew-lead-prompt.md`** - Reference workflow phase keywords

## Team Orchestration

### Concurrency Considerations

**Concurrency Boundary**: The `OutputView.refreshContent()` method is called from the TUI render goroutine and reads agent state via `manager.GetOutputLines()`, which is protected by locks in the agent manager.

**Timestamp Cache Access**: The `lineTimestamps` map is accessed only within `refreshContent()`, which runs on the TUI render goroutine. No cross-goroutine access occurs, so no locking is required for the timestamp cache itself.

**Agent State Access**: All reads of agent output via `manager.GetOutputLines()` and `manager.GetOutputGeneration()` are already lock-protected by the agent manager's internal mutex.

### Implementation Sequence

This is a single-feature implementation with logical sequencing:

1. **Backend: Theme and Style Infrastructure** (No Dependencies)
   - Update theme config structure
   - Update styles initialization
   - Update all template theme files

2. **Frontend: Timestamp Injection Logic** (Depends on Task 1)
   - Implement phase detection
   - Add timestamp cache to OutputView
   - Modify refreshContent() to inject timestamps

3. **Integration: End-to-End Testing** (Depends on Task 1, 2)
   - Manual testing with running agent
   - Verify log file isolation
   - Verify scroll preservation
   - Verify timestamp fixation on redraws

## Task Breakdown

### Task 1: Theme and Style Infrastructure

**Description**: Add timestamp color support to theme system and styles.

**Acceptance Criteria**:
1. `internal/config/themes.go`:
   - `Theme.Colors` struct has `Timestamp string` field
   - `validateTheme()` validates timestamp color (optional field for backwards compatibility)
   - `getDefaultTheme()` returns `Timestamp: "#888888"`
   - `colorFields` map includes timestamp validation

2. `internal/tui/styles.go`:
   - `Styles` struct has `Timestamp lipgloss.Style` field
   - `NewStyles()` initializes `Timestamp` style using theme's timestamp color with fallback to `TextMuted`

3. All theme files updated:
   - `cmd/howmux/templates/howmux/themes/default.yaml` has `timestamp: "#888888"`
   - `cmd/howmux/templates/howmux/themes/light.yaml` has `timestamp: "#CCCCCC"`
   - `cmd/howmux/templates/howmux/themes/high-contrast.yaml` has `timestamp: "#FFAA00"`

4. Backwards compatibility:
   - Existing theme files without `timestamp` field load successfully
   - Missing timestamp color falls back to `TextMuted` style

**Dependencies**: None

---

### Task 2: Timestamp Injection in Output View

**Description**: Implement timestamp detection, caching, and display in agent tab output.

**Acceptance Criteria**:
1. `internal/tui/output_view.go` - Add timestamp cache:
   - `OutputView` struct has `lineTimestamps map[string]time.Time` field initialized in constructors
   - Cache key format: `fmt.Sprintf("%d:%d", agentIssueNumber, lineIndex)`

2. `internal/tui/output_view.go` - Phase transition detection:
   - Implement `detectPhaseTransition(line string) bool` function
   - Detects lines starting with `>` (agent narrative markers)
   - Detects workflow keywords: "delegate", "reading", "checking", "pushing", "create", "label", "quality assurance"
   - Case-insensitive matching
   - Returns `true` if line indicates phase boundary

3. `internal/tui/output_view.go` - Timestamp formatting:
   - Implement `formatTimestamp(t time.Time) string` returning format: `"[YYYY-MM-DD HH:MM:SS]"`
   - Uses Go time format: `"[2006-01-02 15:04:05]"`

4. `internal/tui/output_view.go` - Modify `refreshContent()`:
   - For each agent output line, check if phase transition detected
   - If phase transition AND no cached timestamp for this line:
     - Store `time.Now()` in `lineTimestamps` cache with key `<issue>:<lineIndex>`
   - When rendering lines:
     - If cached timestamp exists for line, prepend `formatTimestamp(timestamp) + " "`
     - Apply `styles.Timestamp` styling to timestamp prefix only
     - Keep original line content unstyled (or use existing styles)
   - Preserve existing scroll position logic (no changes to viewport manipulation)

5. Display verification:
   - Timestamps appear only on phase boundary lines
   - Timestamps are styled distinctly using `styles.Timestamp`
   - Timestamps remain fixed across multiple redraws
   - Scroll position preserved when timestamps added
   - Line wrapping accounts for timestamp prefix width

6. Isolation verification:
   - Log files at `.howmux/logs/issue-*.log` contain no timestamps
   - Only TUI display shows timestamps
   - Output capture mechanism unchanged

**Dependencies**: Task 1 (requires Timestamp style to be available)

---

### Task 3: Integration Testing and Validation

**Description**: Comprehensive testing of timestamp display across various scenarios.

**Acceptance Criteria**:
1. **Manual testing with live agent**:
   - Start watcher: `watch start`
   - Create test issue with `howmux` label
   - Open agent tab when agent spawns
   - Verify timestamps appear at workflow phase boundaries
   - Verify timestamp format matches `[YYYY-MM-DD HH:MM:SS]`

2. **Timestamp fixation test**:
   - Scroll agent output up/down
   - Resize terminal window
   - Switch between tabs
   - Return to agent tab
   - Verify timestamps remain unchanged (no drift)

3. **Log file isolation test**:
   - Agent completes workflow
   - Read `.howmux/logs/issue-<number>.log`
   - Verify no `[YYYY-MM-DD HH:MM:SS]` patterns in log file
   - Verify log contains only raw agent output

4. **Scroll preservation test**:
   - Scroll to middle of agent output
   - Wait for new output (phase transition detected)
   - Verify viewport position unchanged
   - Verify timestamp appears at phase boundary without force-scroll

5. **Theme compatibility test**:
   - Switch themes: `theme default`, `theme light`, `theme high-contrast`
   - Verify timestamp color changes according to theme
   - Verify no crashes with old theme files (backwards compatibility)

6. **Performance test**:
   - Agent outputs 100+ lines rapidly
   - Verify timestamp detection doesn't cause lag
   - Verify viewport remains responsive during output flood

7. **Edge cases**:
   - Agent with no phase transitions (pure output stream)
   - Multiple agents in parallel
   - Agent that completes instantly
   - Empty agent output

**Dependencies**: Task 2 (requires timestamp injection implementation)

## Validation Commands

```bash
# Build the project
task build

# Run the binary
./howmux

# Inside TUI:
watch start
status

# Test theme switching:
theme default
theme light
theme high-contrast

# Verify log file isolation:
cat .howmux/logs/issue-<number>.log | grep -E '\[[0-9]{4}-[0-9]{2}-[0-9]{2}'
# Expected: no matches (exit code 1)

# Check for phase transition patterns in logs (should NOT have timestamps):
cat .howmux/logs/issue-<number>.log | grep -i "delegate\|reading\|checking"
# Expected: matches without timestamp prefixes
```

## Implementation Notes

### Phase Transition Detection Keywords

Based on `.kiro/agents/krew-lead-prompt.md`, detect these patterns:
- Lines starting with `>` (agent narrative output)
- Contains: "delegate", "delegat" (captures delegating, delegated)
- Contains: "reading", "read spec", "read issue"
- Contains: "checking", "check", "qa loop", "quality assurance"
- Contains: "pushing", "push branch", "push changes"
- Contains: "create pr", "creating pr", "label done", "label failed"

### Timestamp Cache Key Design

Key format: `fmt.Sprintf("%d:%d", issueNumber, lineIndex)`

Why this format?
- Issue number: Isolates timestamps per agent (multiple agents can run simultaneously)
- Line index: Unique identifier within agent's output stream
- Simple string concatenation avoids complex composite keys
- Easy to debug/inspect in memory during development

### Scroll Position Preservation

Existing implementation already preserves scroll position by avoiding `viewport.GotoBottom()` in `refreshContent()`. The comment states:
```go
// Intentionally do not force-scroll here; preserve the user's scroll position.
```

No changes needed to scroll logic. Timestamp injection happens during line rendering, which doesn't trigger viewport position changes.

### Backwards Compatibility Strategy

**Theme Files**:
- New `timestamp` field is optional in theme validation
- `getColorOrFallback()` helper pattern (used for agent colors) applies to timestamp
- Missing timestamp color falls back to `TextMuted` style
- Users with old theme files see muted gray timestamps (sensible default)

**Configuration**:
- No config changes required
- Feature is always-on (no toggle)
- No migration needed

## Non-Goals

- Configurable timestamp format (hardcoded `[YYYY-MM-DD HH:MM:SS]`)
- Timestamp toggle option (always enabled)
- Persisting timestamps to disk
- Timestamps in main tab (only agent tabs)
- Relative timestamps ("5 minutes ago")
- Timezone customization (uses local system time)

## Risk Analysis

### Performance Risk: Low
- Timestamp detection is simple string contains check (O(n) per line, where n = line length)
- Timestamp cache is unbounded map, but agent output is capped at 1000 lines by OutputCapture
- Worst case: 1000 timestamps * 24 bytes per time.Time = ~24KB per agent
- Mitigation: Cache is scoped to OutputView lifecycle, garbage collected when agent tab closes

### Compatibility Risk: Low
- Theme system already has optional field pattern (`agent_success`, `agent_fail`)
- Fallback mechanism prevents crashes with old themes
- No breaking changes to existing APIs

### User Experience Risk: Low
- Timestamps are visually distinct (muted color)
- Only appear at phase boundaries (not every line)
- Preserve scroll position (no disruptive jumps)
- Worst case: User sees extra info they can ignore

Closes #30
