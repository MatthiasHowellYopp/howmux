# Design Spec: Tighten Agent Prompts for Claude Sonnet 5

Closes #104

## 1. Solution Approach

All six prompts were written to compensate for a weaker model's tendency to skip
steps, drop requirements under pressure, or default to convenient-but-wrong
patterns (phased plans, holistic pass/fail, unlocked accessors). Sonnet 5 reads a
rule once and follows it; it does not need the rule repeated three times, or an
extended gallery of anti-patterns for every failure mode already covered by a
one-line rule. The scaffolding that accumulated to fight the older model's
failure modes is now dead weight: longer prompts to parse, more surface for
contradiction, and — per the issue's stated concern — length/noise without
incremental behavioral value.

**Strategy**:

1. **Preserve every responsibility verbatim in substance.** Each prompt's
   distinguishing responsibility (validator's two-phase gate, krew-lead's
   four-stage retry, architect's concurrency/mechanical-change analysis,
   builder's QA gate, planner's one-question gate and two approval gates,
   documenter's read-only constraint) stays. Only the *scaffolding around* the
   rule — repeated restatements, exhaustive worked examples, "critical
   requirements" sections that just re-say the workflow — is cut.
2. **Collapse repetition to a single authoritative statement per rule.** Where
   a constraint appears 2–4 times across a prompt (e.g., "write to the
   absolute WORKTREE path, not a relative path" appears in almost every
   prompt's workflow section AND its own "Critical Requirements" section),
   keep the fullest, clearest instance and delete the rest.
3. **Trim worked examples to the minimum that still teaches the lesson.**
   Several prompts carry 2–3 separate illustrated examples for the same
   single rule (e.g., architect's mechanical-change rule has an abstract
   statement, a Good/Bad phrasing pair, AND a full "Real-World Example: PR
   #20" narrative). Where a second or third example repeats a lesson the
   first example already teaches, and none of the repeated examples are
   directly load-bearing per the rubric, cut it. Where an example encodes a
   real failure mode the model would otherwise reproduce (verified against a
   real historical PR referenced in the prompt), compress it to the shortest
   form that still teaches the discriminating detail — do not delete it
   outright, since eval regressions are cited as a real risk for cut content.
4. **Do not touch structure the eval graders key on.** File paths, report
   section headings, sentinel-file conventions, and exact phrases that
   `basic-spec-generation`, `basic-functionality-verification`, etc. check for
   (e.g., "Overview", "What Was Built", `feature-<name>.md`, `VALIDATION
   FAILED`) are preserved character-for-character.
5. **One task per file, no shared edits.** Each of the six builder tasks
   touches exactly one prompt file, so all six can run in parallel with zero
   coordination.
6. **Conservative bias.** Per the issue's own risk callout, prefer leaving a
   borderline passage in over cutting it — the harm of a small amount of
   residual redundancy is bounded; the harm of silently removing load-bearing
   guidance is an eval regression that's expensive to diagnose after the fact.
   Every specific edit below was checked against: (a) does removing it drop a
   responsibility the rubric grades, and (b) is there a rubric criterion or eval
   case whose expected_output depends on the exact wording being removed. If
   either is uncertain, the edit was left out of this plan.

**Files NOT touched**: `ai-resources/prompts/*.md` (explicitly out of scope —
separate workflow), all six `.kiro/agents/*.json` configs (read for context
only), all `.kiro/skills/*-conventions/SKILL.md` files (these are a *different*
mechanism — project-specific skill resources loaded alongside the prompt, not
part of the prompt file itself; issue #104 scopes only the six `*-prompt.md`
files).

---

## 2. Per-Prompt Analysis and Edit Plan

### 2.1 `architect-prompt.md` (310 lines — the largest prompt by far)

**Current structure**: Purpose → Workflow → Design Spec Requirements →
Concurrency Analysis (required, with boundaries/detection rules/example/spec
requirements) → Mechanical Change Surface Enumeration (definition, "the trap",
6-category enumeration, good/bad phrasing examples, writer/reader rule,
verification-commands rule, a full "Real-World Example: PR #20" walkthrough
with two `##` headings of its own) → Implementation Approach (single-PR rule +
prohibited/required patterns) → Builder Context and Workflow Integration
(restates Implementation Approach) → Sentinel File → Critical Requirements
(restates several already-stated rules) → Task Breakdown Guidelines and
Examples (DO/DON'T code blocks + Key Principles).

This file has the most redundancy of the six because it restates its two
headline requirements (concurrency analysis, mechanical-change enumeration)
each 2–3 times in different framings, and carries a second full illustrated
walkthrough (PR #20) in addition to the abstract rule and the "Good/Bad"
phrasing pair — three worked instances of the same lesson.

**Redundant / over-verbose / stale-scaffolding passages**:

- **Lines 221–249, "Implementation Approach" + "Builder Context and Workflow
  Integration"**: these two `##` sections say the same thing twice. "Builder
  Context and Workflow Integration" (241–249) restates "One Issue at a Time",
  "Parallel Task Execution", "Complete Implementation" — each already stated
  in "Implementation Approach" (221–239) as "Kiro-krew may spawn multiple
  builder agents...", "Tasks without dependencies...", "All tasks must
  contribute to complete issue resolution...". Nothing new in the second
  section except the closing sentence about task breakdowns indicating
  dependencies, which is also already implied by "Tasks organized to enable
  parallelization where no dependencies exist" (line 239).
- **Lines 255–266, "Critical Requirements"**: of the 9 bullets, 6 restate
  material already stated verbatim elsewhere: "Write to the absolute
  `<WORKTREE>` path..." (restates line 16's Design Spec Requirements intro and
  line 251's Sentinel File instruction), "Create the specs/ directory..."
  (net-new, keep), "Write the spec file to disk..." (net-new — model could
  otherwise just respond in chat — keep), "Must reference source issue with
  Closes #<number>" (net-new, keep — no other line states this), "Do NOT
  implement any code" / "Do NOT spawn other agents" / "Focus on architecture,
  design, and planning only" (all three restate the Purpose section's opening
  sentence: "You design and plan solutions but do NOT implement code or spawn
  other agents"), "Complete Implementation Focus" / "Single-PR Task
  Breakdown" / "Validation Completeness" (all three restate "Implementation
  Approach", lines 221–239, in compressed bullet form).
- **Lines 174–219, "Real-World Example: PR #20"**: this is a third full
  illustration of the mechanical-change-enumeration lesson, after the abstract
  rule (54–114) and the "Good/Bad" phrasing pair (115–133). It reproduces the
  same acceptance-criteria list already shown as the "Good" example (119–133)
  almost verbatim, then narrates "what actually happened" in prose. The
  narrative ending ("What actually happened", 208–218) doesn't add a new rule —
  it's flavor text restating the trap already named at lines 65–71 ("The
  Trap"). Sonnet 5 does not need three instances of the same worked example to
  internalize one rule.
- **Lines 293–303, "Anti-Patterns to Avoid (❌ DON'T DO THIS)"**: shows a
  "Phase 1 / Phase 2" bad example. This duplicates, almost word for word, the
  "Prohibited Patterns" bullet list already given at lines 232–235
  ("Phase-based planning (e.g., 'Phase 1: Foundation', 'Phase 2: Core
  Logic')..."). The DO/DON'T code-block pair (268–303) is a second, longer
  restatement of the same single-PR/no-phasing rule stated compactly at
  221–239.

**LOAD-BEARING sections — map to eval rubric / cases**:

- **Design Spec Requirements (14–22)** and the five required sections
  (Solution Approach, Relevant Files, Team Orchestration, Task Breakdown,
  Validation Commands) → graded directly by `completeness` (deterministic:
  "All required sections present") and exercised by every eval case
  (`basic-spec-generation` expects "Clear problem statement... Task
  Modification list"; all cases expect a spec with these sections). **Must
  keep verbatim.**
- **Concurrency Analysis section (24–52)**, specifically the three numbered
  "Design Specification Requirements" at lines 45–51 (call it out in Solution
  Approach; add the lock-guarded-accessor acceptance criterion; require a
  concurrent test) → no eval *case* currently exercises this directly, but it
  is an explicit named responsibility in the issue's constraints ("don't
  remove the architect's concurrency-analysis... requirements") and feeds
  `task_decomposition`/`acceptance_criteria_testability` when a real
  concurrency-bearing issue is analyzed. **Must keep the boundaries list, the
  3 detection rules, and the 3 "Design Specification Requirements" bullets in
  substance.** The one worked example (the `Running()`/`w.mu.RLock()` snippet
  at line 43) is short and concrete — keep it, it is the only example in this
  subsection, not a redundant one.
- **Mechanical Change Surface Enumeration — the rule itself (54–114)**: the
  definition, "The Trap" paragraph, and the 6-category enumeration
  (Source/Tests/Docs/Config/CI/Template-synced) → graded by
  `task_decomposition` and `acceptance_criteria_testability`, and is the
  issue's named example of a load-bearing responsibility ("don't remove the
  architect's... mechanical-change enumeration requirements"). **Must keep the
  6-category enumeration verbatim** — this is the actionable checklist a
  builder needs, and it's stated exactly once at lines 77–114 (this exact
  instance is not duplicated elsewhere and is the version to keep).
- **"Good"/"Bad" acceptance-criteria phrasing pair (115–133)**: this is the
  single clearest, most compact illustration of the enumeration rule in
  action, and doubles as the template the architect should pattern-match its
  own output against. **Keep as the sole worked example** for this rule
  (replacing the PR #20 walkthrough — see edit below).
- **Writer/Reader rule (142–154)** and **Verification Commands rule
  (156–173)**: both are compact (single example each), state a distinct
  sub-rule not covered by the enumeration list itself (env-var pairs; grep
  commands the builder/validator can run), and are referenced by name in the
  issue body's constraints indirectly via "mechanical-change enumeration
  requirements". **Keep both, unchanged.**
- **Implementation Approach (221–239)**: single-PR delivery, no phased
  planning, parallelizable task breakdown → this is the *sole* place this rule
  needs to live after edits; graded by `task_decomposition`. **Keep verbatim**
  as the canonical statement of this rule.
- **Sentinel File (251–253)** → required for krew-lead to detect completion
  (cross-referenced in krew-lead-prompt.md's own Sentinel File Convention
  section). **Keep verbatim.**
- **"Proper Task Structure (✅ DO THIS)" example (270–291)**: this is the one
  positive worked example of dependency-aware task breakdown format (Task 1/2/3
  with explicit `**Dependencies**:` lines) — this is the format
  `task_decomposition` and `acceptance_criteria_testability` grade against, and
  it's the *only* place this exact format is demonstrated. **Keep verbatim.**

**Concrete edits**:

1. Delete the "Builder Context and Workflow Integration" section entirely
   (lines 241–249, from `## Builder Context and Workflow Integration` through
   the paragraph ending "...while dependent work is sequenced correctly."). No
   replacement text needed — "Implementation Approach" (221–239) already
   covers this ground.
2. In "Critical Requirements" (255–266), delete these three bullets (they
   restate the Purpose section, lines 3–5):
   - `Do NOT implement any code - only design and plan`
   - `Do NOT spawn other agents`
   - `Focus on architecture, design, and planning only`
   and delete these three bullets (they restate "Implementation Approach"):
   - `**Complete Implementation Focus**: Design specs must emphasize complete issue resolution in single PR`
   - `**Single-PR Task Breakdown**: All task breakdowns must support unified delivery, not phased approaches`
   - `**Validation Completeness**: All acceptance criteria must be achievable within one implementation cycle`
   Keep the remaining bullets: `Write to the absolute <WORKTREE> path...`,
   `Create the .../specs/ directory if it doesn't exist`, `Write the spec file
   to disk - do NOT just return it in your response`, `Must reference source
   issue with Closes #<number>`.
3. Delete the entire "Real-World Example: PR #20" section (lines 174–219, from
   `### Real-World Example: PR #20` through the line ending "...didn't
   verify."). The abstract rule (54–114) plus the Good/Bad phrasing pair
   (115–133) already teach this; the PR #20 narrative is a third repetition.
4. In "Task Breakdown Guidelines and Examples", delete the "Anti-Patterns to
   Avoid (❌ DON'T DO THIS)" subsection (lines 293–303, the `### Phase 1:
   Foundation Setup` / `### Phase 2: Core Implementation` code block and its
   heading). Keep "Proper Task Structure (✅ DO THIS)" (270–291) and "Key
   Principles" (304–end) — Key Principles' first bullet ("Tasks CAN establish
   foundations...") already states the boundary the anti-pattern block was
   illustrating, in one sentence, so no replacement text is needed.

**Confirmation**: None of these edits remove a responsibility or
tool-permission-relevant behavior. Concurrency analysis (24–52), mechanical
surface enumeration and its 6-category checklist (54–114), the
writer/reader rule (142–154), verification-commands rule (156–173),
single-PR/no-phasing rule (221–239), the sentinel file instruction (251–253),
and the DO-example task format (270–291) all survive unchanged. What's removed
is the second/third restatement of each rule, not the rule.

---

### 2.2 `krew-lead-prompt.md` (168 lines)

**Current structure**: Purpose → Input → Workflow (11 numbered steps, the core
orchestration sequence) → Available Agents (allow-list) → Critical
Requirements (8 bullets) → Retry and Execution Policy (Four-Stage Retry
Process, QA Loop Retry Policy, Attempt Tracking, Incident Report Format) →
Sentinel File Convention.

This file is comparatively lean already — most content is a single
non-redundant procedural spec (the workflow steps) rather than repeated
principle statements. The redundancy here is narrower: "Critical Requirements"
restates several already-explicit workflow steps.

**Redundant / over-verbose passages**:

- **Lines 73–83, "Critical Requirements"**: of the 9 bullets, several restate
  the Workflow section verbatim: `You are running inside the worktree - all
  file operations happen in the current directory` restates step 2's opening
  sentence ("The worktree has already been created and you are running inside
  it. Your current directory IS the worktree."). `Do NOT run
  worktree-create.sh or change directories to a worktree path` restates step
  2's closing sentence ("Do NOT run worktree-create.sh."). `Do NOT run
  .howmux/scripts/worktree-merge.sh - the PR workflow handles merging` is net
  new information (not stated elsewhere) — keep. `You have shell access - use
  it for git operations...` restates what's already implicit from steps 1, 7,
  8, 9, 10, 11 all being shell/gh commands; borderline, but it's one line and
  names which steps use shell — keep since it's compact and not a pure
  restatement of prose elsewhere (it's a cross-reference, not a repeat).
- **Lines 119–124, "Attempt Tracking"**: this 4-bullet section restates
  content already stated in the Four-Stage Retry Process above it (each of
  the four stages already states its own `[attempt:N]` tag) and in "QA Loop
  Retry Policy" (which already states `[qa-attempt:N]` tags). The only
  non-redundant content is "Preserve error logs and diagnostic information for
  incident reporting," which is already implied by the Incident Report Format
  template itself requiring "Result: [failure details]" per attempt.

**LOAD-BEARING sections — map to eval rubric / cases**:

- **Workflow steps 1–11 (13–61)** → `workflow_adherence` ("All workflow steps
  executed in correct order: read issue, create worktree, architect, build,
  validate, push, PR, label") is graded directly against this sequence. Every
  eval case (`basic-orchestration`, `pr-creation-flow`, `complex-coordination`)
  expects this exact ordering. **Keep entirely unchanged** — this is the
  single most eval-load-bearing section in any of the six prompts.
- **Step 2's "Capture the absolute worktree path first... Always give
  subagents the absolute WORKTREE path"** → graded by `delegation_quality`
  ("Sub-agents receive complete context including WORKTREE_PATH"). **Keep
  verbatim.**
- **Step 3's explicit instruction to state the absolute spec/sentinel paths to
  the architect** → also `delegation_quality`. **Keep verbatim.**
- **Available Agents allow-list (63–71)** → not directly named in the rubric
  but is a hard tool/capability boundary (which subagents may be invoked);
  removing it would change behavior, not just trim prose. **Keep verbatim.**
- **Four-Stage Retry Process (86–108)** and **Incident Report Format
  (125–151)** → `retry_policy_compliance` ("Four-stage retry process applied
  correctly with proper attempt tags and escalation") and exercised by
  `error-recovery` eval case. **Keep verbatim** — every stage's tag and
  behavior is distinct, non-redundant content (this section, unlike others in
  this file, doesn't restate itself).
- **QA Loop Retry Policy (110–117)** → distinct from general retries per its
  own text ("QA iterations are tracked separately from general agent
  retries"); graded indirectly by `workflow_adherence`'s QA-loop-in-sequence
  expectation. **Keep verbatim.**
- **Sentinel File Convention (153–168)**, including the "empty response →
  check sentinel before retry" recovery procedure → this is the mechanism
  every other agent's own "Sentinel File" section depends on for krew-lead to
  detect completion; also feeds `error_handling`. **Keep verbatim.**

**Concrete edits**:

1. In "Critical Requirements" (73–83), delete these two bullets:
   - `You are running inside the worktree - all file operations happen in the current directory`
   - `Do NOT run worktree-create.sh or change directories to a worktree path`
   Keep the remaining 6 bullets unchanged (they either state new information
   or are cross-references to specific step numbers, not restatements).
2. Delete the "Attempt Tracking" subsection entirely (lines 119–124, from
   `### Attempt Tracking` through `- Preserve error logs and diagnostic
   information for incident reporting`). No replacement needed — each retry
   stage already states its own tag requirement, and the Incident Report
   Format template already requires attempt-by-attempt result documentation.

**Confirmation**: The full 11-step workflow, the four-stage retry process, the
QA loop retry policy, the incident report format, the available-agents
allow-list, and the sentinel file convention (including the empty-response
recovery procedure) are all unchanged. What's removed are two lines that
duplicate step 2 of the Workflow section, and one subsection whose content is
already fully covered by the retry-stage tags and the incident report
template.

---

### 2.3 `builder-prompt.md` (81 lines — already the leanest of the six)

**Current structure**: Purpose → Instructions (6 bullets) → QA Feedback
Processing → Quality Assurance → Workflow (5 steps) → Sentinel File → Working
Directory → Report Format (template + "Critical QA Requirements" closing
bullets).

**Redundant / over-verbose passages**:

- **Lines 76–80, "Critical QA Requirements"** (the closing 4 bullets under
  Report Format): these restate content already stated in "Quality
  Assurance" (25–29: "Run ALL listed QA checks and ensure they pass before
  reporting completion") and in Workflow step 4 (35: "Run ALL provided QA
  checks and ensure they pass"). `ALL formatting checks must pass` / `ALL
  linting checks must pass` / `ALL tests must pass if tests exist... (100%
  pass rate required)` is the same "run all QA checks" instruction restated a
  third time, now split into three near-identical bullets. `Document specific
  failing checks and fixes applied` restates the "QA Feedback Processing"
  section's "Document how validator feedback was addressed" (already stated
  at line 22).
- **Workflow (31–37)** substantially restates "Instructions" (7–14) and
  "Quality Assurance" (25–29) as a 5-step numbered list covering the same
  ground already stated as bullets: "Understand the Task" restates "Do the
  work..."; "Navigate" restates "When given a working directory path, cd into
  it..."; "Execute" restates "Do the work: write code, create files..."; "Quality
  Assurance" restates the QA section immediately above it; "Report" restates
  the Report Format section immediately below it. This is a full
  restructuring of the same content into a second format (numbered workflow
  vs. bulleted instructions), not new guidance.

**LOAD-BEARING sections — map to eval rubric / cases**:

- **"You are assigned ONE task... Stay focused on the single task. Do not
  expand scope." (Instructions, 7–14)** → this is the builder's core scope
  boundary, exercised by every eval case (`simple-command-implementation`,
  `testing-implementation`, `performance-optimization` all give exactly one
  bounded task). **Keep verbatim.**
- **QA Feedback Processing (16–23)** → the mechanism the krew-lead's QA
  Feedback Loop (krew-lead-prompt.md step 6.4) depends on for the builder to
  incorporate validator's specific feedback; graded by `spec_adherence`.
  **Keep verbatim** — it is compact (7 lines) and each line states distinct
  information (where to look, what to parse, what to do, what to document).
- **Quality Assurance (25–29)** → graded by `code_correctness` and
  `test_coverage` (both `deterministic: true` in the rubric — the eval harness
  literally checks whether QA commands were run and passed). The instruction
  to fall back to CI config when no discovery results are provided is
  non-redundant (net-new information vs. Workflow step 4). **Keep verbatim.**
- **Sentinel File (39–41)** → required for krew-lead detection. **Keep
  verbatim.**
- **Working Directory (43–45)** → this is the file's single most
  eval-critical paragraph for `code_correctness`/`spec_adherence`: subagents
  landing writes in the wrong directory (repo root instead of worktree) is
  the named failure mode the issue's own architect-prompt.md example (PR #20)
  traces back to. **Keep verbatim, unabridged** — do not compress this
  paragraph even though it's the same warning repeated across all six
  prompts; it is one sentence long here already, and this file's version is
  the specific instance builder subagents will read.
- **Report Format template (49–75)**: the structured template itself
  (Task/Status/What was done/Files changed/QA Commands Discovered/QA Results/
  Verification) is a format-consistency aid feeding `spec_adherence` and
  `test_coverage` scoring (deterministic checks likely parse this structure).
  **Keep the template verbatim.**

**Concrete edits**:

1. Delete the "Critical QA Requirements" bullet list (lines 76–80, the four
   bullets after the Report Format template, from `**Critical QA
   Requirements:**` to the end of the file). "Quality Assurance" (25–29) and
   Workflow step 4 (35) already state "run ALL QA checks... ensure they
   pass"; "100% pass rate required" is the only marginally new phrase here,
   so fold it into the existing "Quality Assurance" section instead of
   keeping a separate closing list: change line 27 (`If no discovery results
   are provided, examine CI configuration...`) — actually, simplest
   conservative edit: append " (100% pass rate required for existing tests)"
   to the end of the existing sentence in Quality Assurance section, line 25
   ("Run ALL listed QA checks and ensure they pass before reporting
   completion.") → becomes "Run ALL listed QA checks and ensure they pass
   before reporting completion (100% pass rate required for existing
   tests)." Then delete the "Critical QA Requirements" heading and its 4
   bullets entirely.
2. Delete the "Workflow" section (lines 31–37, `## Workflow` through step 5
   `**Report** - Provide a brief summary of what was done.`). This entire
   5-step numbered restructuring duplicates Instructions + QA Feedback
   Processing + Quality Assurance + Report Format, which appear immediately
   around it. No replacement text needed.

**Confirmation**: The one-task scope boundary, QA feedback processing from
validator, the QA-must-pass gate (with the 100% figure preserved by folding
it into the retained Quality Assurance section rather than dropping it), the
sentinel file requirement, the absolute-working-directory instruction, and the
full report format template are all unchanged in substance. Only the
duplicate numbered "Workflow" restructuring and the third restatement of
"run all QA checks" are removed.

---

### 2.4 `validator-prompt.md` (272 lines — second largest)

**Current structure**: Your Role → Purpose → Two-Phase Validation Process
(Phase 1 extraction, Phase 2 verification) → Critical Rule: Specification
Compliance (with "Examples of Implementation Requirements" and "Never
Accept" bullets) → Anti-Pattern Example: PR #238 (a full worked example with
wrong-response/right-response pair) → Instructions → Shell Access Note →
Write Access Note → Quality Verification → Workflow → Report Format (full
template) → Report Format Rules (8 numbered rules).

**Redundant / over-verbose passages**:

- **Lines 64–98, "Anti-Pattern Example: PR #238"**: this is the file's single
  largest self-contained example — a full "Issue #235 specified" /
  "Implementation used" / "❌ WRONG Validator Response" / "✅ CORRECT Validator
  Response" / "Why This Matters" walkthrough. It illustrates exactly the same
  rule already stated compactly and completely at lines 48–62 ("Critical
  Rule: Specification Compliance" + "Examples of Implementation Requirements"
  + "Never Accept" bullets). The abstract rule ("If the issue says 'use X', you
  MUST verify X is used") plus the four one-line "Never Accept" bullets
  already fully specify the behavior; the 35-line PR #238 narrative adds a
  concrete instance but no new rule. (Note: an even more detailed version of
  this same example, plus five additional anti-pattern examples, lives in the
  sibling `validator-conventions` skill, which is loaded as a resource
  alongside this prompt and is out of scope for this issue — so this
  particular repetition inside the prompt file is fully redundant with
  material the model already receives via the skill resource, not just with
  itself.)
- **Lines 48–62 vs. lines 100–108 ("Instructions")**: "Critical Rule:
  Specification Compliance" and "Instructions" both restate "verify EACH
  criterion individually — never group" in near-identical phrasing
  ("Follow the two-phase process: extraction THEN verification" / "Verify EACH
  criterion individually - never group criteria" at 105–106 vs. the Phase
  1/Phase 2 headers already making this structure explicit at lines 13–46).
  This is a smaller, one-line-level redundancy — flagged but the fix is a
  targeted line removal, not a section deletion (see edit 2 below).

**LOAD-BEARING sections — map to eval rubric / cases**:

- **Two-Phase Validation Process (13–46)**, specifically the Phase
  1/Phase 2 structure and the instruction "Complete Phase 1 BEFORE moving to
  Phase 2. Do not verify anything until all criteria are extracted." → this
  is the constraint the issue explicitly names as a responsibility not to
  strip ("don't strip the validator's two-phase verification requirement"),
  and is what `issue_coverage` grades ("All acceptance criteria from the
  issue are verified") — `basic-functionality-verification`,
  `security-validation`, and `regression-testing` all expect systematic,
  criterion-by-criterion coverage. **Keep verbatim, unabridged.**
- **"Critical Rule: Specification Compliance" (48–62)**, the core rule
  sentence and the "Never Accept" bullets → this is the compact, complete
  statement of the specification-compliance requirement; graded by
  `defect_detection` ("Identifies real issues without false positives" — a
  validator that accepts "functionally equivalent" alternatives produces
  false negatives on spec violations). **Keep verbatim** — this is the
  version to retain in place of the PR #238 narrative.
- **Report Format template (141–262)** — every heading (`# Validation Report:
  Issue #[NUMBER]`, `## Phase 1: Acceptance Criteria Extraction`, `## Phase 2:
  Individual Criterion Verification`, `## Overall Validation Result`, `##
  Quality Assurance Results`, `## Feedback (for failures)`) is almost
  certainly what a deterministic grading pass parses for structure; also
  feeds `actionable_feedback`. **Keep verbatim, unabridged** — do not
  compress the template itself, only the prose *around* it.
- **Report Format Rules (263–272)**, in particular rule 6 ("Failure
  Sentinel: ...output must contain the exact text 'VALIDATION FAILED' on its
  own line") and rule 7 ("Exit Code Contract: Exit with code 1 when ANY
  criterion or QA check fails...") → these are exact-string/exact-behavior
  contracts other tooling (krew-lead's QA gate) likely depends on. **Keep
  verbatim, unabridged, exact wording including the literal string
  "VALIDATION FAILED".**
- **Shell Access Note (109–115)** and **Write Access Note (117–120)** → these
  state the validator's actual tool boundary (read-only + one write
  exception for the sentinel file), matching `validator.json`'s
  `autoAllowReadonly: true` and the `write` tool grant restricted to the
  sentinel path. **Keep verbatim** — this is a tool-permission-relevant
  responsibility, explicitly in scope for preservation.
- **Quality Verification (121–125)** → feeds `test_execution`
  (`deterministic: true`: "Validation commands are actually run and results
  reported"). **Keep verbatim.**

**Concrete edits**:

1. Delete the entire "Anti-Pattern Example: PR #238" section (lines 64–98,
   from `## Anti-Pattern Example: PR #238` through the paragraph ending
   "...before PR creation."), keeping the "Critical Rule: Specification
   Compliance" section (48–62) directly above it as the sole statement of
   this rule in the prompt. The heading immediately after it, `##
   Instructions`, becomes the next section.
2. In "Instructions" (100–108), delete the bullet `Follow the two-phase
   process: extraction THEN verification` — this restates the Phase 1/Phase 2
   structure already established as the section's own headline (`## Two-Phase
   Validation Process`, lines 13–46). Keep the remaining 5 bullets, including
   `Verify EACH criterion individually - never group criteria` (this is not
   redundant with anything else — the two-phase *process* structure and the
   *never-group* rule are two distinct constraints).

**Confirmation**: The two-phase extraction-then-verification requirement, the
strict specification-compliance rule (issue says "use X" → verify X exactly),
the full structured report template with every required heading, the exact
"VALIDATION FAILED" sentinel string, the exit-code contract, the
read-only/sentinel-write-only tool boundary, and the independent QA
re-verification requirement are all unchanged. What's removed is one
35-line narrative example that illustrates a rule already stated compactly
two sections above it (and is more fully covered by the separate
`validator-conventions` skill resource), plus one line in "Instructions" that
duplicates the section's own heading structure.

---

### 2.5 `documenter-prompt.md` (44 lines — smallest of the six, minimal redundancy)

**Current structure**: Purpose → Instructions (5 bullets) → Documentation
Format (5 required subsections with one-line descriptions each) → Sentinel
File → Rules (6 bullets).

This file is already tight. There is no repeated restatement of the same rule
across sections — each section adds distinct information. The only marginal
redundancy is the read-only/no-shell constraint appearing in both "Rules" and
being implicit from tool permissions (documenter.json grants only `read`,
`write` — no `shell`), but the prompt-level statement is the only place this
constraint is *stated to the model*, since the model does not read its own
JSON config, so this is not actually redundant — it is the sole source of this
information within the prompt.

**Redundant / over-verbose passages**: None identified that meet the bar for
a confident, low-risk cut. Specifically considered and rejected:

- "Rules" (38–44) bullets `Do NOT modify any implementation code` and `Do NOT
  spawn other agents` might look like they restate "Purpose" (3–4: "You
  generate concise markdown documentation for features that have been built
  and validated"), but Purpose never states the negative constraint — it only
  states the positive charter. Removing either bullet would leave the
  no-code-modification and no-subagent-spawning constraints stated nowhere in
  the prompt (documenter.json's tool list doesn't include `shell` or
  `subagent`, but the model reasons from the prompt, not the JSON). **Not
  cut.**
- The five Documentation Format subsections (17–33) are each one line and
  non-overlapping (Overview/What Was Built/Technical Implementation/Usage/
  Configuration) — no redundancy to remove.

**LOAD-BEARING sections — map to eval rubric / cases**:

- **Documentation Format (13–33)**, the five required sections → graded
  directly by `documentation_completeness` (`deterministic: true`: "All
  required sections present: Overview, What Was Built, Technical
  Implementation, Usage") and exercised by every eval case
  (`architecture-documentation`, `api-documentation`,
  `configuration-reference`, etc., all expect exactly this structure). **Keep
  verbatim, unabridged.**
- **"Generate a markdown documentation file in `<WORKTREE>/app_docs/` with
  filename format `feature-<descriptive-name>.md`" (Instructions, line 10)**
  → graded directly by `file_naming_convention` (`deterministic: true`:
  "Output file follows the required format: app_docs/feature-<descriptive-
  name>.md"). **Keep verbatim, exact path and filename pattern.**
- **"Do NOT run shell commands - you only read files and write documentation"
  (Rules, line 42)** → this is the explicit statement of the
  read-only-no-shell constraint the issue names as a responsibility not to
  remove ("don't remove documenter's read-only-no-shell constraint"). **Keep
  verbatim.**
- **"Document what was actually built, not what was planned" (Rules, line
  43)** → graded by `accuracy` ("Documentation reflects what was actually
  built, not just what was planned" — nearly a direct quote of this rubric
  description). **Keep verbatim.**
- **Sentinel File (34–36)** → krew-lead completion detection. **Keep
  verbatim.**

**Concrete edits**: None. This file does not contain redundant, over-verbose,
or stale scaffolding passages that meet the bar for a confident cut without
risking the loss of load-bearing content in a file this size (44 lines, no
repeated principle statements). Recommend the builder task for this file be a
**no-op verification pass**: confirm no redundant passages were missed, run
the eval baseline comparison (see Validation Commands), and report "no edit
required" if the pre/post scores are identical (they should be, since no
byte changes). This keeps the per-file task structure uniform across all six
files while avoiding an edit that isn't warranted.

**Confirmation**: N/A — no edit proposed, so nothing is at risk of being
removed.

---

### 2.6 `planner-prompt.md` (138 lines)

**Current structure**: Header → ABSOLUTE RESTRICTIONS (6 bullets) → Critical
Rule: One Question at a Time → Question Format Rules (Prohibition list, Option
Presentation Format, then a "GOOD/BAD" and second "PROBLEMATIC/IMPROVED" pair
of examples) → MANDATORY WORKFLOW GATES (Gate 1: Draft Review Gate with 5
substeps including Root Cause Analysis; Gate 2: Label Confirmation Gate; Gate
3: Issue Creation) → Issue Creation (bash examples) → Guidelines.

**Redundant / over-verbose passages**:

- **Lines 18–55, "Question Format Rules"**: contains two separate
  illustrated example pairs that teach the *same* single-question,
  a/b/c-option-format rule: (1) "✅ GOOD - Clear options" / "❌ BAD - Ambiguous
  yes/no" (24–33), and (2) a second, longer "❌ PROBLEMATIC - Multiple
  questions" / "✅ IMPROVED - Single focused question" plus a *third*
  "❌ PROBLEMATIC - Ambiguous either/or" / "✅ IMPROVED - Clear options" pair
  (35–55). That's three worked example pairs for one rule, immediately
  preceded by "Critical Rule: One Question at a Time" (14–16) already stating
  the rule in prose, and immediately preceded by "Prohibition on Multiple
  Questions" (20–23) already stating it a second time in bullet form. Four
  statements of the same constraint before Gate 1 even starts.
- **Lines 14–16 vs. 20–23**: "Critical Rule: One Question at a Time" and
  "Question Format Rules → Prohibition on Multiple Questions" say the same
  thing in slightly different words ("You MUST ask only ONE question per
  response. Never present bulleted lists..." vs. "NEVER ask multiple
  questions in one response / NEVER present bulleted lists of questions").

**LOAD-BEARING sections — map to eval rubric / cases**:

- **The single-question rule itself** → graded directly by
  `single_question_adherence` (`scoring: 1-5`, and its description is nearly
  a direct quote of the rule text) and is the explicit subject of the
  `single-question-flow` eval case, whose `context` field states "Demonstrates
  single_question_adherence rubric dimension (critical planner rule)" and
  gives the exact prohibited-pattern text to avoid. **The rule statement
  itself must survive** — this is named in the issue as a responsibility not
  to remove ("don't remove planner's one-question-at-a-time gate").
- **The a/b/c option-format convention** → graded by `question_clarity`
  ("Questions posed are clear, focused, and avoid ambiguous phrasing") and is
  the exact format `single-question-flow`'s `expected_output` demonstrates
  (lettered options with an "Other (please specify)" tail). **At least one
  worked example of this format must survive** — this is the pattern the
  model reproduces.
- **Gate 1: Draft Review Gate (60–101)**, all 5 substeps including Root Cause
  Analysis (62–83) and the explicit "MANDATORY DRAFT REVIEW... wait for
  explicit approval before proceeding" language (98–101) → graded by
  `requirement_clarity`, `constraint_identification`, and
  `acceptance_criteria_quality`; exercised directly by `root-cause-analysis`
  and `constraint-identification` eval cases. This is one of the two
  "mandatory approval gates" the issue names as a responsibility not to
  remove. **Keep entirely unchanged**, including the worktree-based
  hypothesis-testing procedure and the structured a/b/c/d decision-option
  format for root-cause-vs-symptom (89–96), since these are the specific
  behaviors `root-cause-analysis`'s `expected_output` checks for.
- **Gate 2: Label Confirmation Gate (103–109)** → the second mandatory
  approval gate the issue names explicitly. **Keep verbatim** — it is already
  a single compact paragraph with no redundancy.
- **Gate 3 / Issue Creation (111–130)**, including the exact `gh issue create`
  command forms with/without `--label` → mechanically necessary, not prose
  scaffolding. **Keep verbatim.**
- **ABSOLUTE RESTRICTIONS (5–12)** → the planner's tool/scope boundary
  (no source edits, no implementation, redirect language for
  fix-it requests). Not redundant with anything else in the file. **Keep
  verbatim.**

**Concrete edits**:

1. In "Question Format Rules" (18–55), delete the second and third example
   pairs — keep only the first "✅ GOOD - Clear options" / "❌ BAD - Ambiguous
   yes/no" pair (24–33) as the sole worked illustration. Specifically delete
   lines 35–55 in full: the `**Examples of Problematic vs. Improved
   Patterns:**` heading and both subsequent pairs (`❌ PROBLEMATIC - Multiple
   questions` / `✅ IMPROVED - Single focused question`, and `❌ PROBLEMATIC -
   Ambiguous either/or` / `✅ IMPROVED - Clear options`). The remaining GOOD/BAD
   pair already demonstrates both the option-lettering format and the
   ambiguous-phrasing failure mode; the two deleted pairs illustrate the exact
   same two lessons a second and third time.
2. Delete "Prohibition on Multiple Questions" as a separate subsection
   (lines 20–23, the 3-bullet list under that sub-heading), since "Critical
   Rule: One Question at a Time" (14–16) directly above it already states
   this in prose ("You MUST ask only ONE question per response. Never
   present bulleted lists of multiple questions. Ask a single focused
   question, wait for the answer, then proceed to the next."). Keep the
   "Option Presentation Format" sub-heading and its content (the GOOD/BAD
   pair retained per edit 1) immediately following.

**Confirmation**: The one-question-at-a-time rule survives (stated once, in
"Critical Rule: One Question at a Time"), the a/b/c/other option-format
convention survives (one worked example retained), both mandatory approval
gates (Draft Review Gate and Label Confirmation Gate) are completely
untouched, the root-cause-analysis procedure and its structured decision-
option format are completely untouched, and the ABSOLUTE RESTRICTIONS scope
boundary is untouched. What's removed is two redundant example pairs
teaching a lesson the retained first pair already teaches, and one
subsection that duplicates the sentence directly above it.

---

## 3. Task Breakdown for the Builder

All six tasks are independent — each touches exactly one file, none share
line ranges or dependencies, and all six can be executed in parallel.

### Task 1: Tighten `architect-prompt.md`

**Acceptance Criteria**:
1. Section `## Builder Context and Workflow Integration` (originally lines
   241–249) is removed in its entirety.
2. In `## Critical Requirements`, the three bullets `Do NOT implement any
   code - only design and plan`, `Do NOT spawn other agents`, `Focus on
   architecture, design, and planning only` are removed.
3. In the same section, the three bullets `**Complete Implementation
   Focus**: ...`, `**Single-PR Task Breakdown**: ...`, `**Validation
   Completeness**: ...` are removed.
4. The remaining `## Critical Requirements` bullets (`Write to the absolute
   <WORKTREE> path...`, `Create the .../specs/ directory...`, `Write the spec
   file to disk...`, `Must reference source issue with Closes #<number>`) are
   present, unchanged.
5. Section `### Real-World Example: PR #20` (originally lines 174–219) is
   removed in its entirety, including both of its own sub-headings.
6. Subsection `### Anti-Patterns to Avoid (❌ DON'T DO THIS)` under `##
   Task Breakdown Guidelines and Examples` (originally lines 293–303) is
   removed; `### Proper Task Structure (✅ DO THIS)` and `### Key Principles`
   remain unchanged.
7. All of the following remain present and byte-identical to the original:
   `## Purpose`, `## Workflow`, `## Design Specification Requirements`, `##
   Concurrency Analysis (Required for All Changes)` in full (including its
   `###` subsections), `## Mechanical Change Surface Enumeration` through
   `### Verification Commands in Acceptance Criteria` (i.e., everything
   except the deleted PR #20 example), `## Implementation Approach` in full,
   `## Sentinel File`.
8. File still parses as valid Markdown; no dangling headers or broken code
   fences from the deletions.

### Task 2: Tighten `krew-lead-prompt.md`

**Acceptance Criteria**:
1. In `## Critical Requirements`, the bullets `You are running inside the
   worktree - all file operations happen in the current directory` and `Do
   NOT run worktree-create.sh or change directories to a worktree path` are
   removed.
2. The remaining 6 bullets in `## Critical Requirements` are present,
   unchanged (including `Do NOT run .howmux/scripts/worktree-merge.sh - the
   PR workflow handles merging` and the shell-access bullet).
3. Subsection `### Attempt Tracking` (originally lines 119–124) is removed in
   its entirety.
4. `## Workflow` (all 11 numbered steps), `## Available Agents`, `###
   Four-Stage Retry Process` (all four stages), `### QA Loop Retry Policy`,
   `### Incident Report Format`, and `## Sentinel File Convention` remain
   present and byte-identical to the original.
5. File still parses as valid Markdown.

### Task 3: Tighten `builder-prompt.md`

**Acceptance Criteria**:
1. The sentence in `## Quality Assurance` (`Run ALL listed QA checks and
   ensure they pass before reporting completion.`) is amended to append `(100%
   pass rate required for existing tests)` before the closing period.
2. The `**Critical QA Requirements:**` heading and its four bullets
   (originally lines 76–80, following the Report Format template) are
   removed in their entirety.
3. Section `## Workflow` (originally lines 31–37, the 5-step numbered list
   from "Understand the Task" through "Report") is removed in its entirety.
4. `## Purpose`, `## Instructions`, `## QA Feedback Processing`, the amended
   `## Quality Assurance`, `## Sentinel File`, `## Working Directory`, and
   `## Report Format` (full template) remain present, with all text other
   than the Task 3.1 amendment byte-identical to the original.
5. File still parses as valid Markdown.

### Task 4: Tighten `validator-prompt.md`

**Acceptance Criteria**:
1. Section `## Anti-Pattern Example: PR #238` (originally lines 64–98) is
   removed in its entirety, including its nested `### Criterion 4:...`
   sub-heading.
2. In `## Instructions`, the bullet `Follow the two-phase process: extraction
   THEN verification` is removed. The remaining 5 bullets (including `Verify
   EACH criterion individually - never group criteria`) are present,
   unchanged.
3. `## Your Role`, `## Purpose`, `## Two-Phase Validation Process` (both
   Phase 1 and Phase 2 subsections in full), `## Critical Rule: Specification
   Compliance` (including "Examples of Implementation Requirements" and
   "Never Accept" bullets), `## Shell Access Note`, `## Write Access Note`,
   `## Quality Verification`, `## Workflow`, `## Report Format` (the entire
   template), and `## Report Format Rules` (all 8 numbered rules, including
   the exact string `VALIDATION FAILED` in rule 6 and the exit-code contract
   in rule 7) remain present and byte-identical to the original.
4. File still parses as valid Markdown.

### Task 5: Verify `documenter-prompt.md` (no-op pass)

**Acceptance Criteria**:
1. `documenter-prompt.md` is confirmed unchanged (byte-identical to the
   version at the start of this issue) — no edit is warranted per the
   analysis in section 2.5.
2. The builder records in its sentinel file that this file was reviewed and
   no redundant/over-verbose/stale-scaffolding passages were found that could
   be safely removed without risking the responsibilities graded by
   `documentation_completeness`, `file_naming_convention`, `accuracy`, or the
   read-only-no-shell constraint.
3. Running `howmux eval documenter` before and after this task produces
   identical scores per case (since the file did not change).

### Task 6: Tighten `planner-prompt.md`

**Acceptance Criteria**:
1. Under `## Question Format Rules`, the subsection `**Examples of
   Problematic vs. Improved Patterns:**` and its two example pairs
   (`❌ PROBLEMATIC - Multiple questions` / `✅ IMPROVED - Single focused
   question`, and `❌ PROBLEMATIC - Ambiguous either/or` / `✅ IMPROVED - Clear
   options`) — originally lines 35–55 — are removed in their entirety.
2. The `**Prohibition on Multiple Questions:**` sub-heading and its 3-bullet
   list (originally lines 20–23) are removed.
3. `## Critical Rule: One Question at a Time` (the prose statement, lines
   14–16), the `**Option Presentation Format:**` sub-heading and its
   retained `✅ GOOD - Clear options` / `❌ BAD - Ambiguous yes/no` example
   pair (originally lines 24–33), `## ABSOLUTE RESTRICTIONS`, `##
   MANDATORY WORKFLOW GATES` in full (all three gates, including the
   Root Cause Analysis substeps and the a/b/c/d decision-option format), `##
   Issue Creation`, and `## Guidelines` remain present and byte-identical to
   the original.
4. File still parses as valid Markdown.

---

## 4. Validation Commands

Regression checking uses the eval harness against the committed baseline at
`.howmux/evals/baseline.json` (recorded per-agent averages using the runner's
pooled, skip-aware formula).

**Per-file validation procedure** (run for each of Tasks 1–4 and 6; Task 5 is
a no-op so the "before" and "after" run are expected to be identical):

```bash
# Before editing <agent>-prompt.md, capture current score (should match baseline):
howmux eval <agentname>

# After editing, re-run:
howmux eval <agentname>

# Compare the reported average for <agentname> against the committed baseline:
#   .howmux/evals/baseline.json → .agents.<agentname>.average
```

Where `<agentname>` is one of: `architect`, `krew-lead`, `builder`,
`validator`, `documenter`, `planner`.

**Pass condition (stricter than the tool's default)**: the post-edit average
for that agent must be **greater than or equal to** the baseline average
recorded in `.howmux/evals/baseline.json`. Per the issue's acceptance
criteria, treat **any** drop below the baseline average as a failure — do
**not** rely on `howmux eval`'s own built-in 0.05 tolerance band if the tool
reports a pass despite a numeric drop. If `howmux eval baseline <agentname>`
becomes available once baseline.json is fully wired for comparison, prefer it,
but still apply the zero-tolerance rule above rather than the tool's default
threshold.

**Baseline averages to check against** (from `.howmux/evals/baseline.json` as
of this spec):

| Agent | Baseline average |
|---|---|
| architect | 0.757 |
| documenter | 0.844 |
| builder | 0.5 |
| validator | 0.38 |
| krew-lead | 0.369 (0.36923076923076925) |
| planner | 0.693 (0.6933333333333334) |

**If a regression is detected** (post-edit average < baseline average for that
agent, by any amount): revert the edit(s) to that file, re-run the eval to
confirm the score returns to baseline, and either (a) drop the specific edit
that caused the regression and re-test the remaining edits individually to
isolate which change was load-bearing, or (b) escalate to a human with the
specific eval case(s) that regressed and their score deltas, since this
indicates a passage judged redundant in this spec was in fact behaviorally
load-bearing for Sonnet 5.

**Sanity check before starting**: confirm the working tree is at the commit
the baseline was recorded against (or later, with no other prompt changes in
between) — `.howmux/evals/baseline.json` notes architect/documenter were
recorded at `e72345a` and builder/validator/krew-lead/planner at `78f388c`
("issue #104 pre-edit baseline"). If the six prompt files have any
uncommitted or unrelated changes relative to that state, run a fresh
`howmux eval <agentname>` for each of the six agents first to get true
current-state numbers before making any edits from this spec, rather than
trusting the recorded baseline.json blindly if drift is suspected.
