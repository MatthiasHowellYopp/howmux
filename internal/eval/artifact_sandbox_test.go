package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matthiashowellyopp/howmux/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestArtifactCollectionParity verifies that native and sandbox execution paths
// both collect artifacts and include them in scored output.
//
// This test addresses issue #50: sandboxed runs were scoring only narration
// because artifacts written inside the container were not host-visible.
// The bind-mount solution makes both paths equivalent.
func TestArtifactCollectionParity(t *testing.T) {
	// Skip if Docker not available or in short mode
	if testing.Short() {
		t.Skip("skipping Docker-dependent test in short mode")
	}

	// Early Docker check
	if err := checkDockerAvailability(); err != nil {
		t.Skipf("Docker not available: %v", err)
	}

	// Use a simple architect test case - create minimal test input
	testInput := `Analyze this issue and create a design specification.

Issue: Add health check endpoint to the API server.

Requirements:
- Add GET /health endpoint
- Return 200 OK with {"status": "healthy"}
- Add tests

Write the spec to .howmux/specs/issue-123-health-check.md`

	// Run native path
	t.Run("native_produces_artifact", func(t *testing.T) {
		nativeOutput, _, _, _, err := invokeAgentNative("architect", testInput)
		require.NoError(t, err, "native execution should succeed")

		// Verify artifact marker is present
		assert.Contains(t, nativeOutput, "--- PRODUCED ARTIFACT ---",
			"native output should contain artifact marker")

		// Verify spec file header is present
		assert.Contains(t, nativeOutput, "=== .howmux/specs/issue-",
			"native output should contain spec file header")

		// Artifact content should be substantial (more than just narration)
		artifactStart := strings.Index(nativeOutput, "--- PRODUCED ARTIFACT ---")
		if artifactStart != -1 {
			artifactSection := nativeOutput[artifactStart:]
			assert.Greater(t, len(artifactSection), 200,
				"artifact section should contain substantial spec content")
		}
	})

	// Run sandbox path
	t.Run("sandbox_produces_artifact", func(t *testing.T) {
		cConfig := createMinimalContainerConfig()
		sandboxOutput, _, _, err := invokeAgentInContainer("architect", testInput, cConfig)
		require.NoError(t, err, "sandbox execution should succeed")

		// Verify artifact marker is present (same as native)
		assert.Contains(t, sandboxOutput, "--- PRODUCED ARTIFACT ---",
			"sandbox output should contain artifact marker")

		// Verify spec file header is present (same as native)
		assert.Contains(t, sandboxOutput, "=== .howmux/specs/issue-",
			"sandbox output should contain spec file header")

		// Artifact content should be substantial
		artifactStart := strings.Index(sandboxOutput, "--- PRODUCED ARTIFACT ---")
		if artifactStart != -1 {
			artifactSection := sandboxOutput[artifactStart:]
			assert.Greater(t, len(artifactSection), 200,
				"artifact section should contain substantial spec content")
		}
	})

	// Run parity comparison
	t.Run("native_and_sandbox_structure_matches", func(t *testing.T) {
		// Run both paths
		nativeOutput, _, _, _, err := invokeAgentNative("architect", testInput)
		require.NoError(t, err, "native execution should succeed")

		cConfig := createMinimalContainerConfig()
		sandboxOutput, _, _, err := invokeAgentInContainer("architect", testInput, cConfig)
		require.NoError(t, err, "sandbox execution should succeed")

		// Both should have artifact markers
		nativeHasArtifact := strings.Contains(nativeOutput, "--- PRODUCED ARTIFACT ---")
		sandboxHasArtifact := strings.Contains(sandboxOutput, "--- PRODUCED ARTIFACT ---")

		assert.True(t, nativeHasArtifact, "native should produce artifact")
		assert.True(t, sandboxHasArtifact, "sandbox should produce artifact")

		// Both should have spec file headers
		nativeHasSpec := strings.Contains(nativeOutput, "=== .howmux/specs/issue-")
		sandboxHasSpec := strings.Contains(sandboxOutput, "=== .howmux/specs/issue-")

		assert.True(t, nativeHasSpec, "native should include spec file")
		assert.True(t, sandboxHasSpec, "sandbox should include spec file")

		// Verify structural similarity (both have artifact sections of comparable length)
		nativeArtifactIdx := strings.Index(nativeOutput, "--- PRODUCED ARTIFACT ---")
		sandboxArtifactIdx := strings.Index(sandboxOutput, "--- PRODUCED ARTIFACT ---")

		if nativeArtifactIdx > -1 && sandboxArtifactIdx > -1 {
			nativeArtifactLen := len(nativeOutput) - nativeArtifactIdx
			sandboxArtifactLen := len(sandboxOutput) - sandboxArtifactIdx

			// Lengths should be in the same ballpark (within 50% of each other)
			// since both should contain the actual spec content
			ratio := float64(nativeArtifactLen) / float64(sandboxArtifactLen)
			assert.Greater(t, ratio, 0.5, "artifact sections should be comparable in length")
			assert.Less(t, ratio, 2.0, "artifact sections should be comparable in length")
		}
	})
}

// createMinimalContainerConfig returns a basic container config for testing.
// Uses minimal resource limits for fast test execution.
func createMinimalContainerConfig() *ContainerConfig {
	sandboxCfg := &config.SandboxConfig{
		WorkspaceDir: "/workspace",
		CPUCores:     1.0,
		MemoryMB:     512, // Minimal for architect spec writing
		Timeout:      0,   // Use default
	}

	return createContainerConfig(sandboxCfg, nil, false)
}

// TestArtifactCollectionCleanup verifies that temp workspace directories
// are properly cleaned up after sandbox runs.
func TestArtifactCollectionCleanup(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Docker-dependent test in short mode")
	}

	if err := checkDockerAvailability(); err != nil {
		t.Skipf("Docker not available: %v", err)
	}

	// Count temp dirs before
	tempDir := os.TempDir()
	beforeDirs, err := filepath.Glob(filepath.Join(tempDir, "howmux-eval-sandbox-*"))
	require.NoError(t, err)

	// Run a sandbox execution
	testInput := "Simple test input"
	cConfig := createMinimalContainerConfig()
	_, _, _, err = invokeAgentInContainer("architect", testInput, cConfig)
	// Don't require success - we're just testing cleanup
	_ = err

	// Count temp dirs after - should be the same (all cleaned up)
	afterDirs, err := filepath.Glob(filepath.Join(tempDir, "howmux-eval-sandbox-*"))
	require.NoError(t, err)

	assert.Equal(t, len(beforeDirs), len(afterDirs),
		"temp workspace directories should be cleaned up after execution")
}
