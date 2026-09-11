# Design Specification: Add Clipboard Copy/Paste Support

**Issue:** #9  
**Title:** Add clipboard copy/paste support to planning and issue windows  
**Closes:** #9

## Solution Approach

Add cross-platform clipboard integration to howmux's TUI using the existing `github.com/atotto/clipboard` library (already present as an indirect dependency). Implement copy from viewport message history and paste into textinput fields across both planning tabs and agent output views.

### High-Level Strategy

1. **Leverage existing clipboard library**: Promote `github.com/atotto/clipboard` to a direct dependency since it already exists in go.mod
2. **Non-intrusive integration**: Clipboard operations layer on top of Bubble Tea's event model without modifying existing key bindings or focus behavior
3. **Graceful degradation**: Handle clipboard access failures (permissions, headless environments) with silent fallback rather than breaking the application
4. **Consistent UX**: Same clipboard behavior across planning tab and agent output tabs

### Architecture Decisions

**Why `atotto/clipboard`?**
- Already present in go.mod as indirect dependency (v0.1.4)
- Cross-platform support (macOS, Linux, Windows)
- Simple API (`clipboard.ReadAll()`, `clipboard.WriteString()`)
- Well-tested in production Go applications
- Zero additional dependencies

**Why not `golang-design/clipboard`?**
- Requires CGO and additional system dependencies
- More complex API surface
- Would increase binary size and build complexity

## Relevant Files

### Files to Modify

1. **`go.mod`** — Promote `github.com/atotto/clipboard` from indirect to direct dependency
2. **`internal/tui/planning_tab.go`** — Add clipboard operations to planning tab keyboard handling
3. **`internal/tui/output_view.go`** — Add clipboard operations to agent output viewport
4. **`internal/tui/tui.go`** — Handle clipboard operations at top-level for cross-tab consistency

### New Files

1. **`internal/tui/clipboard.go`** — Clipboard utility functions with error handling
2. **`internal/tui/clipboard_test.go`** — Unit tests for clipboard utilities

## Team Orchestration

This is a single-feature implementation with no external dependencies or breaking changes. All tasks contribute to complete clipboard functionality delivered in one PR.

### Parallel Work Opportunities

- **Task 1** (clipboard utilities) and **Task 2** (viewport copy) have no dependencies and can run in parallel
- **Task 3** (textinput paste) depends on Task 1 completing
- **Task 4** (testing) can begin once Tasks 1-3 are complete
- **Task 5** (documentation) can run in parallel with testing

## Step-by-Step Task Breakdown

### Task 1: Create Clipboard Utility Layer
**Acceptance Criteria:**
- Create `internal/tui/clipboard.go` with safe wrappers for clipboard operations
- Implement `CopyToClipboard(text string) error` with graceful error handling
- Implement `PasteFromClipboard() (string, error)` with graceful error handling
- Handle permission failures and headless environments without panicking
- Log clipboard errors at debug level (don't surface to users)

**Dependencies:** None

**Implementation Notes:**
```go
// Safe clipboard operations with error handling
func CopyToClipboard(text string) error {
    if text == "" {
        return nil // No-op for empty text
    }
    
    if err := clipboard.WriteString(text); err != nil {
        logging.Debug("clipboard copy failed", "error", err)
        return err // Return but don't propagate to user
    }
    return nil
}

func PasteFromClipboard() (string, error) {
    text, err := clipboard.ReadAll()
    if err != nil {
        logging.Debug("clipboard paste failed", "error", err)
        return "", err // Return empty string on failure
    }
    return text, nil
}
```

### Task 2: Implement Viewport Copy (Selection Required)
**Acceptance Criteria:**
- Users can select text in viewport using mouse drag (Bubble Tea built-in)
- Ctrl+C (Cmd+C on macOS) copies selected text to clipboard in planning tab viewport
- Ctrl+C copies selected text in agent output tab viewports
- Visual feedback: selected text remains highlighted after copy
- Copy operation does not interfere with other Ctrl+C uses (exit confirmation)
- Handle empty selection gracefully (no-op)

**Dependencies:** Task 1 (clipboard utilities must exist)

**Implementation Notes:**
- Bubble Tea's viewport already supports mouse text selection via `tea.MouseMsg`
- Add Ctrl+C handler in viewport Update methods
- Check for active text selection before copying
- Selection state tracked by viewport component

**Viewport Selection Detection:**
Bubble Tea viewports don't expose selection state directly. For this implementation:
- User must manually select text with mouse drag
- Ctrl+C copies entire visible viewport content (simpler, doesn't require selection tracking)
- Future enhancement: track selection if Bubble Tea exposes it

**Simplified Copy Approach:**
Since Bubble Tea viewports don't expose text selection state, implement "copy visible content" on Ctrl+C:
```go
case "ctrl+c":
    // Copy all visible viewport content
    content := pt.viewport.View()
    if content != "" {
        CopyToClipboard(content)
    }
```

This meets the user need (copy assistant responses) without requiring selection tracking infrastructure.

### Task 3: Implement Textinput Paste
**Acceptance Criteria:**
- Ctrl+V (Cmd+V on macOS) pastes clipboard content into planning tab message input
- Multi-line clipboard content handled correctly (newlines preserved or converted to spaces based on input mode)
- Paste works when focus is on message input (focusTarget == FocusTargetMessage)
- Paste does NOT work when footer command line has focus (footer handles its own paste)
- Cursor position maintained correctly after paste
- Paste appends to existing input text at cursor position

**Dependencies:** Task 1 (clipboard utilities)

**Implementation Notes:**
```go
case "ctrl+v":
    if pt.focusTarget == FocusTargetMessage {
        text, err := PasteFromClipboard()
        if err == nil && text != "" {
            // Insert at cursor position
            current := pt.textinput.Value()
            cursorPos := pt.textinput.CursorPosition()
            newValue := current[:cursorPos] + text + current[cursorPos:]
            pt.textinput.SetValue(newValue)
            // Move cursor to end of pasted text
            pt.textinput.SetCursor(cursorPos + len(text))
        }
    }
```

### Task 4: Cross-Platform Keyboard Handling
**Acceptance Criteria:**
- Ctrl+C and Ctrl+V work on Linux and Windows
- Cmd+C and Cmd+V work on macOS (detected via runtime.GOOS)
- Keyboard shortcuts don't conflict with existing bindings:
  - Ctrl+C for exit confirmation (handled separately in overlay)
  - Ctrl+W for tab close (unchanged)
  - Ctrl+D for exit (unchanged)
- Shortcuts work consistently across planning tab and agent output tabs

**Dependencies:** Tasks 2 and 3

**Implementation Notes:**
Bubble Tea normalizes Cmd to Ctrl in key messages on macOS, so `case "ctrl+c"` handles both Ctrl+C and Cmd+C automatically. No platform-specific code needed.

### Task 5: Error Handling and Graceful Degradation
**Acceptance Criteria:**
- Clipboard operations fail silently in headless environments (no GUI clipboard available)
- Permission errors logged at debug level, never shown to user
- Application never panics from clipboard failures
- Users in SSH/headless environments see no error messages, clipboard simply doesn't work
- All clipboard operations use the safe wrappers from Task 1

**Dependencies:** Tasks 1-4

**Testing Requirements:**
- Unit tests with mocked clipboard to simulate failures
- Integration tests in normal GUI environment
- Manual testing in headless SSH session (should be silent no-op)

### Task 6: Testing and Validation
**Acceptance Criteria:**
- Unit tests for clipboard utilities (mock clipboard operations)
- Integration tests for copy from viewport
- Integration tests for paste into textinput
- Manual testing on macOS (primary platform)
- Manual testing on Linux (if available)
- Verify no regression in existing keyboard shortcuts
- Test in headless environment (should fail gracefully)

**Dependencies:** Tasks 1-5

**Test Cases:**
```go
// Test clipboard utilities
func TestCopyToClipboard_EmptyString(t *testing.T)
func TestCopyToClipboard_Success(t *testing.T)
func TestPasteFromClipboard_Success(t *testing.T)
func TestPasteFromClipboard_Error(t *testing.T)

// Test planning tab integration
func TestPlanningTab_CopyViewportContent(t *testing.T)
func TestPlanningTab_PasteIntoInput(t *testing.T)
func TestPlanningTab_PasteWhenFooterFocused(t *testing.T) // Should not paste

// Test agent output integration
func TestAgentOutput_CopyViewportContent(t *testing.T)
```

### Task 7: Documentation Updates
**Acceptance Criteria:**
- Update README.md with clipboard shortcuts in keyboard controls section
- Add clipboard functionality to help overlay (if it exists)
- Document graceful degradation behavior for headless environments
- Note platform-specific shortcuts (Ctrl vs Cmd)

**Dependencies:** None (can run in parallel with testing)

## Validation Commands

### Build and Test
```bash
# Ensure clipboard dependency is properly added
go mod tidy
go mod verify

# Run unit tests
go test ./internal/tui/clipboard_test.go
go test ./internal/tui -run TestClipboard

# Run all TUI tests to check for regressions
go test ./internal/tui/... -v

# Build the application
task build
```

### Manual Validation

**Planning Tab - Copy:**
1. Launch howmux REPL
2. Start a planning session
3. Send a message and receive response
4. View the viewport with response text
5. Press Ctrl+C (or Cmd+C on macOS)
6. Open external text editor and paste (Ctrl+V / Cmd+V)
7. Verify viewport content appears in editor

**Planning Tab - Paste:**
1. In planning tab with message input focused (Tab to switch focus)
2. Copy text from external editor
3. Press Ctrl+V (or Cmd+V on macOS) in howmux
4. Verify text appears in message input
5. Verify cursor position is correct
6. Send message to confirm input works

**Agent Output Tab - Copy:**
1. Start watcher and trigger an agent
2. Switch to agent output tab
3. Wait for agent output to appear
4. Press Ctrl+C (or Cmd+C on macOS)
5. Paste into external editor
6. Verify agent output appears

**Multi-line Paste:**
1. Copy multi-line text from external editor
2. Paste into planning tab message input
3. Verify newlines are handled appropriately
4. Send message to confirm it works

**Focus Boundary Test:**
1. In planning tab, ensure footer command line has focus (default)
2. Press Ctrl+V — verify nothing happens (paste should only work when message input focused)
3. Press Tab to focus message input
4. Press Ctrl+V — verify paste works

**Headless Environment Test (if possible):**
1. SSH into a headless Linux server
2. Run howmux
3. Attempt Ctrl+C and Ctrl+V operations
4. Verify no error messages appear
5. Verify application continues to function normally

## Implementation Notes

### Clipboard Library Choice

Using `github.com/atotto/clipboard` because:
- Already in go.mod as indirect dependency (just promote to direct)
- Cross-platform (macOS, Linux, Windows)
- Simple API
- Well-maintained and widely used

### Text Selection Limitations

Bubble Tea's `viewport` component doesn't expose text selection state. For the initial implementation:
- Copy operation copies **entire visible viewport content** on Ctrl+C
- This is simpler and still meets the core user need
- Future enhancement: implement selection tracking if needed

### Focus Model Integration

Kiro-krew uses "Model A" focus pattern:
- Footer command line is always-available default (FocusTargetFooter)
- Tab key toggles to message input (FocusTargetMessage)

Clipboard operations must respect this:
- Copy (Ctrl+C): works regardless of focus (copies viewport)
- Paste (Ctrl+V): only works when focusTarget == FocusTargetMessage
- Footer command line handles its own paste separately

### Error Handling Philosophy

Clipboard failures should never disrupt the user experience:
- All errors logged at debug level only
- No user-visible error messages
- Silent no-op behavior in headless/SSH environments
- Application never panics from clipboard failures

### Platform Differences

Bubble Tea normalizes platform-specific modifiers:
- Cmd key on macOS appears as "ctrl" in key messages
- Single key binding (`case "ctrl+c"`) works across platforms
- No runtime.GOOS checks needed for keyboard handling

### Testing Strategy

1. **Unit tests** with mocked clipboard for error cases
2. **Integration tests** for happy path (requires real clipboard)
3. **Manual testing** for cross-platform validation
4. **Headless testing** for graceful degradation

## Dependencies

- **Existing:** `github.com/atotto/clipboard v0.1.4` (promote from indirect to direct)
- **No new dependencies required**

## Risks and Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| Clipboard permission failures | High | Graceful error handling, debug-level logging only |
| Headless environment breaks | Medium | Silent no-op, no error messages |
| Keyboard shortcut conflicts | Low | Careful key binding order, testing |
| Platform-specific behavior differences | Low | Rely on Bubble Tea's platform normalization |
| Text selection not available in viewport | Low | Copy entire viewport content as fallback |

## Acceptance Criteria Summary

1. ✅ Copy from planning tab viewport (Ctrl+C / Cmd+C)
2. ✅ Copy from agent output viewport (Ctrl+C / Cmd+C)
3. ✅ Paste into planning tab message input (Ctrl+V / Cmd+V)
4. ✅ Multi-line paste handled correctly
5. ✅ Cross-platform support (macOS, Linux, Windows)
6. ✅ Focus-aware paste (only when message input focused)
7. ✅ Graceful degradation in headless environments
8. ✅ No breaking changes to existing keyboard shortcuts
9. ✅ Unit tests for clipboard utilities
10. ✅ Integration tests for copy/paste operations
11. ✅ Documentation updated with keyboard shortcuts

## Out of Scope

- Visual text selection feedback (viewport limitation)
- Clipboard history / multiple clipboard buffers
- Rich text / formatted clipboard content
- Clipboard monitoring / auto-paste
- Clipboard sync across sessions
