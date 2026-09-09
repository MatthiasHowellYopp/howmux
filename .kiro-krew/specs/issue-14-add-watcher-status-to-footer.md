# Design Specification: Add Watcher Status to Footer

**Issue:** #14  
**Title:** Add watcher status to footer  
**Closes:** #14

## Problem Statement

The footer currently displays minimal status information (theme and keyboard shortcuts). Users cannot see at a glance whether the watcher is active or which repository it's monitoring without running the `status` command. This forces an extra step to check a fundamental piece of system state.

## Solution Approach

Add real-time watcher status information to the footer's base info section, visible across all tabs. The status will display:
- Active state: `watcher: active (owner/repo, interval)` 
- Inactive state: `watcher: inactive`

This will be implemented by:
1. Passing the watcher instance to `FooterManager` during initialization
2. Adding a method to format watcher status in `footer.go`
3. Integrating the formatted status into `renderBaseInfo()` with proper separator
4. Ensuring updates propagate when watcher state changes via `watch start/stop`

The implementation follows the existing footer rendering pattern and respects the theme styling system already in place.

## Architecture & Design Decisions

### Component Relationships

```
model (tui.go)
  ├─ watcher: *watcher.Watcher (already exists)
  └─ footerManager: *FooterManager
       ├─ config: *config.Config (already has access)
       └─ watcher: *watcher.Watcher (NEW - needs to be added)
```

### Key Design Points

1. **Watcher Reference in FooterManager**: The `FooterManager` struct needs a reference to the `Watcher` instance to check its `started` state. This is analogous to how it already has access to `config`.

2. **Method Addition**: A new private method `renderWatcherStatus()` will format the watcher status string consistently with existing footer patterns.

3. **Integration Point**: The `renderBaseInfo()` method is the natural integration point since it already formats the base status line shown on all tabs.

4. **Real-time Updates**: No special update mechanism needed - the footer is re-rendered on every frame via the Bubble Tea update loop, so state changes from `watch start/stop` commands will automatically reflect in the next render.

5. **Formatting Consistency**: Use the existing `|` separator pattern and the config's theme styling to maintain visual consistency.

## Relevant Files

### Files to Modify

1. **internal/tui/footer.go**
   - Add `watcher` field to `FooterManager` struct
   - Update `NewFooterManager()` constructor signature and implementation
   - Add `renderWatcherStatus()` method to format the watcher status string
   - Update `renderBaseInfo()` to include watcher status

2. **internal/tui/tui.go**
   - Update `newModel()` to pass watcher to `NewFooterManager()` constructor

3. **internal/tui/footer_test.go** (if exists)
   - Update test fixtures to provide mock watcher instance
   - Add test cases for watcher status rendering

### Files to Reference (No Changes)

- **internal/watcher/watcher.go** - Read-only access to understand the `started` field and `Running()` method
- **internal/config/config.go** - Reference for `Repo` and `PollInterval` fields
- **internal/tui/commands.go** - Reference to understand watch start/stop flow

## Team Orchestration

This is a focused single-agent task with clear boundaries:
- All changes are within the TUI layer (`internal/tui/`)
- No changes to watcher logic itself
- No changes to config structure
- The watcher package provides read-only access via `Running()` method

**Task Dependencies**: This is a self-contained feature with no external dependencies. All required state is already available through existing references.

## Step-by-Step Task Breakdown

### Task 1: Add Watcher Reference to FooterManager

**File:** `internal/tui/footer.go`

**Changes:**
1. Add `watcher` field to `FooterManager` struct:
   ```go
   type FooterManager struct {
       styles            *Styles
       config            *config.Config
       watcher           *watcher.Watcher  // NEW
       contextTracker    *ContextTracker
       autocompleteInput *AutocompleteInput
       tabManager        *TabManager
       width             int
       height            int
   }
   ```

2. Update `NewFooterManager()` constructor:
   ```go
   func NewFooterManager(styles *Styles, config *config.Config, watcher *watcher.Watcher, autocompleteInput *AutocompleteInput, tabManager *TabManager) *FooterManager {
       return &FooterManager{
           styles:            styles,
           config:            config,
           watcher:           watcher,  // NEW
           contextTracker:    NewContextTracker(),
           autocompleteInput: autocompleteInput,
           tabManager:        tabManager,
       }
   }
   ```

**Acceptance Criteria:**
- `FooterManager` struct has `watcher` field of type `*watcher.Watcher`
- Constructor signature updated to accept watcher parameter
- Constructor properly initializes the watcher field

**Dependencies:** None

---

### Task 2: Implement Watcher Status Formatting

**File:** `internal/tui/footer.go`

**Changes:**
1. Add `renderWatcherStatus()` method after `renderBaseInfo()`:
   ```go
   // renderWatcherStatus formats the watcher status for display
   func (fm *FooterManager) renderWatcherStatus() string {
       if fm.watcher == nil {
           return "watcher: unavailable"
       }
       
       if !fm.watcher.Running() {
           return "watcher: inactive"
       }
       
       // Format poll interval for display
       interval := fm.config.PollInterval.String()
       
       return fmt.Sprintf("watcher: active (%s, %s)", fm.config.Repo, interval)
   }
   ```

2. Update `renderBaseInfo()` to include watcher status:
   ```go
   // renderBaseInfo renders the base information shown on all tabs
   func (fm *FooterManager) renderBaseInfo() string {
       watcherStatus := fm.renderWatcherStatus()
       return fmt.Sprintf("%s | theme: %s | Ctrl+Y copy · Ctrl+C quit", 
           watcherStatus, fm.config.Theme)
   }
   ```

**Acceptance Criteria:**
- `renderWatcherStatus()` correctly handles nil watcher
- Active state shows: `watcher: active (owner/repo, interval)`
- Inactive state shows: `watcher: inactive`
- `renderBaseInfo()` includes watcher status as first element with proper separator
- Interval formatting uses Go's duration string format (e.g., "5m", "1m30s")

**Dependencies:** Task 1 must be complete

---

### Task 3: Wire Watcher to FooterManager in TUI Model

**File:** `internal/tui/tui.go`

**Changes:**
1. Update the `NewFooterManager()` call in `newModel()` function (around line 140):
   ```go
   // Initialize footer system
   footerManager := NewFooterManager(styles, cfg, w, autocompleteInput, tabManager)
   ```
   Note: Change from `NewFooterManager(styles, cfg, autocompleteInput, tabManager)` to include `w` (watcher parameter).

**Acceptance Criteria:**
- `newModel()` passes watcher instance to `NewFooterManager()`
- Parameter order matches updated constructor signature
- No compilation errors

**Dependencies:** Tasks 1 and 2 must be complete

---

### Task 4: Update Test Fixtures

**File:** `internal/tui/footer_test.go` (and any other test files using `NewFooterManager`)

**Changes:**
1. Update all `NewFooterManager()` calls in tests to include a mock watcher:
   ```go
   // Create mock watcher for testing
   mockWatcher := &watcher.Watcher{}  // or use a proper mock
   fm := NewFooterManager(styles, cfg, mockWatcher, autocomplete, tabManager)
   ```

2. Add test cases for watcher status rendering:
   ```go
   func TestRenderWatcherStatus(t *testing.T) {
       // Test inactive watcher
       // Test active watcher with various configs
       // Test nil watcher handling
   }
   ```

**Files to Update:**
- `internal/tui/footer_test.go`
- `internal/tui/autocomplete_overlay_validation_test.go` (line 20, 234)
- Any other test files that instantiate `FooterManager`

**Acceptance Criteria:**
- All existing tests pass with updated constructor signature
- New test cases cover active, inactive, and nil watcher scenarios
- Tests verify correct formatting of repo and interval display

**Dependencies:** Tasks 1, 2, and 3 must be complete

---

## Validation Commands

Run these commands to verify the implementation:

```bash
# Build the project
go build ./cmd/howmux

# Run tests
go test ./internal/tui/... -v

# Run the application and verify visually
./howmux
# Then in the REPL:
# 1. Check footer shows "watcher: inactive"
# 2. Run "watch start" - footer should update to show "watcher: active (owner/repo, interval)"
# 3. Run "watch stop" - footer should update back to "watcher: inactive"
# 4. Verify status appears on all tabs (switch tabs with F2 / [ / ])
```

### Expected Behavior

**Initial state (watcher inactive):**
```
theme: default | watcher: inactive | Ctrl+Y copy · Ctrl+C quit
```

**After "watch start":**
```
theme: default | watcher: active (matthiashowellyopp/howmux, 5m) | Ctrl+Y copy · Ctrl+C quit
```

**After "watch stop":**
```
theme: default | watcher: inactive | Ctrl+Y copy · Ctrl+C quit
```

The watcher status should:
- Update in real-time when commands are issued
- Appear consistently across all tabs
- Maintain visual alignment with existing footer elements
- Use the current theme's styling

## Edge Cases & Error Handling

1. **Nil Watcher**: If `footerManager.watcher` is nil, display "watcher: unavailable" to avoid panics
2. **Long Repository Names**: Use the full `owner/repo` format - the footer system already handles width management
3. **Interval Formatting**: Go's `Duration.String()` automatically formats to human-readable strings (e.g., "5m0s" → "5m")
4. **Theme Changes**: Watcher status respects theme styling via the footer's existing theme integration

## Testing Strategy

1. **Unit Tests**: Test `renderWatcherStatus()` with various watcher states and configurations
2. **Integration Tests**: Verify footer updates when watcher state changes via commands
3. **Visual Testing**: Manual verification across different tabs and theme settings
4. **Regression Testing**: Ensure existing footer functionality (planning info, theme display) remains intact

## Notes

- The implementation follows the existing pattern used for displaying theme information
- No new dependencies required - all necessary state is already available
- The Bubble Tea update loop handles real-time updates automatically
- The watcher's `Running()` method is the single source of truth for active state
- Configuration values (`Repo`, `PollInterval`) are immutable after initialization, so no sync issues
