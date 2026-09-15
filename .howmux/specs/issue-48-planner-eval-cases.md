# Design Specification: Add Planner Eval Test Cases

**Issue:** #48  
**Closes:** #48  
**Created:** 2026-09-15

## Problem Statement

The `planner` agent is the only agent with an evaluation rubric (`.howmux/evals/rubrics/planner.yaml`) but **zero test cases**. The `.howmux/evals/cases/planner/` directory does not exist. Every other agent (architect, builder, validator, documenter, krew-lead) has 5–7 test cases. As a result, planner is entirely unscored and absent from the evaluation baseline.

This gap prevents:
- Quality assurance for planner agent behavior
- Regression detection when planner prompt or conventions change
- Performance benchmarking against other agents
- Baseline establishment for future improvements

## Solution Approach

Create 5 representative test cases for the planner agent that exercise all dimensions defined in the planner rubric:

1. **requirement_clarity** — Issue requirements are clearly defined, specific, and leave no ambiguity
2. **scope_appropriateness** — Issue scope is properly bounded for a single PR
3. **acceptance_criteria_quality** — Acceptance criteria are comprehensive, testable, and provide clear completion indicators
4. **constraint_identification** — Technical constraints, dependencies, and blockers are properly identified
5. **question_clarity** — Questions posed are clear, focused, and avoid ambiguous phrasing
6. **single_question_adherence** — Responses contain only one question, avoiding multiple questions in single responses

Test cases will follow the established YAML schema used by other agents:
- `name` — kebab-case identifier
- `description` — Human-readable description of what the test validates
- `agent: planner` — Agent identifier
- `input` — Symptom-level problem/issue description for planner to analyze
- `setup` — Optional fixtures or context (file references, text blocks)
- `context` — List of contextual requirements/expectations
- `expected_output` — Description of expected planner behavior and output

All test cases will be mirrored to the template directory to maintain template synchronization.

## Relevant Files

### Files to Create

**Primary eval cases (5 files):**
- `.howmux/evals/cases/planner/root-cause-analysis.yaml`
- `.howmux/evals/cases/planner/scope-refinement.yaml`
- `.howmux/evals/cases/planner/acceptance-criteria-definition.yaml`
- `.howmux/evals/cases/planner/constraint-identification.yaml`
- `.howmux/evals/cases/planner/single-question-flow.yaml`

**Template-synced copies (5 files):**
- `cmd/howmux/templates/howmux/evals/cases/planner/root-cause-analysis.yaml`
- `cmd/howmux/templates/howmux/evals/cases/planner/scope-refinement.yaml`
- `cmd/howmux/templates/howmux/evals/cases/planner/acceptance-criteria-definition.yaml`
- `cmd/howmux/templates/howmux/evals/cases/planner/constraint-identification.yaml`
- `cmd/howmux/templates/howmux/evals/cases/planner/single-question-flow.yaml`

### Files Referenced (for pattern consistency)

- `.howmux/evals/rubrics/planner.yaml` — Rubric defining evaluation criteria
- `.howmux/evals/cases/architect/*.yaml` — Reference for YAML schema and structure
- `.howmux/evals/cases/builder/*.yaml` — Additional schema examples
- `.kiro/agents/planner-prompt.md` — Planner agent behavior and workflow
- `.kiro/skills/planner-conventions/SKILL.md` — Planner analysis methodology

## Team Orchestration

This is a straightforward file creation task with no cross-component dependencies. A single builder agent can complete all tasks sequentially:

1. Create the planner cases directory structure
2. Create all 5 test case YAML files in the primary location
3. Create the template cases directory structure
4. Mirror all 5 YAML files to the template location

No parallel execution opportunities exist since template mirroring depends on primary file creation.

## Step-by-Step Task Breakdown

### Task 1: Create Primary Planner Eval Cases Directory

**Acceptance Criteria:**
- Directory `.howmux/evals/cases/planner/` exists
- Directory has appropriate permissions (755)

**Dependencies:** None

**Validation:**
```bash
[ -d .howmux/evals/cases/planner/ ] && echo "✓ Directory exists"
```

---

### Task 2: Create Planner Test Case Files

Create 5 test case YAML files under `.howmux/evals/cases/planner/` that exercise the planner rubric dimensions.

**Acceptance Criteria:**

1. **File: `root-cause-analysis.yaml`**
   - Exercises: `requirement_clarity`, `constraint_identification`
   - Input: Symptom-level bug report (e.g., "Feature X crashes when Y happens")
   - Expected output: Planner performs root cause analysis, traces code paths, identifies underlying issue vs symptom
   - Context: Should demonstrate code tracing methodology from planner-conventions

2. **File: `scope-refinement.yaml`**
   - Exercises: `scope_appropriateness`, `question_clarity`
   - Input: Vague feature request with unclear boundaries (e.g., "Make the UI better")
   - Expected output: Planner asks ONE clarifying question to refine scope to single-PR achievability
   - Context: Should demonstrate single-question adherence (planner's critical rule)

3. **File: `acceptance-criteria-definition.yaml`**
   - Exercises: `acceptance_criteria_quality`, `requirement_clarity`
   - Input: Feature request with no success criteria (e.g., "Add export functionality")
   - Expected output: Planner drafts testable acceptance criteria with clear completion indicators
   - Context: Should show comprehensive, testable criteria definition

4. **File: `constraint-identification.yaml`**
   - Exercises: `constraint_identification`, `requirement_clarity`
   - Input: Feature request with implicit dependencies (e.g., "Add OAuth login" without mentioning existing auth system)
   - Expected output: Planner identifies technical constraints, existing system dependencies, potential blockers
   - Context: Should demonstrate dependency mapping and constraint analysis

5. **File: `single-question-flow.yaml`**
   - Exercises: `single_question_adherence`, `question_clarity`
   - Input: Ambiguous request requiring multiple clarifications (e.g., "Add analytics")
   - Expected output: Planner asks ONE focused question, waits for response, avoids bulleted question lists
   - Context: Should demonstrate prohibited patterns (multiple questions) vs proper single-question format

**File Format Requirements:**
- Follow exact YAML schema from architect/builder examples
- Include: `name`, `description`, `agent: planner`, `input`, `setup` (optional), `context`, `expected_output`
- Input should be representative planner input (symptom-level problem descriptions)
- Context should reference planner-specific behaviors from prompt/conventions
- Expected output should describe planner behavior, not just technical outcomes

**Dependencies:** Task 1 (directory must exist)

**Validation:**
```bash
# Verify all 5 files exist
for file in root-cause-analysis scope-refinement acceptance-criteria-definition constraint-identification single-question-flow; do
  [ -f .howmux/evals/cases/planner/${file}.yaml ] && echo "✓ ${file}.yaml exists" || echo "✗ ${file}.yaml missing"
done

# Verify YAML is valid
for file in .howmux/evals/cases/planner/*.yaml; do
  yamllint "$file" 2>/dev/null || python3 -c "import yaml; yaml.safe_load(open('$file'))" && echo "✓ $(basename $file) valid YAML"
done

# Verify agent field is correct
grep -l "agent: planner" .howmux/evals/cases/planner/*.yaml | wc -l | grep -q "5" && echo "✓ All files have agent: planner"
```

---

### Task 3: Create Template Directory Structure

**Acceptance Criteria:**
- Directory `cmd/howmux/templates/howmux/evals/cases/planner/` exists
- Directory has appropriate permissions (755)

**Dependencies:** None (can run in parallel with Task 1-2, but must complete before Task 4)

**Validation:**
```bash
[ -d cmd/howmux/templates/howmux/evals/cases/planner/ ] && echo "✓ Template directory exists"
```

---

### Task 4: Mirror Cases to Template Directory

**Acceptance Criteria:**
- All 5 YAML files from `.howmux/evals/cases/planner/` are copied to `cmd/howmux/templates/howmux/evals/cases/planner/`
- Files are byte-for-byte identical to source files
- Template sync check passes: `task sync:check` exits 0

**Dependencies:** Task 2 (source files must exist), Task 3 (destination directory must exist)

**Validation:**
```bash
# Verify all 5 files are mirrored
for file in root-cause-analysis scope-refinement acceptance-criteria-definition constraint-identification single-question-flow; do
  [ -f cmd/howmux/templates/howmux/evals/cases/planner/${file}.yaml ] && echo "✓ ${file}.yaml mirrored" || echo "✗ ${file}.yaml not mirrored"
done

# Verify files are identical
for file in .howmux/evals/cases/planner/*.yaml; do
  basename=$(basename "$file")
  diff -q "$file" "cmd/howmux/templates/howmux/evals/cases/planner/$basename" && echo "✓ $basename identical" || echo "✗ $basename differs"
done

# Verify template sync passes
task sync:check && echo "✓ Template sync check passed"
```

---

### Task 5: Verify Eval System Integration

**Acceptance Criteria:**
- `howmux eval planner --list` successfully lists all 5 new test cases
- `howmux eval planner --no-sandbox` runs without errors (requires kiro-cli)
- Build succeeds: `go build ./...` exits 0
- No syntax errors in YAML files

**Dependencies:** Task 2, Task 4 (all files must exist and be synced)

**Validation:**
```bash
# Build check
go build ./... && echo "✓ Build successful"

# List check
howmux eval planner --list 2>&1 | grep -c "yaml" | grep -q "5" && echo "✓ All 5 cases listed"

# Eval runner check (requires kiro-cli)
if command -v kiro-cli &> /dev/null; then
  howmux eval planner --no-sandbox && echo "✓ Eval runs successfully"
else
  echo "⚠ kiro-cli not available, skipping eval run check"
fi
```

---

## Validation Commands

After completing all tasks, run the full validation suite:

```bash
# Build verification
go build ./...

# Template sync verification
task sync:check

# Case listing verification
howmux eval planner --list

# Eval execution verification (requires kiro-cli)
howmux eval planner --no-sandbox
```

**Expected Results:**
- `go build ./...` — exits 0, no compilation errors
- `task sync:check` — exits 0, reports "Templates in sync"
- `howmux eval planner --list` — lists 5 test cases
- `howmux eval planner --no-sandbox` — runs and scores all 5 cases (requires kiro-cli)

## Out of Scope

The following are explicitly excluded from this work:

- Recording planner scores into `baseline.json` — deferred to a separate baseline update task after scores are reviewed
- Modifying the planner rubric at `.howmux/evals/rubrics/planner.yaml`
- Changes to the eval runner implementation
- Changes to planner prompt or conventions
- Adding fixtures under `.howmux/evals/fixtures/` (test cases use inline input, not file fixtures)

## Notes

### Test Case Design Considerations

1. **Root Cause Focus**: The planner's distinguishing capability is deep root cause analysis (per planner-conventions). At least one test case should demonstrate code tracing and hypothesis validation methodology.

2. **Single Question Adherence**: The planner's most critical behavioral rule is "ONE question at a time" (emphasized in planner-prompt.md). At least one test case should explicitly validate this rule and demonstrate prohibited patterns (multiple questions in one response).

3. **Gate Workflow**: The planner follows a mandatory gate workflow (Draft Review Gate → Label Confirmation Gate → Issue Creation). Test cases should exercise these gates where relevant.

4. **Input Style**: Planner inputs are symptom-level descriptions from users, NOT technical specs. Use conversational, problem-focused language (e.g., "The status command doesn't show elapsed time" not "Implement elapsed time formatting in handleStatus()").

5. **Planning Worktree**: Advanced test cases can reference planning worktree usage for hypothesis validation (per planner-conventions), but at least 3 cases should be simpler scenarios that don't require worktree investigation.

### Template Sync Mechanism

The template sync check verifies that files under `cmd/howmux/templates/howmux/` match their corresponding live files under `.howmux/`. The eval cases are part of this sync surface, but `results/` and `baseline.json` are excluded (`.templatesyncignore` rules). After creating the primary cases, they must be mirrored exactly to the template location for `task sync:check` to pass.

### Future Baseline Recording

Once these cases exist and can be scored, a follow-up task will:
1. Run `howmux eval planner --no-sandbox` to generate scores
2. Review scores for reasonableness
3. Record planner's entry into `baseline.json` using `howmux eval planner --record-baseline`

This is intentionally deferred to keep this issue focused on case creation only.
