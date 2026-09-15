# Design Specification: Eval Artifact Collection on Sandbox Path

**Issue:** #50  
**Title:** Eval: collect artifacts on the sandbox/container path  
**Closes:** #50

## Problem Summary

Artifact collection (#42) is integrated into the native eval path (`invokeAgentNative`) only. The sandboxed path (`invokeAgentInContainer`, used for `--sandbox` runs) has no equivalent call, so **sandboxed eval runs still score the ACP conversational stream, not the produced artifact**. This means:

- Native path: Agent writes spec to host filesystem → `collectArtifacts` reads it → scored output includes the actual artifact
- Sandbox path: Agent writes spec **inside container** → `collectArtifacts` finds nothing on host → scored output contains only narration, not the deliverable

This is the regression that #42 fixed for native mode, still present in the container path.

## Root Cause

The container filesystem is **not host-visible**. In `invokeAgentInContainer`, the agent runs inside a Docker container:
- Container workspace: `/workspace` (inside container)
- Agent writes artifacts to `/workspace/.howmux/specs/issue-*.md` (container-internal path)
- After the turn, `collectArtifacts(agent, workspaceDir)` runs on the **host** with a host `workspaceDir` path
- No files exist at that host path → `collectArtifacts` returns empty string

The native path works because `workspaceDir` is a real host directory where the agent writes directly.

## Solution Approach

**Bind-mount the workspace directory** so container writes land on a host path that `collectArtifacts` can read.

### Why bind-mount (Option 1 from issue)?

1. **Simplest integration**: No extraction logic needed; `collectArtifacts` works unchanged on the mounted host path
2. **Real file I/O**: Agent sees a real filesystem (not a copy-out fake), matching native path behavior
3. **Already standard**: Docker eval workflows commonly mount workspaces; this is the expected pattern
4. **Parity guarantee**: Native and sandbox paths both read from a real filesystem after the agent turn

### Alternative rejected: Copy artifacts out (Option 2)

Would require:
- Detecting which files are artifacts (per-agent patterns from `artifact_collector.go`)
- Running `docker cp` or `docker exec cat` for each matched file
- Writing to a temp dir on host, then calling `collectArtifacts` on that temp location

**Why rejected**: More complex, slower (extra container I/O), and introduces a new code path where native and sandbox diverge (native reads real workspace, sandbox reads extracted copy). The bind-mount approach keeps them identical.

## Concurrency Analysis

This change **does not introduce cross-goroutine access**. The artifact collection happens in the main eval goroutine after the agent turn completes (synchronous call at the end of `invokeAgentInContainer`), reading files from a host-mounted directory. No shared state is read/written concurrently.

## Relevant Files

### Files to Modify

1. **`internal/eval/runner.go`**
   - `invokeAgentInContainer`: Add bind-mount to `hostConfig`, then call `collectArtifacts` after agent turn (matching native path)
   - `createContainerConfig`: Ensure `WorkspaceDir` is set to a host path (already is: defaults to `/workspace` as the container-internal target, but we'll create a host temp dir and mount it there)

2. **`internal/eval/sandbox/container.go`**
   - `CreateWithPlatform`: Accept an optional bind-mount in `hostConfig.Binds` (Docker API already supports this; no new API needed)

### Files Referenced (No Changes)

- **`internal/eval/artifact_collector.go`**: Already works correctly; just needs a host-visible path
- **`internal/eval/types.go`**: `ContainerConfig` already has `WorkspaceDir`; no new fields needed

## Detailed Implementation Plan

### Task 1: Add Workspace Bind-Mount to Sandbox Path

**Location**: `internal/eval/runner.go` → `invokeAgentInContainer`

**What to do**:

1. **Create a host temp directory** for the workspace at the start of `invokeAgentInContainer`:
   ```go
   hostWorkspaceDir, err := os.MkdirTemp("", "howmux-eval-sandbox-*")
   if err != nil {
       return "", CostInfo{}, nil, fmt.Errorf("creating host workspace: %w", err)
   }
   defer os.RemoveAll(hostWorkspaceDir)
   ```

2. **Bind-mount it into the container** by adding a `Bind` to `hostConfig` before calling `c.CreateWithPlatform`:
   ```go
   hostConfig := sandbox.NewHostConfigWithLimits(cConfig.ResourceLimits)
   // Add workspace bind-mount so container writes land on host filesystem
   hostConfig.Binds = []string{
       fmt.Sprintf("%s:%s", hostWorkspaceDir, cConfig.WorkspaceDir),
   }
   ```

   - `hostWorkspaceDir`: The host temp dir we just created
   - `cConfig.WorkspaceDir`: The container-internal path (e.g., `/workspace`)
   - Docker bind syntax: `<host-path>:<container-path>`

3. **Call `collectArtifacts` after the agent turn** (after `c.ExecWithOutput` returns), passing the **host** path:
   ```go
   result := stripANSISequences(output)
   
   // Collect artifacts from the mounted workspace (same as native path)
   artifactContent := collectArtifacts(agent, hostWorkspaceDir)
   if artifactContent != "" {
       result += artifactContent
   }
   
   cost := estimateCost(prompt, result)
   return result, cost, nil, nil
   ```

   This matches the native path exactly (lines 825-829 in `runner.go`).

**Acceptance Criteria**:

1. After a sandboxed turn, the produced artifact (per the agent's `collectArtifacts` pattern) is retrieved from the mounted host directory and appended to the scored output under `--- PRODUCED ARTIFACT ---`
2. A sandboxed architect run scores the written spec (the actual file content), not just the narration
3. The artifact content in sandboxed mode matches what the native path would produce for the same agent/case
4. The temp workspace directory is cleaned up after the turn (via `defer os.RemoveAll(hostWorkspaceDir)`)

**Dependencies**: None (can run in parallel with Task 2)

---

### Task 2: Add Parity Test (Native vs Sandbox Artifact Scoring)

**Location**: `internal/eval/runner_test.go` (or new `internal/eval/artifact_sandbox_test.go`)

**What to do**:

1. **Create a test case** that runs the same architect eval case in both native and sandbox mode
2. **Compare the scored output** to verify both contain `--- PRODUCED ARTIFACT ---` with the spec content
3. **Verify artifact structure** matches (file path headers like `=== .howmux/specs/issue-*.md ===`)

**Test structure**:
```go
func TestArtifactCollectionParity(t *testing.T) {
    // Skip if Docker not available
    if testing.Short() {
        t.Skip("skipping Docker-dependent test in short mode")
    }
    
    // Load a simple architect test case
    tc := getArchitectTestCase(t)
    
    // Run native path
    nativeOutput, _, _, _, err := invokeAgentNative("architect", tc.Input)
    require.NoError(t, err)
    
    // Run sandbox path (with minimal container config)
    cConfig := createMinimalContainerConfig()
    sandboxOutput, _, _, err := invokeAgentInContainer("architect", tc.Input, cConfig)
    require.NoError(t, err)
    
    // Both should contain the artifact marker
    assert.Contains(t, nativeOutput, "--- PRODUCED ARTIFACT ---")
    assert.Contains(t, sandboxOutput, "--- PRODUCED ARTIFACT ---")
    
    // Both should contain the spec file header
    assert.Contains(t, nativeOutput, "=== .howmux/specs/issue-")
    assert.Contains(t, sandboxOutput, "=== .howmux/specs/issue-")
    
    // Artifact content structure should match (not exact text, since the spec
    // content may vary slightly between runs, but the file structure should match)
    nativeLines := strings.Split(nativeOutput, "\n")
    sandboxLines := strings.Split(sandboxOutput, "\n")
    
    // Find artifact start in both outputs
    nativeArtifactIdx := -1
    sandboxArtifactIdx := -1
    for i, line := range nativeLines {
        if strings.Contains(line, "--- PRODUCED ARTIFACT ---") {
            nativeArtifactIdx = i
            break
        }
    }
    for i, line := range sandboxLines {
        if strings.Contains(line, "--- PRODUCED ARTIFACT ---") {
            sandboxArtifactIdx = i
            break
        }
    }
    
    require.Greater(t, nativeArtifactIdx, -1, "native output should contain artifact")
    require.Greater(t, sandboxArtifactIdx, -1, "sandbox output should contain artifact")
}
```

**Acceptance Criteria**:

1. Test verifies both native and sandbox paths produce equivalent scored output structure
2. Test confirms artifact content is present in both outputs (not just narration)
3. Test is skipped gracefully when Docker is not available (using `testing.Short()` or checking Docker availability)
4. Test uses a minimal container config (fast, no need for full eval run)

**Dependencies**: Task 1 (artifact collection must be implemented before the test can pass)

---

### Task 3: Update Documentation

**Location**: `docs/unified-container-flow.md` (and optionally `docs/evaluation.md`)

**What to do**:

1. **Add a section** under "Complete Flow Analysis" titled "Workspace Bind-Mount (Artifact Collection)"
2. **Document** that the container workspace is bind-mounted to a host temp directory so agent-produced files are host-visible
3. **Note** that `collectArtifacts` runs on the mounted host path after the agent turn, matching the native path behavior

**Section content**:
```markdown
### Workspace Bind-Mount (Artifact Collection)

**Location:** `internal/eval/runner.go` - `invokeAgentInContainer()`
**Purpose:** Make agent-produced artifacts host-visible for scoring

**Steps:**
1. Create host temp directory: `os.MkdirTemp("", "howmux-eval-sandbox-*")`
2. Bind-mount to container workspace: `hostConfig.Binds = []string{"{host}:{container}"}`
3. Agent writes artifacts to container workspace (e.g., `/workspace/.howmux/specs/issue-*.md`)
4. Artifacts land on mounted host directory via bind-mount
5. After agent turn, `collectArtifacts(agent, hostWorkspaceDir)` reads from host directory
6. Artifact content appended to scored output under `--- PRODUCED ARTIFACT ---`
7. Host temp directory cleaned up via `defer os.RemoveAll(hostWorkspaceDir)`

**Why This Approach:**
- **Parity**: Native and sandbox paths both read artifacts from a real filesystem after the agent turn
- **Simplicity**: No extraction logic needed; `collectArtifacts` works unchanged
- **Standard Practice**: Bind-mounting workspaces is the common Docker eval pattern

**Performance:** ~negligible (bind-mount setup <1ms, temp dir cleanup <10ms)
```

**Acceptance Criteria**:

1. Documentation clearly explains the bind-mount approach and its purpose
2. Documentation includes the performance impact (negligible)
3. Documentation references the parity goal (native and sandbox score identically)

**Dependencies**: Task 1 (document after implementation)

---

## Team Orchestration

**Task execution order**:
- Task 1 and Task 2 can run **in parallel** (Task 2 will fail until Task 1 is complete, but builder can implement both concurrently)
- Task 3 depends on Task 1 (document the actual implementation, not the plan)

**Single PR**: All tasks contribute to complete issue resolution in one pull request.

## Validation Commands

```bash
# Build and vet
go build ./...
go vet ./internal/eval/...

# Unit tests (artifact collector logic)
go test ./internal/eval -v -run TestCollectArtifacts

# Parity test (new test from Task 2)
go test ./internal/eval -v -run TestArtifactCollectionParity

# Behavioral validation (requires Docker + kiro-cli)
# Native path (baseline)
howmux eval architect simple-task > /tmp/native-output.txt

# Sandbox path (should now match native)
howmux eval architect simple-task --sandbox > /tmp/sandbox-output.txt

# Verify both contain artifacts
grep -A 5 "--- PRODUCED ARTIFACT ---" /tmp/native-output.txt
grep -A 5 "--- PRODUCED ARTIFACT ---" /tmp/sandbox-output.txt

# Both should show the spec file content, not just narration
```

**Expected result**: Both outputs contain `--- PRODUCED ARTIFACT ---` with the spec content. The sandbox output is no longer empty after that marker.

## Out of Scope

- **Native-path artifact scoring**: Already done (#42)
- **Builder artifact scoring**: Separate follow-up (builder needs git diff extraction, which has additional complexity in containers)
- **Other agents**: Validator and krew-lead produce no artifacts to collect; documenter follows the same pattern as architect

## Success Criteria

1. ✅ Sandboxed architect runs score the written spec, not the narration
2. ✅ Artifact content in sandbox mode matches native mode structure
3. ✅ `collectArtifacts` works unchanged (no new extraction logic)
4. ✅ Tests verify parity between native and sandbox artifact scoring
5. ✅ Host temp workspace is cleaned up after each run
6. ✅ Documentation explains the bind-mount approach and why it was chosen

## Notes

- **Why not copy out artifacts?** Bind-mounting is simpler, faster, and keeps native/sandbox identical. Copying would introduce a divergent code path.
- **Docker layer caching**: Bind-mounts don't affect Docker's layer caching; the temp directory is created fresh each run.
- **Security**: The temp workspace is created with `os.MkdirTemp`, which uses secure permissions (0700). It's cleaned up immediately after the turn via `defer`.
- **Existing tests**: Docker-sandbox eval tests are known to fail locally without a daemon (pre-existing, noted in issue). The new parity test should skip gracefully when Docker is unavailable.
