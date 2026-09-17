# Design Specification: Update All Agent Models to claude-sonnet-5

**Issue**: #78  
**Title**: Update all agent models to claude-sonnet-5  
**Closes**: #78

## Solution Approach

This is a **sweeping mechanical change** that updates the `model` field in all six agent configuration JSON files from their current values (claude-sonnet-4 or claude-sonnet-4.5) to `claude-sonnet-5`. The change must be applied to both:

1. **Live agent configs** in `.kiro/agents/` (the active configs used by the system)
2. **Template copies** in `cmd/howmux/templates/kiro/agents/` (embedded templates for `howmux init/update`)

This is a simple, atomic change with no architectural implications or concurrency concerns. The modification affects only the `model` field in JSON configuration files—no code, prompts, or logic changes.

### Current State

| Agent | Current Model | Target Model |
|-------|--------------|--------------|
| krew-lead.json | claude-sonnet-4 | claude-sonnet-5 |
| architect.json | claude-sonnet-4.5 | claude-sonnet-5 |
| builder.json | claude-sonnet-4.5 | claude-sonnet-5 |
| validator.json | claude-sonnet-4 | claude-sonnet-5 |
| documenter.json | claude-sonnet-4 | claude-sonnet-5 |
| planner.json | claude-sonnet-4.5 | claude-sonnet-5 |

### Key Constraints

- **Preserve all other settings**: Do not modify any other fields in the JSON files (tools, resources, descriptions, etc.)
- **Maintain valid JSON**: Ensure all files remain syntactically valid JSON after modification
- **Template synchronization**: Live and template copies must be updated identically (except local-only `mcpServers`/`@creds-agent` entries which remain live-only per builder-conventions)
- **No prompt changes**: The `.md` prompt files are NOT modified—only the `.json` config files

## Relevant Files

### Files to Modify (Live Agents)
- `.kiro/agents/krew-lead.json`
- `.kiro/agents/architect.json`
- `.kiro/agents/builder.json`
- `.kiro/agents/validator.json`
- `.kiro/agents/documenter.json`
- `.kiro/agents/planner.json`

### Files to Sync (Template Copies)
- `cmd/howmux/templates/kiro/agents/krew-lead.json`
- `cmd/howmux/templates/kiro/agents/architect.json`
- `cmd/howmux/templates/kiro/agents/builder.json`
- `cmd/howmux/templates/kiro/agents/validator.json`
- `cmd/howmux/templates/kiro/agents/documenter.json`
- `cmd/howmux/templates/kiro/agents/planner.json`

### Verification Scripts
- `scripts/compare-templates.go` — JSON-aware comparison tool used by `task sync:check`
- `Taskfile.yml` — defines `sync:check` task

## Team Orchestration

This is a single-task implementation with no dependencies or parallelization opportunities. One builder agent can complete the entire update atomically.

**Task Execution Order**:
1. Task 1 (atomic): Update all 12 JSON files (6 live + 6 template) and verify

## Step-by-Step Task Breakdown

### Task 1: Update All Agent Model Fields to claude-sonnet-5

**Acceptance Criteria**:

1. **Live agent configs updated** — All 6 files in `.kiro/agents/` have `"model": "claude-sonnet-5"`:
   - `krew-lead.json`: `claude-sonnet-4` → `claude-sonnet-5`
   - `architect.json`: `claude-sonnet-4.5` → `claude-sonnet-5`
   - `builder.json`: `claude-sonnet-4.5` → `claude-sonnet-5`
   - `validator.json`: `claude-sonnet-4` → `claude-sonnet-5`
   - `documenter.json`: `claude-sonnet-4` → `claude-sonnet-5`
   - `planner.json`: `claude-sonnet-4.5` → `claude-sonnet-5`
   - **Verification**: `grep -h '"model"' .kiro/agents/*.json | sort -u` returns only `"model": "claude-sonnet-5",` (with trailing comma) and `"model": "claude-sonnet-5"` (without trailing comma if last field)

2. **Template agent configs updated** — All 6 files in `cmd/howmux/templates/kiro/agents/` have `"model": "claude-sonnet-5"`:
   - Same 6 files as above, template copies updated identically
   - **Note**: Template copies do NOT include `mcpServers` or `@creds-agent` tool entries (these are live-only per builder-conventions)
   - **Verification**: `grep -h '"model"' cmd/howmux/templates/kiro/agents/*.json | sort -u` returns only `"model": "claude-sonnet-5",` and `"model": "claude-sonnet-5"`

3. **Only `model` field modified** — No other fields in any of the 12 JSON files are changed:
   - `name`, `description`, `prompt`, `resources`, `tools`, `allowedTools`, `toolsSettings`, `welcomeMessage` remain unchanged
   - Live-only `mcpServers` and `@creds-agent` tool entries remain present in live configs and absent in template configs
   - **Verification**: `git diff` shows only `"model"` field changes, no other lines modified

4. **Valid JSON structure maintained** — All 12 JSON files are syntactically valid:
   - **Verification**: `python3 -c "import json, sys; [json.load(open(f)) for f in sys.argv[1:]]" .kiro/agents/*.json cmd/howmux/templates/kiro/agents/*.json` exits 0

5. **Template sync verification passes** — `task sync:check` confirms live and template agents match (ignoring local-only entries):
   - **Verification**: `task sync:check` exits 0

6. **Completeness check** — No stray references to old model names remain in agent configs:
   - **Verification**: `grep -rn "claude-sonnet-4" .kiro/agents/ cmd/howmux/templates/kiro/agents/` returns zero matches (exit code 1 from grep)

7. **QA gates pass** — All project QA checks succeed:
   - `task fmt:check` — formatting verification
   - `task lint` — linting
   - `task sync:check` — template synchronization (redundant with #5, but explicit requirement)
   - `task test` — test suite
   - `task build` — binary compilation

**Implementation Notes**:

- Use `jq` or `python -m json.tool` to preserve JSON formatting and ensure valid syntax
- For each agent file, use a JSON-aware edit tool (not `sed`) to change only the `model` field value
- After updating all 6 live configs, copy them to templates and strip `mcpServers`/`@creds-agent` entries from template copies (or manually edit templates to mirror only the model field change)
- Run `task sync:check` before running other QA gates to catch template drift early
- The grep verification command expects exit code 1 (no matches found) as success—this is the expected behavior for "no stray references"

**Dependencies**: None (standalone atomic task)

## Validation Commands

Run these commands in sequence to verify the implementation:

```bash
# 1. Verify all live agent configs use claude-sonnet-5
grep -h '"model"' .kiro/agents/*.json | sort -u
# Expected: Only "model": "claude-sonnet-5" (with/without trailing comma)

# 2. Verify all template agent configs use claude-sonnet-5
grep -h '"model"' cmd/howmux/templates/kiro/agents/*.json | sort -u
# Expected: Only "model": "claude-sonnet-5" (with/without trailing comma)

# 3. Verify no stray old model references
grep -rn "claude-sonnet-4" .kiro/agents/ cmd/howmux/templates/kiro/agents/
# Expected: No output (exit code 1 = no matches found)

# 4. Verify all JSON files are valid
python3 -c "import json, sys; [json.load(open(f)) for f in sys.argv[1:]]" \
  .kiro/agents/*.json cmd/howmux/templates/kiro/agents/*.json
# Expected: No output, exit code 0

# 5. Run all QA gates
task fmt:check   # Formatting check
task lint        # Linting
task sync:check  # Template synchronization
task test        # Test suite
task build       # Binary compilation
# Expected: All tasks exit 0
```

## Mechanical Change Surface Enumeration

This is a mechanical change (version bump of the model identifier). The complete affected surface is enumerated above in the acceptance criteria.

**Surface Coverage**:
- ✅ Source code: N/A (no Go code affected, only JSON configs)
- ✅ Configuration files: All 6 live agent JSON configs (enumerated explicitly)
- ✅ Template-synced files: All 6 template agent JSON copies (enumerated explicitly)
- ✅ Test files: N/A (agent config changes do not require test updates)
- ✅ Documentation: N/A (this is an internal model version bump, not a user-facing change)
- ✅ CI/Build workflows: N/A (no workflow changes needed)

**Completeness verification**: The grep command in acceptance criterion #6 serves as the completeness check—if it finds any `claude-sonnet-4` references after the change, those are stray references that must be addressed.

## Success Criteria

Implementation is complete when:
1. All 12 JSON files (6 live + 6 template) have `"model": "claude-sonnet-5"`
2. No other fields in any JSON file are modified
3. All JSON files remain syntactically valid
4. `task sync:check` passes (live and template match, ignoring local-only entries)
5. All QA gates pass: `task fmt:check`, `task lint`, `task sync:check`, `task test`, `task build`
6. No stray `claude-sonnet-4` references remain in agent config files
