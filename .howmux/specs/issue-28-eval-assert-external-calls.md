# Design Specification: Eval Assert External Calls in Scoring

**Issue:** #28  
**Closes:** #28  
**Dependencies:** Issue #27 (hermetic evals with external call recording)

## Solution Approach

Extend the eval system to score agents based on their external tool interactions, not just text output. This transforms evals from output-only judges to behavioral verifiers — detecting when an agent fails to call the right tool, calls it with wrong arguments, or calls unexpected tools.

The design adds three capabilities:

1. **Expected Call Declarations** — Test cases declare which external calls they expect (tool + subcommand + required argument patterns)
2. **Recorded Call Scoring** — The runner compares recorded `ExternalCalls` from #27 against expectations and contributes to the case score
3. **Multi-Service Fake Tooling** — Generalize the `gh` fake from #27 to cover `jtk` (Jira/Confluence) and a placeholder `asana`, using a uniform fake mechanism

### Key Design Decisions

- **Backward Compatible** — Cases without `ExpectedCalls` behave exactly as today (output-only scoring)
- **Service-Agnostic Faking** — Adding a new fake tool is uniform: write one bash shim script following the established pattern
- **Flexible Matching** — The matcher supports tool name, subcommand, and required argument substrings/flags (not rigid exact-match, to avoid brittleness from flag ordering)
- **Pass/Fail Contribution** — Missing expected calls or unexpected calls lower the score; satisfied expectations raise it; the discrepancy is captured in the result

### Concurrency Considerations

This change does NOT introduce cross-goroutine access to shared state. All eval execution is sequential within a single goroutine:
- Call recording happens during `invokeAgentNative` (single agent execution)
- Call log reading occurs after agent completes
- Scoring runs sequentially per test case

No locking or concurrent access patterns are introduced.

## Relevant Files

### Files to Modify

- `internal/eval/types.go` — Add `ExpectedCalls []ExpectedCall` field to `TestCase`
- `internal/eval/runner.go` — Add call-scoring logic in `evaluate()` and `evaluateProgressive()`
- `internal/eval/faketools.go` — Add `jtk` and `asana` fake scripts alongside `gh`; refactor setup to install all fakes

### Files to Create

- `.howmux/evals/cases/krew-lead/pr-creation-flow.yaml` — New case asserting PR creation + done-labeling calls
- `.howmux/evals/cases/krew-lead/jira-integration.yaml` — New case exercising Jira (`jtk`) call faking and assertion (or extend an existing case)

### Files for Reference (Not Modified)

- `.howmux/evals/cases/krew-lead/basic-orchestration.yaml` — Existing case structure reference
- `internal/eval/types.go` — Existing `ExternalCall` struct from #27
- `internal/eval/runner.go` — Existing `readCallLog()` and `invokeAgentNative()` from #27

## Team Orchestration

This is a single-developer implementation with clear separation of concerns:

1. **Type Extension (Phase 1)** — Add `ExpectedCall` matcher type and `TestCase.ExpectedCalls` field
2. **Scoring Logic (Phase 2)** — Implement call matching and scoring contribution
3. **Multi-Service Fakes (Phase 3)** — Add `jtk` and `asana` shims to fake tools setup
4. **Test Cases (Phase 4)** — Create reference cases proving the behavior

Each phase builds on the previous. Phases 1-3 are backend (no agent changes), Phase 4 is case authoring.

## Step-by-Step Task Breakdown

### Task 1: Define Expected Call Matcher Type

**Acceptance Criteria:**

1. **Matcher struct added** — `internal/eval/types.go` defines `ExpectedCall` struct with:
   - `Tool string` (e.g. `gh`, `jtk`, `asana`)
   - `Subcommand string` (e.g. `pr create`, `issue edit`, `projects list`)
   - `RequiredArgs []string` (substrings/flags that must appear in args, e.g. `Closes #`, `--add-label`)
   - YAML and JSON tags with `omitempty` for optional fields
   
2. **TestCase field added** — `TestCase` struct gains:
   ```go
   ExpectedCalls []ExpectedCall `yaml:"expected_calls,omitempty" json:"expected_calls,omitempty"`
   ```
   
3. **Backward compatible** — Existing cases without `expected_calls` continue to work (field is optional, `omitempty`)

4. **Verification:**
   ```bash
   go build ./internal/eval/
   grep -n "ExpectedCall" internal/eval/types.go
   grep -n "expected_calls" internal/eval/types.go
   ```

**Dependencies:** None

---

### Task 2: Implement Call Matching Logic

**Acceptance Criteria:**

1. **Matcher function added** — `internal/eval/faketools.go` (or new `internal/eval/callmatcher.go`) defines:
   ```go
   func MatchCall(recorded ExternalCall, expected ExpectedCall) bool
   ```
   - Returns `true` if `recorded.Tool == expected.Tool` AND subcommand matches AND all required args present
   - Subcommand match: `expected.Subcommand` appears in `recorded.Args` (e.g. `["pr", "create"]` matches subcommand `"pr create"`)
   - Required args: every substring in `expected.RequiredArgs` appears somewhere in `recorded.Args` or `recorded.RawLine`

2. **Match analysis function added:**
   ```go
   func AnalyzeCalls(recorded []ExternalCall, expected []ExpectedCall) (satisfied, missing, unexpected []string)
   ```
   - Returns lists of: satisfied expected calls, unmet expected calls, unexpected recorded calls (not matching any expectation)

3. **Unit-testable** — Functions are pure (no side effects), can be tested independently

4. **Verification:**
   ```bash
   go build ./internal/eval/
   grep -n "MatchCall" internal/eval/
   grep -n "AnalyzeCalls" internal/eval/
   ```

**Dependencies:** Task 1

---

### Task 3: Integrate Call Scoring into Runner

**Acceptance Criteria:**

1. **Scoring contribution added** — In `evaluate()` and `evaluateProgressive()` in `runner.go`, after standard criterion scoring:
   - If `tc.ExpectedCalls` is non-empty:
     - Call `AnalyzeCalls(cr.ExternalCalls, tc.ExpectedCalls)`
     - Create a synthetic `CriterionScore` for "Expected External Calls":
       - `Name: "external_calls_adherence"`
       - `MaxScore: 5` (matching other criteria scale)
       - `Deterministic: true`
       - Score calculation: `5 * (satisfied / len(expected))` if all satisfied, else deduct for missing/unexpected
     - Append this score to `cr.Scores`
     - Include discrepancy summary in `Reasoning` (which expected calls were unmet, which unexpected calls occurred)
   - If `tc.ExpectedCalls` is empty or nil: skip (no regression to existing cases)

2. **No regression** — Cases without `ExpectedCalls` score exactly as today (verified by running existing cases)

3. **Discrepancy captured** — The `CriterionScore.Reasoning` field includes:
   - Satisfied calls (e.g. `"Satisfied: gh pr create with Closes #, gh issue edit --add-label"`)
   - Missing calls (e.g. `"Missing: gh issue edit --add-label howmux-done"`)
   - Unexpected calls (e.g. `"Unexpected: gh api repos/..."`), if any

4. **Verification:**
   ```bash
   go build ./internal/eval/
   go test ./internal/eval/
   grep -n "external_calls_adherence" internal/eval/runner.go
   # Run an existing case (no ExpectedCalls) and verify it still passes:
   howmux eval krew-lead --no-sandbox
   ```

**Dependencies:** Task 2

---

### Task 4: Add jtk and asana Fakes to Setup

**Acceptance Criteria:**

1. **Fake scripts added** — `internal/eval/faketools.go` defines two new constants:
   - `fakeJtkShim` — Mimics `jtk` / `atlassian-cli` behavior (logs calls to `HOWMUX_EVAL_CALL_LOG`, returns canned JSON for common commands like `issues get`, `issues search`, `comments add`, `projects list`)
   - `fakeAsanaShim` — Placeholder fake for future Asana CLI (logs calls, returns minimal canned responses)

2. **Setup installs all fakes** — `SetupFakeTools()` writes three scripts: `gh`, `jtk` (or `atlassian-cli`), `asana`
   - All scripts have execute permissions (0755)
   - All scripts log to the same `HOWMUX_EVAL_CALL_LOG`
   - All scripts are placed in the same `toolsDir` returned by `SetupFakeTools()`

3. **Service-agnostic pattern** — Adding a new fake is a small, uniform addition:
   - Add a new `const fakeXyzShim` bash script
   - Write it to `filepath.Join(tempDir, "xyz")` in `SetupFakeTools()`
   - No changes elsewhere (the PATH injection and call logging are already service-agnostic)

4. **Documentation comment** — Add a comment in `faketools.go` explaining how to add a new fake tool (the three-step pattern above)

5. **Verification:**
   ```bash
   go build ./internal/eval/
   grep -n "fakeJtkShim" internal/eval/faketools.go
   grep -n "fakeAsanaShim" internal/eval/faketools.go
   # Inspect the temp directory during a test run to verify all three fakes are present:
   # (run with debug logging or manually check via breakpoint/print)
   ```

**Dependencies:** None (can run in parallel with Tasks 1-2)

---

### Task 5: Create Reference Test Cases

**Acceptance Criteria:**

1. **Krew-lead PR-creation case** — `.howmux/evals/cases/krew-lead/pr-creation-flow.yaml` created with:
   - `input`: orchestrate issue #42, create PR, label issue done
   - `expected_calls`:
     - Tool: `gh`, Subcommand: `pr create`, RequiredArgs: `["Closes #42"]`
     - Tool: `gh`, Subcommand: `issue edit`, RequiredArgs: `["--add-label", "howmux-done"]` (or the appropriate label from config)
   - The case scores based on whether krew-lead made those calls

2. **Jira integration case** — `.howmux/evals/cases/krew-lead/jira-integration.yaml` (or extend an existing case) with:
   - `input`: task involving Jira ticket creation or comment
   - `expected_calls`:
     - Tool: `jtk`, Subcommand: `issues get`, RequiredArgs: `["AEA-123"]` (or similar)
   - Demonstrates multi-service coverage

3. **Cases run green** — Both cases execute under hermetic mode (`--no-sandbox`) with no Docker/network:
   ```bash
   howmux eval krew-lead --no-sandbox
   # Verify pr-creation-flow and jira-integration cases pass
   ```

4. **Score reflects behavior** — A manual inspection of the `.howmux/evals/results/<timestamp>/krew-lead.json` output shows:
   - `external_calls_adherence` criterion present in scored cases
   - `Reasoning` field shows satisfied/missing/unexpected calls

**Dependencies:** Tasks 3, 4

---

### Task 6: End-to-End Verification

**Acceptance Criteria:**

1. **Build passes:**
   ```bash
   go build ./...
   go test ./internal/eval/
   ```

2. **Existing cases unchanged** — Run all agents' cases and verify no regression:
   ```bash
   howmux eval architect --no-sandbox
   howmux eval builder --no-sandbox
   howmux eval validator --no-sandbox
   howmux eval documenter --no-sandbox
   howmux eval krew-lead --no-sandbox
   # All existing cases (without expected_calls) should score as before
   ```

3. **New cases demonstrate behavior** — The two new krew-lead cases (pr-creation-flow, jira-integration) score on interactions:
   - If the agent makes the expected calls → high score
   - If the agent skips a call → lower score with missing call in reasoning
   - If the agent makes unexpected calls → flagged in reasoning

4. **Multi-service fakes present:**
   ```bash
   grep -niE "jtk|atlassian|asana" internal/eval/faketools.go
   # Verify both fake scripts are defined and installed
   ```

5. **No Docker/network required** — All hermetic cases run offline

**Dependencies:** Task 5

---

## Validation Commands

```bash
# Build and test
go build ./...
go test ./internal/eval/

# Verify new types and scoring
grep -n "ExpectedCalls" internal/eval/types.go
grep -n "external_calls_adherence" internal/eval/runner.go
grep -niE "jtk|atlassian|asana" internal/eval/faketools.go

# Run krew-lead eval and check for expected-call scoring
howmux eval krew-lead --no-sandbox

# Inspect results for the new criterion
cat .howmux/evals/results/$(ls -t .howmux/evals/results/ | head -1)/krew-lead.json | grep -A5 external_calls_adherence
```

## Out of Scope (This Issue)

- **Agent prompt changes** — This is eval infrastructure only; no agent behavior changes
- **Docker sandbox integration** — Call recording and faking work only in native mode (`--no-sandbox`); container mode does not record external calls yet (that is a separate future enhancement)
- **Real integration testing** — Evals stay hermetic by design; no live GitHub/Jira/Asana calls
- **Exact-match rigidity** — The matcher uses substring/pattern matching to avoid brittleness from flag ordering or minor argument variations

## Notes

- The fake tool pattern established in #27 (`SetupFakeTools`, PATH injection, call logging) is service-agnostic by design — this issue proves it by adding two more services with no changes to the framework
- The matcher is intentionally flexible (substring matching) to avoid flaky failures when flag ordering changes or agents add harmless extra flags
- The `external_calls_adherence` score is deterministic (no LLM judge needed) because call matching is objective
- This enables behavioral regression testing: if a prompt change causes krew-lead to stop labeling issues done, the pr-creation-flow case will catch it

## Mechanical Change Surface (Not Applicable)

This is not a mechanical change (rename/move/version-bump). It is a feature addition with localized changes to eval types, runner scoring logic, and fake tool setup.

## Writer/Reader Verification (Not Applicable)

No environment variables or symbol renames in this issue.
