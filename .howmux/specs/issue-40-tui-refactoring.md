# TUI Refactoring: Separate UI and Business Logic

**Closes #40**

## Problem Statement

The current TUI code exhibits classic mixing of concerns:

1. **UI Rendering mixed with Business Logic**: The main `model` struct in `tui.go` handles both Bubbletea view rendering AND business operations (agent management, watcher control, command execution, session management)

2. **State Management Scattered**: Application state is spread across multiple structs without clear ownership:
   - Watcher state accessed directly by view layer
   - Agent manager called from rendering paths
   - Session state mixed with view state
   - Tab focus states managed in multiple places

3. **Testing Difficulties**: 
   - Cannot test business logic without Bubbletea infrastructure
   - Cannot test UI rendering separately from agent/watcher logic
   - No clear interfaces for mocking dependencies

4. **Concurrency Concerns**: Direct access to shared state from render loop creates potential race conditions (as noted in architect instructions)

5. **Command Handler Complexity**: `commands.go` mixes command parsing, validation, execution, and UI updates

## Solution Approach

Implement a **layered architecture** with clear separation between:

### Layer 1: Core Business Logic (New `internal/core` package)
- **Application State Manager**: Single source of truth for app state
- **Command Service**: Pure business logic for all commands
- **Agent Service**: Wrapper around agent.Manager with business rules
- **Watcher Service**: Wrapper around watcher.Watcher with business rules
- **Session Service**: Session lifecycle management

### Layer 2: UI State Management (Refactored TUI)
- **View Models**: Transform business state to renderable UI state
- **UI Controllers**: Handle user input, delegate to services, update views
- **Presentation Logic**: Focus, layout, styling, overlays

### Layer 3: UI Components (Enhanced existing components)
- **Composable Widgets**: Footer, tabs, overlays as independent components
- **Event Handlers**: Mouse/keyboard input transformed to domain events

### Key Architectural Decisions

**1. Introduce Application Service Layer**
- Services encapsulate all business logic
- Return results as data structures (not tea.Cmd)
- No awareness of Bubbletea or rendering
- Easy to test, mock, and reason about

**2. View Models for State Transformation**
- Business state → UI state transformation explicit
- Immutable snapshots for rendering
- No direct access to services from render paths

**3. Command Pattern for User Actions**
- All user intentions expressed as commands
- Commands executed by services
- Results returned as events
- UI updates in response to events

**4. Concurrency-Safe State Access**
- All shared state accessed through accessor methods
- RWMutex protection where needed
- Clear goroutine boundaries documented
- Render loop never directly accesses mutable state

## Relevant Files

### Files to Create (New `internal/core` package)

- `internal/core/services.go` - Application service definitions and interfaces
- `internal/core/app_state.go` - Central application state manager
- `internal/core/command_service.go` - Command execution business logic
- `internal/core/agent_service.go` - Agent lifecycle and query logic
- `internal/core/watcher_service.go` - Watcher control logic
- `internal/core/session_service.go` - Session management logic
- `internal/core/events.go` - Domain event types
- `internal/core/view_models.go` - UI state transformation logic

### Files to Create (TUI Layer)

- `internal/tui/controller.go` - Main UI controller coordinating services and view
- `internal/tui/ui_state.go` - UI-specific state (focus, overlays, layout)
- `internal/tui/command_handler.go` - Transform user input to domain commands
- `internal/tui/event_mapper.go` - Map domain events to UI updates

### Files to Modify

- `internal/tui/tui.go` - Refactor to thin view layer, delegate to controller
- `internal/tui/commands.go` - Extract business logic to service layer
- `internal/tui/footer.go` - Decouple from watcher, use view model
- `internal/tui/tab_manager.go` - Pure UI concern, consume view models
- `internal/tui/planning_tab.go` - Separate planning logic from rendering
- `internal/tui/agent_tab.go` - Separate agent queries from rendering

### Files to Reference (No changes needed)

- `internal/agent/manager.go` - Already has clean interface, wrap in service
- `internal/watcher/watcher.go` - Already has clean interface, wrap in service
- `internal/session/manager.go` - Already has clean interface, wrap in service

## Team Orchestration

This refactoring follows the **Strangler Fig pattern** to allow incremental migration:

### Phase Approach (All in One PR)

**Task 1: Foundation** - Core services and interfaces (no UI changes)
- Can be implemented and tested independently
- No coupling to existing TUI code

**Task 2: Service Integration** - Wire services into existing TUI
- Services exist alongside direct calls
- Both paths work during transition
- Can be tested incrementally

**Task 3: Command Migration** - Move command logic to services
- One command at a time
- Old path removed once new path works
- Tests validate equivalence

**Task 4: View Model Introduction** - Transform business state to UI state
- Render path uses view models
- Old direct access deprecated
- Concurrent access eliminated

**Task 5: UI Controller Extraction** - Complete separation
- Controller owns service orchestration
- Model becomes pure view state
- Clean layering achieved

All tasks contribute to complete issue resolution in one PR. Tasks 1-2 can be implemented in parallel since they don't depend on each other. Tasks 3-5 must run sequentially.

## Step-by-Step Task Breakdown

### Task 1: Create Core Service Layer

**Acceptance Criteria**:
1. `internal/core/services.go` defines service interfaces:
   ```go
   type CommandService interface
   type AgentService interface  
   type WatcherService interface
   type SessionService interface
   ```
2. `internal/core/app_state.go` implements central state manager with RWMutex protection
3. All service implementations are stateless or internally synchronized
4. Services return data structures, never `tea.Cmd` or UI types
5. Unit tests verify service behavior without Bubbletea dependencies
6. **Concurrency**: All access to AppState fields protected by RWMutex
7. **Concurrent test**: `TestAppStateConcurrentAccess` exercises state mutations from multiple goroutines with `-race` flag

**Dependencies**: None (can run first)

**Verification**:
```bash
cd internal/core && go test -v -race ./...
go build ./internal/core
```

### Task 2: Implement Domain Events System

**Acceptance Criteria**:
1. `internal/core/events.go` defines all domain event types:
   - `AgentStartedEvent`, `AgentStoppedEvent`
   - `WatcherStartedEvent`, `WatcherStoppedEvent`  
   - `CommandExecutedEvent`, `CommandFailedEvent`
   - `SessionCreatedEvent`, `SessionRestoredEvent`
2. Events are immutable value objects with timestamp
3. Event types support marshaling for testing/debugging
4. Services emit events instead of side effects
5. **No UI types** in event definitions (pure domain)

**Dependencies**: Task 1

**Verification**:
```bash
cd internal/core && go test -v ./events_test.go
```

### Task 3: Extract Command Business Logic to CommandService

**Acceptance Criteria**:
1. All command handlers in `commands.go` extracted to `internal/core/command_service.go`
2. Each command method has signature: `Execute<Command>(ctx context.Context, args) (result, error)`
3. No direct calls to agent.Manager, watcher.Watcher - all go through services
4. Command validation logic moved to service layer
5. Original `handleWatch`, `handleStatus`, `handleStop`, etc. become thin wrappers
6. Unit tests for each command without TUI dependencies
7. Integration test: Execute command via service, verify state change and event emission

**Dependencies**: Tasks 1, 2

**Verification**:
```bash
cd internal/core && go test -v -run TestCommandService
# Verify old commands.go still compiles and links
cd internal/tui && go test -v -run TestHandleWatch
```

### Task 4: Introduce View Models for State Transformation

**Acceptance Criteria**:
1. `internal/core/view_models.go` defines UI state structures:
   - `StatusViewModel` - for status overlay
   - `FooterViewModel` - for footer display
   - `TabListViewModel` - for tab header rendering
2. View models are immutable snapshots (no pointers to mutable state)
3. Transformation functions: `AppState → ViewModel` are pure functions
4. Footer.go refactored to consume `FooterViewModel` instead of direct watcher access
5. **Concurrency**: All ViewModel creation uses RWMutex.RLock() to safely read AppState
6. **Concurrent test**: `TestViewModelCreationUnderLoad` creates view models while state mutates

**Dependencies**: Task 1

**Verification**:
```bash
cd internal/core && go test -v -run TestViewModel
cd internal/tui && go test -v -run TestFooterViewModel
```

### Task 5: Create UI Controller Layer

**Acceptance Criteria**:
1. `internal/tui/controller.go` implements `Controller` struct
2. Controller owns references to all services
3. Controller provides methods: `HandleCommand`, `HandleEvent`, `GetViewModel`
4. Controller translates between Bubbletea messages and domain commands
5. Controller applies domain events to UI state
6. Main `model` in `tui.go` delegates all business logic to controller
7. No direct service calls from `model.Update()`

**Dependencies**: Tasks 1-4

**Verification**:
```bash
cd internal/tui && go test -v -run TestController
# Integration test: Full command flow through controller
cd internal/tui && go test -v -run TestControllerIntegration
```

### Task 6: Refactor Main Model to Pure View Layer

**Acceptance Criteria**:
1. `model` struct in `tui.go` contains only UI state (focus, layout, overlays)
2. All agent/watcher/session state removed from `model`
3. `model.Update()` calls controller, receives events, updates UI
4. `model.View()` uses view models from controller
5. Render path **never** directly accesses services or shared state
6. All tests pass: `cd internal/tui && go test -v ./...`
7. Manual verification: App behaves identically to before refactor

**Dependencies**: Task 5

**Verification**:
```bash
cd internal/tui && go test -v ./...
./howmux # Manual smoke test of all commands
```

### Task 7: Migrate Tab System to View Models

**Acceptance Criteria**:
1. `TabManager` consumes tab view models instead of direct agent state
2. Agent tab rendering uses `AgentViewModel` (snapshot of agent state)
3. Planning tab state transformed through view models
4. **Concurrency**: Tab rendering never calls agent.Manager methods directly
5. **Concurrent test**: `TestTabRenderingUnderAgentChurn` renders tabs while agents start/stop
6. All tab tests pass with refactored implementation

**Dependencies**: Tasks 4, 6

**Verification**:
```bash
cd internal/tui && go test -v -race -run TestTab
```

### Task 8: Document Concurrency Boundaries

**Acceptance Criteria**:
1. `internal/core/ARCHITECTURE.md` created documenting:
   - Service layer guarantees (thread-safe, stateless-or-synchronized)
   - AppState locking strategy
   - Goroutine boundaries (render loop, command handlers, background services)
   - View model immutability contract
2. Code comments on all RWMutex usage explaining what's protected
3. README section: "Architecture - Separation of Concerns"
4. Verification: All `-race` tests pass

**Dependencies**: Tasks 1-7 (documents final state)

**Verification**:
```bash
go test -race ./internal/core/...
go test -race ./internal/tui/...
grep -r "RWMutex" internal/core/ | wc -l  # Should be > 0
test -f internal/core/ARCHITECTURE.md
```

### Task 9: Integration Testing and Validation

**Acceptance Criteria**:
1. Full end-to-end test: Start app, execute all commands, verify behavior
2. Performance test: Refactored code has no significant regression (<5% slower)
3. Race detector: `go test -race ./...` passes for all packages
4. Manual testing checklist completed:
   - All commands work (watch, status, stop, plan, etc.)
   - All tab types render correctly
   - Overlays display properly
   - Footer shows correct state
   - Focus management works
   - Exit cleanup succeeds
5. Code coverage report: Core services ≥80%, UI layer ≥60%

**Dependencies**: Tasks 1-8

**Verification**:
```bash
go test -race -v ./...
go test -coverprofile=coverage.out ./internal/core/... ./internal/tui/...
go tool cover -html=coverage.out
# Manual testing: Run through test plan
./howmux
```

## Validation Commands

```bash
# Verify core services compile and have no dependencies on TUI
cd internal/core && go build

# Verify core services are fully tested
cd internal/core && go test -v -race -cover ./...

# Verify TUI layer compiles
cd internal/tui && go build

# Verify TUI tests pass
cd internal/tui && go test -v -race ./...

# Verify full integration
go test -v -race ./...

# Verify no race conditions under load
go test -race -count=10 ./internal/core/...
go test -race -count=10 ./internal/tui/...

# Build final binary
go build -o howmux ./cmd/howmux

# Manual verification
./howmux  # Execute full command sequence

# Code coverage check
go test -coverprofile=coverage.out ./internal/core/... ./internal/tui/...
go tool cover -func=coverage.out | grep total
```

## Mechanical Change Surface: None

This is not a mechanical rename/move, but an architectural refactoring. The change surface is:

1. **New package**: `internal/core/` (8 new files)
2. **New files in TUI**: 4 new files (controller, ui_state, command_handler, event_mapper)
3. **Modified TUI files**: 6 files refactored (tui.go, commands.go, footer.go, tab_manager.go, planning_tab.go, agent_tab.go)
4. **Test additions**: Comprehensive unit and integration tests for all layers

## Benefits Achieved

### Testability
- **Before**: Cannot test command logic without full TUI setup
- **After**: Services tested independently with standard Go test patterns

### Maintainability  
- **Before**: Business logic scattered across UI code
- **After**: Clear layers with single responsibility

### Concurrency Safety
- **Before**: Render loop directly accessing mutable shared state
- **After**: Immutable view models, explicit locking, documented boundaries

### Code Reusability
- **Before**: Command logic tightly coupled to TUI
- **After**: Services can be used from CLI, API, or other interfaces

### Debugging
- **Before**: Hard to trace flow from input to state change
- **After**: Clear path: Input → Command → Service → Event → UI Update

## Migration Safety

The strangler fig approach ensures:
1. **Old code continues working** during transition
2. **Each task independently verifiable** with tests
3. **Progressive replacement** of old patterns
4. **Rollback possible** at each task boundary
5. **No big-bang cutover** - incremental confidence building

## Performance Considerations

Potential overhead from layering:
- View model creation on each render
- Event allocation and transformation
- Additional indirection through services

Mitigation:
- View models are lightweight structs (no deep copies)
- Events pooled if profiling shows allocation pressure
- Services inlined by compiler in hot paths
- Benchmark tests validate <5% regression

## Future Enhancements Enabled

This refactoring enables:
1. **Headless mode** - Run services without TUI for automation
2. **Alternative UIs** - Web UI, CLI, mobile using same services
3. **Event sourcing** - Record and replay all domain events
4. **Plugin system** - Custom commands via service extension points
5. **Better error handling** - Typed errors from services vs UI string messages
