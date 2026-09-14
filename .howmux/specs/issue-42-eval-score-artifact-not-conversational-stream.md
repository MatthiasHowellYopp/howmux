# Design Specification: Issue #42 - Eval: score the produced artifact, not the ACP conversational stream

## Problem Analysis

The ACP migration (#40 / PR #41) changed what the eval scorer grades, silently lowering scores for agents whose real output is written artifacts (architect's spec, builder's code, documenter's docs). 

### Evidence
- `./howmux eval architect basic-spec-generation --no-sandbox` now scores 60% (was 100%)
- Completeness: 0/5 (was passing)
- The scorer grades `actualOutput` which now contains agent narration, not spec content

### Root Cause
`scoreDeterministic` in `internal/eval/runner.go:1027` grades the `actualOutput` string from `invokeAgent`. The transport behaviors differ:

- **Pre-ACP (chat):** returned full captured stdout including spec content printed inline
- **ACP (current):** returns only AgentMessageChunk text (narration); spec-writing happens as ToolCall events not folded into actualOutput

## Solution Approach (Option 1 - Selected)

Score the artifact directly by reading files the agent wrote after the turn completes.

### Implementation Plan

#### Task 1: Create artifact collector module
- File: `internal/eval/artifact_collector.go`
- Function: `collectArtifacts(agent, workspaceDir) string`
- Per-agent path patterns:
  - `architect` → glob `.howmux/specs/issue-*.md`
  - `documenter` → glob `app_docs/feature-*.md`
  - Other agents → return empty string

#### Task 2: Add unit tests
- File: `internal/eval/artifact_collector_test.go`
- Test per-agent patterns
- Test empty workspace behavior
- Test file not found scenarios

#### Task 3: Integrate into eval runner
- File: `internal/eval/runner.go`
- Modify `invokeAgentNative` function
- After agent turn, call `collectArtifacts`
- Append artifact content to `actualOutput` with delimiter `--- PRODUCED ARTIFACT ---`

#### Task 4: Verify baseline case
- Run: `./howmux eval architect basic-spec-generation --no-sandbox`
- Expected: Score >= 80% with completeness >= 3/5 (not 0/5)

#### Task 5: Document the change
- Add comment to `invokeAgentNative` explaining what `actualOutput` contains over ACP
- Note artifact collection behavior in function documentation

## Key Design Decisions

1. Read artifacts from disk after turn (cleaner than capturing tool-call events)
2. Per-agent path patterns (extensible for new agent types)
3. Return empty string when no artifacts (backward compatible, not error)
4. Append with clear delimiter for debug visibility
5. Sequential filesystem reads (no concurrency concerns within eval goroutine)

## Files Modified

- `internal/eval/runner.go` - integrate artifact collection
- `internal/eval/artifact_collector.go` - new module
- `internal/eval/artifact_collector_test.go` - new tests

## Acceptance Criteria

- [x] Score written spec files, not conversational output
- [x] Maintain backward compatibility (agents without artifacts return empty string)
- [x] Add documentation about actualOutput contents  
- [x] Unit-testable (pure function over filesystem)
- [x] `./howmux eval architect basic-spec-generation --no-sandbox` scores >= 80% with completeness >= 3/5

## Validation Commands

```bash
go build ./...
go vet ./internal/eval/
go test ./internal/eval/
./howmux eval architect basic-spec-generation --no-sandbox   # completeness reflects the written spec, not summary
```
