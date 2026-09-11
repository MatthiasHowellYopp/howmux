# Design Specification: Add Timestamps to Agent Tab Output

Closes #30

## Solution Approach

Add display-only timestamps to agent tab output by detecting workflow phase boundaries in the output stream and injecting formatted timestamps at rendering time. The solution is purely a display-layer enhancement with zero impact on log files or output capture.

### Key Architectural Decisions

1. **Detection at Display Time**: Timestamp injection happens in `internal/tui/output_view.go` during the `refreshContent()` rendering pass, not during capture or logging
2. **Pattern-Based Detection**: Use heuristic pattern matching to identify workflow phase headers from krew-lead output
3. **Read-Only Pattern**: No modifications to `output_capture.go` or agent stdout/stderr streams
4. **Styling Integration**: Add `Timestamp` style to theme system for visual distinction
5. **Performance First**: Detection must be O(n) with the number of output lines, with minimal regex overhead

### Workflow Phase Detection

The krew-lead agent emits recognizable phase markers in its output. Common patterns include:

- Lines starting with `>` containing workflow keywords: "Reading", "Delegat", "Spawn", "Check", "Push", "Creat", "Label", "Request"
- QA-specific markers: "Discover", "Validat", "QA", "quality"
- Completion markers: "successfully", "completed", "finished"

Detection criteria:
- Line starts with `>` (workflow marker convention)
- Contains one of the workflow keywords (case-insensitive)
- OR line matches standard agent delegation patterns like "Delegating to [agent-name]"

### Timestamp Format

Format: `[YYYY-MM-DD HH:MM:SS]`
Example: `[2026-09-11 10:59:26]`

- Brackets for visual containment
- ISO-style date for clarity
- 24-hour time format
- Space separator between date and time
- Fixed width for vertical alignment

### Visual Design

```
[2026-09-11 10:59:26] > Reading issue #30 from repo...
  Issue details loaded
  Title: Add timestamps to agent tab output

[2026-09-11 10:59:31] > Delegating to architect agent...
  Architect spec generation started
  Writing spec to .howmux/specs/issue-30-timestamps.md

[2026-09-11 11:02:14] > Reading architect's spec...
  Spec loaded: 127 lines
```

Timestamp appears at the start of the line (before the workflow marker), styled distinctly to separate it visually from the workflow content.

## Relevant Files

### Files to Modify

1. **internal/tui/output_view.go**
   - Modify `refreshContent()` to detect and inject timestamps
   - Add helper function `injectTimestamps(lines []string) []string`
   - Add pattern detection logic for workflow phases

2. **internal/tui/styles.go**
   - Add `Timestamp` style field to `Styles` struct
   - Initialize in `NewStyles()` using new theme color

3. **internal/config/themes.go**
   - Add `Timestamp` field to `Theme.Colors` struct
   - Add to color validation in `validateTheme()`
   - Add to default theme in `getDefaultTheme()`

4. **.howmux/themes/*.yaml** (all theme files)
   - Add `timestamp: "<color>"` field to each theme's colors section
   - Use muted/secondary colors to avoid visual dominance

### Files to Review (No Changes)

- **internal/agent/manager.go** — Output capture mechanism (untouched)
- **internal/agent/output_capture.go** — Ring buffer storage (untouched)
- **.kiro/agents/krew-lead-prompt.md** — Reference for workflow phase patterns

## Team Orchestration

This is a single-agent implementation task with no dependencies. Builder can execute all changes in one pass.

Task execution order:
1. Theme system updates (config/themes + theme YAML files)
2. Style system updates (tui/styles.go)
3. Output view updates (tui/output_view.go with timestamp injection logic)
4. Manual verification of timestamp display in agent tabs

No sub-task parallelization needed — changes are sequential and interdependent.

## Step-by-Step Task Breakdown

### Task 1: Extend Theme System for Timestamp Color

**Acceptance Criteria**:
1. Add `Timestamp string` field to `Theme.Colors` struct in `internal/config/themes.go`
2. Add `timestamp` to the `colorFields` map in `validateTheme()` function
3. Add `Timestamp: "#888888"` (muted gray) to default theme in `getDefaultTheme()`
4. All theme YAML files under `.howmux/themes/` updated with `timestamp: "<color>"`:
   - `default.yaml`: `timestamp: "#888888"` (muted gray, secondary importance)
   - `high-contrast.yaml`: `timestamp: "#BBBBBB"` (match text_muted for consistency)
   - Any other theme files discovered
5. Theme validation passes for all updated themes
6. **Verification**: `go build ./cmd/howmux && ./howmux --version` succeeds (no compilation errors)

**Dependencies**: None

---

### Task 2: Add Timestamp Style to TUI Style System

**Acceptance Criteria**:
1. Add `Timestamp lipgloss.Style` field to `Styles` struct in `internal/tui/styles.go`
2. Initialize `Timestamp` style in `NewStyles()` function:
   ```go
   Timestamp: lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Colors.Timestamp)),
   ```
3. Style applies the theme's timestamp color without bold/italic/underline
4. **Verification**: `go build ./cmd/howmux` succeeds with no compilation errors

**Dependencies**: Task 1 (Theme.Colors.Timestamp must exist)

---

### Task 3: Implement Timestamp Injection in Output View

**Acceptance Criteria**:
1. Add helper function `isWorkflowPhase(line string) bool` to `internal/tui/output_view.go`:
   - Returns `true` if line starts with `>` AND contains workflow keywords (case-insensitive): "reading", "delegat", "spawn", "check", "push", "creat", "label", "request", "discover", "validat", "qa", "quality", "successfully", "completed", "finished"
   - Returns `false` otherwise
   - Uses efficient string prefix check before full pattern matching

2. Add helper function `injectTimestamps(lines []string, styles *Styles) []string` to `internal/tui/output_view.go`:
   - Takes input lines and styles
   - Returns new slice with timestamps prepended to workflow phase lines
   - For each line:
     - If `isWorkflowPhase(line)` returns `true`:
       - Generate timestamp: `time.Now().Format("2006-01-02 15:04:05")`
       - Prepend styled timestamp: `styles.Timestamp.Render("[" + timestamp + "]") + " " + line`
     - Otherwise, return line unchanged
   - Preserves original line order and content

3. Modify `refreshContent()` in `internal/tui/output_view.go`:
   - After extracting `agentOutput` lines (line ~147-153), call `injectTimestamps()`:
     ```go
     agentOutput = ov.injectTimestamps(agentOutput, ov.styles)
     ```
   - Timestamp injection happens AFTER filtering by agent prefix and BEFORE wrapping/indenting

4. Import `time` package at top of file if not already present

5. **Scroll Position Preservation**: No changes needed to scroll logic — `refreshContent()` already preserves viewport position (line ~186 comment confirms this)

6. **Verification**: 
   - `go build ./cmd/howmux` succeeds
   - `go test ./internal/tui -v` passes
   - Manual test: Start watcher, observe agent tab shows timestamps on workflow phase lines
   - Log files at `.howmux/logs/issue-*.log` contain NO timestamps (display-only confirmation)

**Dependencies**: Task 2 (Styles.Timestamp must exist)

---

### Task 4: Manual Verification and Testing

**Acceptance Criteria**:
1. Build the binary: `go build ./cmd/howmux`
2. Start a test issue workflow:
   ```bash
   # Create a test issue or use existing open issue
   ./howmux
   watch start
   ```
3. Switch to an agent tab (F2 or `[` / `]` navigation)
4. Verify timestamps appear on workflow phase lines:
   - Format matches `[YYYY-MM-DD HH:MM:SS]`
   - Timestamps appear at the start of lines containing workflow keywords
   - Timestamps are styled with the theme's timestamp color
   - No timestamps appear on non-workflow output lines
5. Verify log files contain NO timestamps:
   ```bash
   cat .howmux/logs/issue-*.log | grep '\[20[0-9][0-9]-'
   # Should return empty (no timestamps in log files)
   ```
6. Verify scroll position is preserved when new timestamped lines are added
7. Test with multiple themes:
   ```
   theme default
   theme high-contrast
   ```
   Confirm timestamp color changes appropriately

**Dependencies**: Task 3 (Implementation complete)

---

## Validation Commands

### Compilation Verification
```bash
go build ./cmd/howmux
./howmux --version
```

### Unit Test Verification
```bash
go test ./internal/tui -v
go test ./internal/config -v
```

### Theme Validation
```bash
# Verify all themes load without errors
./howmux
theme default
theme high-contrast
# Repeat for any other themes discovered
```

### Display-Only Verification
```bash
# Start watcher and monitor agent output
./howmux
watch start

# In another terminal, verify log files have NO timestamps
cat .howmux/logs/issue-*.log | grep -c '\[20[0-9][0-9]-'
# Should output: 0
```

### Pattern Detection Verification
```bash
# Create a simple test to verify workflow phase detection
# Run in go test or as a temporary verification script
go test -run TestWorkflowPhaseDetection ./internal/tui -v
```

## Concurrency Analysis

**No concurrency concerns introduced.**

This change operates entirely in the display layer during the rendering pass, which runs on the main TUI goroutine. The `refreshContent()` method already safely reads agent output via the manager's lock-guarded `GetOutputLines()` method.

No new cross-goroutine access is introduced:
- Timestamp injection is pure transformation of already-fetched lines
- No shared state modification
- No new goroutines spawned
- All operations are synchronous within the render loop

## Performance Considerations

### Expected Performance Impact

- **Timestamp injection**: O(n) with number of output lines
- **Pattern detection**: O(m) with line length (prefix check + keyword search)
- **Typical line count**: 100-500 lines per agent
- **Typical line length**: 40-120 characters

### Performance Budget

- Maximum acceptable overhead: <5ms per render cycle
- Pattern matching is lightweight (simple prefix + substring checks, no heavy regex)
- String concatenation for timestamp prepending is minimal

### Optimization Strategy

- Early exit in `isWorkflowPhase()` if line doesn't start with `>`
- Keyword check uses `strings.Contains()` with lowercase conversion (no regex compilation overhead)
- Timestamp generation happens only for detected workflow lines (~5-10 per agent session)

No performance testing infrastructure changes needed — impact is negligible for typical workloads.

## Security Considerations

No security impact. This change:
- Does not modify log files (read-only display transformation)
- Does not expose sensitive data (timestamps are local system time)
- Does not introduce injection vulnerabilities (timestamps are generated, not parsed from input)
- Does not affect agent isolation or command execution

## Future Enhancement Opportunities (Out of Scope)

1. **User-configurable timestamp format** — Allow format customization via config
2. **Relative timestamps** — Show elapsed time since previous phase instead of absolute time
3. **Phase duration tracking** — Calculate and display duration between phases
4. **Filtering by phase** — Allow users to filter output to specific workflow phases

These are deferred to future issues if user feedback indicates demand.
