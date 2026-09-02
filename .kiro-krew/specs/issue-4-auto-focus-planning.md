# Design Specification: Auto-focus Message Input When Creating New Planning Tab

**Issue:** #4
**Status:** Ready for Implementation
**Closes:** #4

## Problem Statement

When users create a new planning tab using the `plan` command, focus remains on the footer command line instead of automatically switching to the planning tab's message input. This creates unnecessary friction requiring an extra Tab keypress before the user can start typing their planning message.

## Current Behavior

1. User types `plan [description]` in the footer command line
2. New planning tab is created via `handlePlan()` in `internal/tui/commands.go`
3. `switchActiveTab()` is called to switch to the new tab (line 276 in commands.go)
4. `switchActiveTab()` restores focus state from `m.tabFocusStates[activeTab.ID()]`
5. Since the tab is new, no entry exists in `tabFocusStates`, so it defaults to `FocusTargetFooter` (line 1237-1240 in tui.go)
6. User must press Tab to transfer focus to the message input

## Expected Behavior

1. User types `plan [description]` in the footer command line
2. New planning tab is created and switched to
3. **Focus automatically switches to the message input**
4. User can immediately start typing their planning message

## Solution Approach

The fix requires overriding the default focus state for newly created planning tabs. After `switchActiveTab()` is called in `handlePlan()`, explicitly set the focus to `FocusTargetMessage` and store this state in the centralized `tabFocusStates` map before returning.

### Architecture Decision

Use the existing focus state management infrastructure (`tabFocusStates` map) rather than introducing special-case logic. This ensures consistency with the Model A focus design where:
- The footer command line is the always-available default
- Tab key toggles between footer and message input
- Focus state is preserved when switching between tabs

## Relevant Files

### Files to Modify

1. **internal/tui/commands.go** (`handlePlan` function)
   - Lines 265-284: Add focus override after `switchActiveTab()` call
   - Set `m.tabFocusStates[planningTab.ID()] = FocusTargetMessage`
   - Call `planningTab.RestoreFocusState(FocusTargetMessage)` to apply the focus
   - Update `m.input.SetFocus(false)` since footer no longer has focus

### Files Referenced (No Changes Required)

1. **internal/tui/tui.go** (`switchActiveTab` function)
   - Lines 1200-1260: Existing focus state management
   - Uses `tabFocusStates` map to restore previous focus state
   - Defaults to `FocusTargetFooter` for new tabs (line 1237-1240)

2. **internal/tui/planning_tab.go**
   - Lines 858-873: `RestoreFocusState` method implementation
   - Handles focus application based on `FocusTarget` value
   - Returns appropriate tea.Cmd for focus changes

3. **internal/tui/focus_state.go**
   - Lines 1-24: `FocusTarget` type definition and constants
   - `FocusTargetFooter` and `FocusTargetMessage` constants

## Implementation Steps

### Task 1: Override Default Focus State for New Planning Tabs
**Dependencies**: None

**Acceptance Criteria**:
- After `switchActiveTab()` is called in `handlePlan()`, add code to:
  1. Store `FocusTargetMessage` in `m.tabFocusStates[planningTab.ID()]`
  2. Call `planningTab.RestoreFocusState(FocusTargetMessage)` to apply focus to message input
  3. Update footer input state with `m.input.SetFocus(false)`
  4. Collect any commands returned from `RestoreFocusState` and include in the final batch

**Implementation Details**:
```go
// In handlePlan(), after line 276 (switchActiveTab call):
// Override default focus state for newly created planning tab
m.tabFocusStates[planningTab.ID()] = FocusTargetMessage

// Apply focus to the message input
messageFocusCmd := planningTab.RestoreFocusState(FocusTargetMessage)

// Update footer input focus state
m.input.SetFocus(false)

// Combine all commands
allCmds := []tea.Cmd{focusCmd}
if messageFocusCmd != nil {
    allCmds = append(allCmds, messageFocusCmd)
}

return m, tea.Batch(allCmds...)
```

## Validation

### Acceptance Criteria Verification

1. ✅ **New Planning Tab Focus**
   - Command: `plan test planning session`
   - Verify: Message input has focus immediately, cursor visible in input area
   - Verify: User can type without pressing Tab first

2. ✅ **Description Provided**
   - Command: `plan create a new feature`
   - Verify: Description is added as first user message
   - Verify: Focus is on message input, ready for next message

3. ✅ **Tab Toggle Behavior**
   - Create new planning tab
   - Verify: Focus starts on message input
   - Press Tab
   - Verify: Focus moves to footer command line
   - Press Tab again
   - Verify: Focus returns to message input

4. ✅ **Existing Planning Tab Focus**
   - Create planning tab A, switch to console
   - Switch back to planning tab A
   - Verify: Focus state is preserved (should be last known state, not forced to message input)

5. ✅ **Hotkey Toggle Behavior**
   - Create planning tab, switch to console
   - Press Ctrl+Alt+P
   - Verify: Returns to planning tab with preserved focus state

6. ✅ **Other Tab Types Unchanged**
   - Switch to Main tab
   - Verify: Focus on footer (existing behavior)
   - Open log viewer tab
   - Verify: Focus on footer (existing behavior)
   - Open agent output tab
   - Verify: Focus on footer (existing behavior)

### Test Commands

```bash
# Manual testing sequence
1. Start kiro-krew
2. Run: plan test focus behavior
3. Observe: Cursor should be in message input, not footer
4. Type a message without pressing Tab first
5. Verify: Message is entered correctly

# Test Tab toggle
6. Press Tab
7. Verify: Focus moves to footer (status bar shows focus indicator change)
8. Press Tab again
9. Verify: Focus returns to message input

# Test existing tab preservation
10. Switch to Main tab (Ctrl+Alt+P or clicking)
11. Switch back to planning tab
12. Verify: Focus state is preserved (should be on footer from step 6 if you didn't switch focus back)

# Test other tab types
13. Run: log
14. Verify: Focus on footer (not changed)
15. Run: status
16. Verify: Overlay opens, focus on footer when closed
```

## Constraints and Considerations

### Constraints

1. **Only affects newly created planning tabs** - Existing planning tabs (loaded from session) must preserve their saved focus state
2. **Must not break Model A focus design** - Footer remains the always-available command line
3. **Tab toggle must continue working** - Users can still press Tab to switch between message input and footer

### Edge Cases

1. **Rapid planning tab creation** - Each new tab gets message focus independently
2. **Description provided vs. empty** - Focus behavior is identical regardless
3. **Maximum planning tabs reached** - Error message shown, no focus state created
4. **ACP connection failure** - Error message shown, no planning tab created, no focus state affected

### Design Trade-offs

**Chosen Approach**: Override focus state after tab creation
- ✅ Uses existing focus state infrastructure
- ✅ Consistent with Model A design
- ✅ Minimal code changes
- ✅ No special-case logic in `switchActiveTab()`

**Alternative Considered**: Add parameter to `switchActiveTab()` to specify initial focus
- ❌ More invasive change affecting all tab types
- ❌ Requires changes to multiple call sites
- ❌ Complicates the tab switching interface

**Alternative Considered**: Set default in `NewPlanningTab()`
- ❌ Would affect all planning tabs, including restored sessions
- ❌ Breaks preservation of focus state for existing tabs
- ❌ Violates separation of concerns (tab creation vs. UI focus management)

## Success Metrics

1. **Zero extra keypresses** - Users can start typing immediately after `plan` command
2. **Preserved behavior** - Existing tab types and planning tab features remain unchanged
3. **Clean implementation** - Uses existing focus state infrastructure without special cases

## Implementation Notes

### Critical Details

1. **Order of operations matters**:
   - `switchActiveTab()` must be called first to activate the tab
   - Then override the focus state in `tabFocusStates` map
   - Then apply focus via `RestoreFocusState()`
   - Finally update footer focus with `SetFocus(false)`

2. **Command batching**:
   - `switchActiveTab()` returns a cmd (likely nil or focus-related)
   - `RestoreFocusState()` returns a cmd (focus command for message input)
   - Both must be batched together in the return value

3. **Focus state storage**:
   - Store in `m.tabFocusStates` map using tab ID as key
   - This ensures the state is preserved when switching tabs later
   - Integrates seamlessly with existing focus state management

### Implementation Checklist

- [ ] Add focus state override after `switchActiveTab()` call in `handlePlan()`
- [ ] Store `FocusTargetMessage` in `tabFocusStates` map
- [ ] Call `RestoreFocusState()` on the planning tab
- [ ] Update footer input focus state
- [ ] Batch all commands in return statement
- [ ] Test all acceptance criteria
- [ ] Verify no regression in existing behaviors

## Related Code Patterns

### Focus State Management Pattern (from tui.go)

```go
// Capture focus state before switching
if currentTab := m.tabManager.GetActiveTab(); currentTab != nil {
    currentFocus := currentTab.CaptureFocusState()
    m.tabFocusStates[currentTab.ID()] = currentFocus
}

// Restore focus state after switching
if activeTab := m.tabManager.GetActiveTab(); activeTab != nil {
    previousFocus, exists := m.tabFocusStates[activeTab.ID()]
    if !exists {
        previousFocus = FocusTargetFooter // Default for new tabs
    }
    
    if cmd := activeTab.RestoreFocusState(previousFocus); cmd != nil {
        cmds = append(cmds, cmd)
    }
    
    if previousFocus == FocusTargetFooter {
        m.input.SetFocus(true)
        cmds = append(cmds, m.input.Focus())
    } else {
        m.input.SetFocus(false)
    }
}
```

This pattern should be adapted in `handlePlan()` to set the initial focus state for the newly created planning tab.

## Testing Strategy

### Unit Testing (Not Required)

The focus state management is integration-level behavior that requires the full TUI stack. Manual testing is sufficient for this UI-focused change.

### Manual Testing (Required)

See "Validation Commands" section above for comprehensive manual test sequence.

### Regression Testing Focus Areas

1. **Tab switching** - Ensure focus state is preserved when switching between tabs
2. **Tab toggle (Tab key)** - Ensure toggling between footer and message input works
3. **Hotkey toggle (Ctrl+Alt+P)** - Ensure planning mode toggle preserves state
4. **Other tab types** - Ensure Main, Agent, and Log tabs are unaffected
5. **Session persistence** - Ensure restored planning tabs don't get force-focused

## Documentation Updates

### User-Facing Changes

Update help text or documentation if it mentions the Tab key requirement for new planning tabs. Current help text in `handleHelp()` is generic and doesn't need updates.

### Code Comments

Add a brief comment in `handlePlan()` explaining why the focus state is explicitly set for new planning tabs:

```go
// Override default focus state for newly created planning tab.
// New planning tabs should start with message input focused
// so users can type immediately without pressing Tab first.
```

## Timeline and Effort Estimate

**Estimated Effort**: 15-30 minutes
- Code change: 5 lines of code
- Testing: 10-20 minutes for comprehensive manual testing
- Documentation: 2 minutes for inline comment

**Complexity**: Low
- Single-file change
- Uses existing infrastructure
- Clear acceptance criteria
- No dependencies on other work
