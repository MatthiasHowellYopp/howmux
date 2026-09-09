# Design Specification: Add Watcher Status to Footer

**Issue:** #14 — Add watcher status to footer  
**Closes:** #14

## Solution Approach

Enhance the footer display system to show real-time watcher status information (active/inactive state, monitored repository, and polling interval) in the base info section. This requires adding a reference to the `Watcher` instance in `FooterManager` so it can query watcher state when rendering the footer.

### Architectural Pattern

The existing architecture already follows a clean dependency injection pattern:
- `model` (in `internal/tui/tui.go`) owns both `watcher` and `footerManager` instances
- `NewFooterManager` constructor already receives `config` and other dependencies
- The footer rendering system uses `renderBaseInfo()` to format information shown on all tabs

The solution extends this pattern by:
1. Adding `watcher *watcher.Watcher` as a new field in `FooterManager` struct
2. Passing the watcher instance during `NewFooterManager` construction
3. Querying `watcher.Running()` method in `renderBaseInfo()` to determine active/inactive state
4. Formatting watcher status with config data (repo, poll interval) already available

### Design Rationale

**Why pass watcher to FooterManager?**
- Consistent with existing pattern: `FooterManager` already receives `config`, `autocompleteInput`, `tabManager`
- Single responsibility: FooterManager's job is to render status information; it needs access to the sources of that information
- No circular dependencies: `watcher` doesn't depend on `FooterManager`, so the reference is safe
- Real-time updates: Footer automatically reflects current watcher state on each render without event plumbing

**Alternative approaches considered (and rejected):**
- Event-based updates: Overkill for simple state query; adds complexity without benefit
- State duplication in model: Violates single source of truth; introduces sync bugs
- Polling via callback: Unnecessarily indirect when direct reference is safe and idiomatic

### Status Display Format

**When watcher is active:**
```
watcher: active (matthiashowellyopp/howmux, 1m0s)
```

**When watcher is inactive:**
```
watcher: inactive
```

**Complete footer example (active watcher):**
```
theme: cyberpunk | watcher: active (matthiashowellyopp/howmux, 1m0s) | Ctrl+Y copy · Ctrl+C quit
```

### Visual Integration

The watcher status will be inserted between the theme information and keyboard shortcuts in the base info line. This positioning:
- Groups related status information together (theme + watcher status)
- Keeps the action shortcuts (copy/quit) at the end where they're currently expected
- Maintains visual balance with existing `|` separators

## Relevant Files

### Files to Modify

1. **`internal/tui/footer.go`** — Core footer rendering logic
   - Add `watcher *watcher.Watcher` field to `FooterManager` struct
   - Update `NewFooterManager` constructor signature to accept watcher parameter
   - Modify `renderBaseInfo()` method to query watcher state and format status string

2. **`internal/tui/tui.go`** — TUI model and initialization
   - Update `newModel` function to pass `w *watcher.Watcher` to `NewFooterManager` constructor
   - No other changes required (model already owns both watcher and footerManager)

### Files Referenced (no changes needed)

3. **`internal/watcher/watcher.go`** — Watcher state tracking
   - Already provides `Running() bool` method for state queries
   - `started` boolean field tracks active/inactive state

4. **`internal/config/config.go`** — Configuration structure
   - Already provides `Repo string` and `PollInterval time.Duration` fields
   - FooterManager already has access via existing `config` field

## Step-by-Step Task Breakdown

### Task 1: Update FooterManager Structure and Constructor

**File:** `internal/tui/footer.go`

**Acceptance Criteria:**
- Add `watcher *watcher.Watcher` field to `FooterManager` struct (after existing `config` field)
- Update `NewFooterManager` function signature to accept `watcher *watcher.Watcher` parameter
- Store watcher reference in the returned `FooterManager` instance

**Implementation Details:**
- Insert new field: `watcher *watcher.Watcher` in the struct definition
- Update constructor signature: `func NewFooterManager(styles *Styles, config *config.Config, autocompleteInput *AutocompleteInput, tabManager *TabManager, watcher *watcher.Watcher) *FooterManager`
- Assign watcher in constructor: `watcher: watcher,`

**Dependencies:** None (can run in parallel with Task 2 planning)

---

### Task 2: Update TUI Model to Pass Watcher to FooterManager

**File:** `internal/tui/tui.go`

**Acceptance Criteria:**
- Modify `newModel` function to pass `w *watcher.Watcher` argument to `NewFooterManager` call
- No other changes required in tui.go (watcher already available in scope)

**Implementation Details:**
- Locate the line: `footerManager := NewFooterManager(styles, cfg, autocompleteInput, tabManager)`
- Update to: `footerManager := NewFooterManager(styles, cfg, autocompleteInput, tabManager, w)`

**Dependencies:** Task 1 must complete first (changes FooterManager constructor signature)

---

### Task 3: Implement Watcher Status Rendering in renderBaseInfo

**File:** `internal/tui/footer.go`

**Acceptance Criteria:**
- Query watcher state using `fm.watcher.Running()` method
- Format status string based on watcher state:
  - When running: `watcher: active (<repo>, <interval>)` using `fm.config.Repo` and `fm.config.PollInterval`
  - When not running: `watcher: inactive`
- Insert watcher status between theme and keyboard shortcuts in the returned string
- Maintain clean visual separation with `|` separators
- Handle nil watcher gracefully (defensive programming: if `fm.watcher == nil`, skip watcher status)

**Implementation Details:**

Replace the current `renderBaseInfo()` method:

```go
func (fm *FooterManager) renderBaseInfo() string {
	return fmt.Sprintf("theme: %s | Ctrl+Y copy · Ctrl+C quit", fm.config.Theme)
}
```

With the enhanced version:

```go
func (fm *FooterManager) renderBaseInfo() string {
	baseInfo := fmt.Sprintf("theme: %s", fm.config.Theme)
	
	// Add watcher status if watcher is available
	if fm.watcher != nil {
		var watcherStatus string
		if fm.watcher.Running() {
			watcherStatus = fmt.Sprintf("watcher: active (%s, %s)", 
				fm.config.Repo, 
				fm.config.PollInterval)
		} else {
			watcherStatus = "watcher: inactive"
		}
		baseInfo = fmt.Sprintf("%s | %s", baseInfo, watcherStatus)
	}
	
	// Append keyboard shortcuts
	return fmt.Sprintf("%s | Ctrl+Y copy · Ctrl+C quit", baseInfo)
}
```

**Edge Cases Handled:**
- Nil watcher check prevents panic if FooterManager is used in contexts without a watcher
- PollInterval.String() provides human-readable duration format (e.g., "1m0s", "5m0s")

**Dependencies:** Tasks 1 and 2 must complete first (FooterManager needs watcher reference)

---

## Validation Commands

After implementation, verify the feature works correctly:

### 1. Build and Run
```bash
# Build the project
task build

# Run kiro-krew
./kiro-krew
```

### 2. Verify Inactive State
When the TUI starts, the footer should display:
```
theme: <current-theme> | watcher: inactive | Ctrl+Y copy · Ctrl+C quit
```

### 3. Start Watcher and Verify Active State
In the kiro-krew prompt:
```
kiro-krew> watch start
```

The footer should immediately update to:
```
theme: <current-theme> | watcher: active (owner/repo, <poll-interval>) | Ctrl+Y copy · Ctrl+C quit
```

### 4. Stop Watcher and Verify State Update
```
kiro-krew> watch stop
```

The footer should immediately revert to:
```
theme: <current-theme> | watcher: inactive | Ctrl+Y copy · Ctrl+C quit
```

### 5. Verify Status Persists Across Tabs
- Open a planning tab: `plan sample task`
- Verify watcher status is visible in the footer on the planning tab
- Switch back to main tab: `F2` or `[`/`]`
- Verify watcher status remains consistent

### 6. Edge Case: Missing Watcher Reference
The implementation includes defensive nil checks, but this can be manually verified by:
- Temporarily commenting out the watcher parameter in tests/manual verification
- Footer should render without panic, simply omitting watcher status

## Team Orchestration

This is a straightforward, single-developer feature with linear dependencies:

1. **Task 1** modifies `FooterManager` structure (foundation)
2. **Task 2** updates the caller to match new signature (integration)
3. **Task 3** implements the rendering logic (feature completion)

**Execution Flow:**
- Task 1 must complete before Task 2 (signature change breaks compilation otherwise)
- Tasks 1 and 2 must complete before Task 3 (Task 3 requires watcher field to exist and be populated)
- All three tasks contribute to complete issue resolution in a single PR

**Builder Context:**
- No parallel execution opportunity (strict linear dependency chain)
- Each task is small and focused (10-20 lines of code total)
- Complete implementation achievable in single pass
- No external dependencies or configuration changes required

## Testing Considerations

### Manual Testing (Primary Verification)

The acceptance criteria focus on visual verification and real-time updates, which are best validated through manual testing:

1. **Visual Rendering:** Confirm footer displays correct format in both states
2. **Real-time Updates:** Verify footer updates immediately when watcher state changes
3. **Cross-tab Consistency:** Ensure status displays correctly on all tab types
4. **Layout Integrity:** Confirm footer doesn't overflow or break with long repo names

### Automated Testing (Future Enhancement)

While manual testing is sufficient for this feature, future automated tests could cover:

```go
// Unit test example for renderBaseInfo logic
func TestFooterManager_RenderBaseInfo_WatcherActive(t *testing.T) {
	// Setup mock watcher that returns true for Running()
	// Setup config with known repo and poll interval
	// Call renderBaseInfo()
	// Assert output contains "watcher: active (owner/repo, 1m0s)"
}

func TestFooterManager_RenderBaseInfo_WatcherInactive(t *testing.T) {
	// Setup mock watcher that returns false for Running()
	// Call renderBaseInfo()
	// Assert output contains "watcher: inactive"
}

func TestFooterManager_RenderBaseInfo_NilWatcher(t *testing.T) {
	// Setup FooterManager with nil watcher
	// Call renderBaseInfo()
	// Assert output doesn't panic and renders theme/shortcuts only
}
```

**Testing Scope:** For this PR, manual testing via the validation commands above is sufficient. Automated tests are optional enhancements that can be added in future work if desired.

## Implementation Notes

### Why This Approach is Complete

This solution fully addresses all acceptance criteria in a single PR:

1. ✅ **Footer displays watcher status** — Task 3 implements the required format
2. ✅ **Real-time updates** — Footer re-renders on every frame; watcher state is queried live via `Running()` method
3. ✅ **Correct information source** — Uses `Watcher.Running()`, `config.Repo`, `config.PollInterval` as specified
4. ✅ **Footer integration** — Status appears in base info section with `|` separators as required
5. ✅ **Visible across all tabs** — Base info section already renders on all tab types

### No Follow-up Work Required

- No configuration changes needed
- No database/state migrations
- No external service integrations
- No documentation updates beyond this spec (feature is self-evident in UI)
- No breaking changes to existing functionality

### Risk Assessment

**Low Risk Changes:**
- Adding a field and constructor parameter: Compile-time safety ensures all call sites are updated
- Reading watcher state: `Running()` is a simple boolean getter with no side effects
- Defensive nil check: Prevents runtime panics in unexpected contexts

**No Known Edge Cases:**
- Long repository names are naturally handled by the terminal's text wrapping
- Poll intervals format consistently via Go's `time.Duration.String()` method
- Watcher state is a simple boolean with no intermediate states

This implementation requires no special error handling, retry logic, or fallback behavior.
