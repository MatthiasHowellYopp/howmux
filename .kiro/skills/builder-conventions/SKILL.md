---
name: builder-conventions
description: Project-specific conventions and patterns for the builder agent. Maintains synchronization between template and live project files.
---

# Builder Conventions

Project-specific conventions, patterns, and best practices for the builder agent to follow during implementation tasks.

## Mandatory Template Synchronization

**Critical for Self-Hosting**: Kiro Krew uses itself to build itself. Live project files and embedded templates must stay in sync so that `kiro-krew init` and `kiro-krew update` always deploy current configurations.

Sync is **one-way** (live → template). CI enforces this via `task sync:check` in the Validate PR workflow.

### Sync Mappings

| Live Path | Template Path |
|-----------|---------------|
| `.kiro/agents/*.json` | `cmd/kiro-krew/templates/kiro/agents/` |
| `.kiro/agents/*.md` | `cmd/kiro-krew/templates/kiro/agents/` |
| `.kiro-krew/scripts/*.sh` | `cmd/kiro-krew/templates/kiro-krew/scripts/` |
| `.kiro-krew/themes/*.yaml` | `cmd/kiro-krew/templates/kiro-krew/themes/` |
| `.kiro-krew/evals/fixtures/*` | `cmd/kiro-krew/templates/kiro-krew/evals/fixtures/` |
| `.kiro-krew/evals/rubrics/*` | `cmd/kiro-krew/templates/kiro-krew/evals/rubrics/` |
| `.kiro-krew/evals/cases/**/*` | `cmd/kiro-krew/templates/kiro-krew/evals/cases/` |

### Exclusion Patterns

**Never sync `*-conventions` skills** — they are project-specific and must NOT be distributed in templates.

**Never sync local-only agent config into templates** — some entries in the live
`.kiro/agents/*.json` are intentionally local-only and must NOT ship in the
embedded templates that reach end users. In particular the **`creds-agent` MCP
server** and the **`@creds-agent`** tool grant vend developer-machine
credentials and belong only in the live agents. `task sync:check` compares
agent JSON with a JSON-aware tool that ignores these named entries (see
`scripts/compare-templates.go`), so a live-only creds-agent block does not count
as drift — do not "fix" a nonexistent drift by copying it into the templates.

### Sync Commands

Run the appropriate commands after modifying any template-synchronized files.

> **Agent JSON:** do not blindly `cp` live agents over the templates — that
> would carry local-only entries (e.g. the creds-agent MCP block) into the
> shipped artifact. Copy the file, then strip any local-only `mcpServers` and
> `@creds-agent`/local-only tool grants from the template copy, or edit the
> template to mirror only the intended change. `task sync:check` will pass as
> long as the non-local-only content matches.

```bash
# Agent files (JSON configs and prompt files)
cp .kiro/agents/*.json cmd/kiro-krew/templates/kiro/agents/
cp .kiro/agents/*.md cmd/kiro-krew/templates/kiro/agents/

# Scripts
cp .kiro-krew/scripts/*.sh cmd/kiro-krew/templates/kiro-krew/scripts/

# Themes
cp .kiro-krew/themes/*.yaml cmd/kiro-krew/templates/kiro-krew/themes/

# Evals (excluding results directory)
cp .kiro-krew/evals/fixtures/* cmd/kiro-krew/templates/kiro-krew/evals/fixtures/
cp .kiro-krew/evals/rubrics/* cmd/kiro-krew/templates/kiro-krew/evals/rubrics/
mkdir -p cmd/kiro-krew/templates/kiro-krew/evals/cases/
cp -r .kiro-krew/evals/cases/* cmd/kiro-krew/templates/kiro-krew/evals/cases/
```

### Verification

Run `task sync:check` to verify all template-synchronized files match. This is the same check CI runs — if it passes locally, CI will pass.

```bash
task sync:check
```

If verification fails, re-run the sync commands above for the affected file category, then re-run `task sync:check`.

## Workflow Integration

When completing tasks that modify template-synchronized files, follow this sequence:

1. **Implement** — complete the assigned task
2. **Sync** — run the appropriate sync commands from above
3. **Verify** — run `task sync:check` (task cannot be marked complete if this fails)
4. **QA** — run `task lint` and `task test`
5. **Complete** — create sentinel file documenting results

### Sentinel File Requirements

Include sync verification status in sentinel files:

```markdown
## Task Complete

**Template Sync**: ✅ VERIFIED (or "N/A - no template files modified")
**QA Results**:
- Linting: ✅ PASS
- Tests: ✅ PASS
- Sync Verification: ✅ PASS

**Sync Commands Used**:
- `cp .kiro/agents/builder.json cmd/kiro-krew/templates/kiro/agents/`
```

## Implementation Patterns

### Quality Assurance
- Run ALL discovered QA commands before completion
- Use QA discovery results from `.kiro-krew/artifacts/qa-tools.md`
- Document specific QA command sources (CI vs build tool)

### File Modifications
- Preserve existing formatting and structure
- Maintain JSON validity for configuration files
- Verify template sync before task completion
- Document changes in sentinel files including sync status

### Error Recovery
- Address validator feedback from `.kiro-krew/artifacts/validator-<issue>.md`
- Focus on specific failing commands identified by validator
- Include sync verification in error recovery process
- Document how feedback was incorporated

## Project Standards

### Code Quality
- Follow existing code style and conventions
- Use project's configured linting and formatting tools
- Ensure all tests pass (100% pass rate required)

### Documentation
- Update relevant docs when adding features
- Follow project's documentation format
- Include usage examples for new functionality

## Go Concurrency Rules

### Mutex-Guarded Fields

A field protected by a `sync.Mutex` or `sync.RWMutex` must **only** be accessed while holding the appropriate lock. This applies to:
- Direct field access within the struct's methods
- **Accessor methods** (getters/setters) called from other goroutines

**Pattern**: If a struct has a mutex and fields guarded by it, every method that reads or writes those fields must acquire the lock first.

**Example** (Watcher):
```go
type Watcher struct {
    mu      sync.RWMutex
    started bool         // Guarded by mu
    // ...
}

// ✅ Correct: accessor acquires lock
func (w *Watcher) Running() bool {
    w.mu.RLock()
    defer w.mu.RUnlock()
    return w.started
}

// ❌ Wrong: accessor reads without lock
func (w *Watcher) Running() bool {
    return w.started  // RACE with Start/Stop
}
```

### Cross-Goroutine Access

When adding a feature that reads shared state from a **different goroutine** than the writers, verify the accessor method is lock-guarded:
- **TUI render path** reading agent/watcher/session state → accessor must hold lock
- **Command handler** reading state written by background loop → accessor must hold lock
- **Status display** reading dynamically updated fields → accessor must hold lock

**If no lock-guarded accessor exists**, either:
1. Add locking to the existing accessor (if it's unlocked), or
2. Create a new lock-guarded accessor method

Do **not** directly access struct fields from outside the struct, even if they are exported — use accessor methods so locking stays encapsulated.

### Concurrent Test Requirement

When a change adds a **new concurrent access path** (e.g., wiring the footer to read watcher state):
1. Add a test that exercises the accessor **concurrently** with the writers (e.g., `Start()`/`Stop()`)
2. The test must run the accessor in a loop while another goroutine modifies the state
3. Verify the test passes with `-race`: `go test -race ./path/to/package`

**Why**: A passing race detector with **no concurrent test** proves nothing — the detector only flags races it observes at runtime. If your test doesn't exercise the concurrent path, `-race` stays silent even when a race exists.

**Example**:
```go
func TestRunningConcurrentAccess(t *testing.T) {
    w := New(cfg, mgr)
    done := make(chan struct{})

    // Goroutine: flip state
    go func() {
        for {
            select {
            case <-done:
                return
            default:
                w.Start()
                time.Sleep(1 * time.Millisecond)
                w.Stop()
                time.Sleep(1 * time.Millisecond)
            }
        }
    }()

    // Main goroutine: read concurrently
    for i := 0; i < 1000; i++ {
        _ = w.Running()  // Would race without RLock
    }

    close(done)
}
```

This test **would fail** under `-race` if `Running()` didn't hold the lock, proving the test exercises the race-prone path.
