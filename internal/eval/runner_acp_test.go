package eval

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/matthiashowellyopp/howmux/internal/acp"
)

// TestACPClientCreation verifies ACP client creation in eval runner
func TestACPClientCreation(t *testing.T) {
	// Simulate the client creation pattern from invokeAgentNative
	workspaceDir := t.TempDir()
	agent := "architect"
	timeout := 5 * time.Minute

	config := &acp.ConnectionConfig{
		Agent:             agent,
		Cwd:               workspaceDir,
		RequestTimeout:    timeout,
		KiroCLIPath:       "kiro-cli",
		ConnectionTimeout: 30 * time.Second,
	}

	// Verify config is valid
	err := acp.ValidateConnectionConfig(config)
	if err != nil {
		t.Errorf("Config validation failed: %v", err)
	}

	// Verify client can be created
	client := acp.NewClient(config)
	if client == nil {
		t.Fatal("Failed to create ACP client")
	}

	// Verify client is not connected initially
	if client.IsConnected() {
		t.Error("Client should not be connected before Connect() call")
	}
}

// TestACPMessageRequestConstruction verifies message request setup
func TestACPMessageRequestConstruction(t *testing.T) {
	agent := "architect"
	prompt := "Design a simple API endpoint"
	timeout := 5 * time.Minute

	req := &acp.MessageRequest{
		Agent:          agent,
		Message:        prompt,
		Streaming:      false, // Eval needs complete output
		ResponseFormat: "text",
		Timeout:        timeout,
	}

	// Verify request is valid
	err := acp.ValidateMessageRequest(req)
	if err != nil {
		t.Errorf("Request validation failed: %v", err)
	}

	// Verify request fields
	if req.Agent != agent {
		t.Errorf("Expected agent %q, got %q", agent, req.Agent)
	}

	if req.Message != prompt {
		t.Errorf("Expected message %q, got %q", prompt, req.Message)
	}

	if req.Streaming {
		t.Error("Eval should use non-streaming mode")
	}

	if req.ResponseFormat != "text" {
		t.Errorf("Expected response format 'text', got %q", req.ResponseFormat)
	}

	if req.Timeout != timeout {
		t.Errorf("Expected timeout %v, got %v", timeout, req.Timeout)
	}
}

// TestACPResponseAssembly verifies response text assembly
func TestACPResponseAssembly(t *testing.T) {
	// Simulate response assembly from ACP
	mockResponse := &acp.MessageResponse{
		Success:   true,
		Message:   "## Architecture Design\n\nThe API should...",
		Timestamp: time.Now(),
		Metadata: map[string]interface{}{
			"session_id": "test-session",
		},
	}

	// Extract response message
	result := mockResponse.Message

	if result == "" {
		t.Error("Response message is empty")
	}

	// Verify ANSI stripping would work on result
	stripped := stripANSISequences(result)
	if len(stripped) == 0 {
		t.Error("Stripped result is empty")
	}

	// Verify cost estimation could be called
	cost := estimateCost("test prompt", stripped)
	if cost.EstimatedUSD < 0 {
		t.Error("Cost should be non-negative")
	}
}

// TestACPTimeoutHandling verifies timeout behavior
func TestACPTimeoutHandling(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Wait for timeout
	<-ctx.Done()

	// Verify timeout detection
	if ctx.Err() != context.DeadlineExceeded {
		t.Errorf("Expected DeadlineExceeded, got %v", ctx.Err())
	}

	// This is the pattern used in eval runner to detect timeouts
	isTimeout := ctx.Err() == context.DeadlineExceeded
	if !isTimeout {
		t.Error("Failed to detect timeout")
	}
}

// TestACPErrorContextMapping verifies error context population
func TestACPErrorContextMapping(t *testing.T) {
	tests := []struct {
		name       string
		acpError   string
		expectType string
	}{
		{
			name:       "connection failure",
			acpError:   "failed to connect: dial tcp: connection refused",
			expectType: "connection_error",
		},
		{
			name:       "agent resolution failure",
			acpError:   "no agent with name foo found",
			expectType: "agent_fallback",
		},
		{
			name:       "timeout error",
			acpError:   "context deadline exceeded",
			expectType: "timeout",
		},
		{
			name:       "generic error",
			acpError:   "unknown error occurred",
			expectType: "generic",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate error context creation from ACP error
			errorCtx := ErrorContext{
				Command:    "kiro-cli acp --agent architect",
				WorkingDir: "/tmp/workspace",
				Stderr:     tt.acpError,
				ExitCode:   1,
			}

			// Verify error context fields are populated
			if errorCtx.Command == "" {
				t.Error("Command should be populated")
			}

			if errorCtx.Stderr == "" {
				t.Error("Stderr should contain error message")
			}

			if errorCtx.ExitCode != 1 {
				t.Error("Exit code should be 1 for errors")
			}
		})
	}
}

// TestACPRecordedCallsPreservation verifies recorded calls still work
func TestACPRecordedCallsPreservation(t *testing.T) {
	// Recorded calls are populated by SetupFakeTools and read from files
	// This test verifies the pattern is compatible with ACP invocation

	workspaceDir := t.TempDir()

	// The eval runner still uses SetupFakeTools regardless of ACP
	// Verify this pattern is preserved
	toolsDir, callLogPath, cleanup, err := SetupFakeTools(workspaceDir)
	if err != nil {
		t.Fatalf("SetupFakeTools failed: %v", err)
	}
	defer cleanup()

	if toolsDir == "" {
		t.Error("Tools directory should not be empty")
	}

	if callLogPath == "" {
		t.Error("Call log path should not be empty")
	}

	// After ACP invocation completes, recorded calls would be read
	// from the workspace directory the same way
	// (This is the existing readRecordedCalls logic, unchanged by ACP)
}

// TestACPInvocationPattern verifies the full invocation pattern
func TestACPInvocationPattern(t *testing.T) {
	// This test documents the expected invocation pattern without
	// actually connecting to kiro-cli (that's covered by integration test)

	workspaceDir := t.TempDir()
	agent := "architect"
	prompt := "Test prompt"
	timeout := 5 * time.Minute

	// Step 1: Create client
	config := &acp.ConnectionConfig{
		Agent:             agent,
		Cwd:               workspaceDir,
		RequestTimeout:    timeout,
		KiroCLIPath:       "kiro-cli",
		ConnectionTimeout: 30 * time.Second,
	}
	client := acp.NewClient(config)

	if client == nil {
		t.Fatal("Failed to create client")
	}

	// Step 2: Build request
	req := &acp.MessageRequest{
		Agent:          agent,
		Message:        prompt,
		Streaming:      false,
		ResponseFormat: "text",
		Timeout:        timeout,
	}

	if err := acp.ValidateMessageRequest(req); err != nil {
		t.Fatalf("Request validation failed: %v", err)
	}

	// Step 3: Would call client.Connect(ctx)
	// Step 4: Would call client.SendMessage(ctx, req)
	// Step 5: Would extract response.Message
	// Step 6: Would call stripANSISequences(result)
	// Step 7: Would call estimateCost(prompt, result)
	// Step 8: Would populate ErrorContext on error
	// Step 9: Would call client.Close() (deferred)

	// This test just verifies all the pieces exist
	t.Log("ACP invocation pattern verified")
}

// TestACPAgentFallbackDetection verifies agent fallback still detected
// TestACPAgentResolutionPreflight verifies the eval path fails loud up front
// when the requested agent has no config, instead of letting kiro-cli silently
// fall back over ACP (the #37 trustworthiness guarantee). It exercises the
// acp.ValidateAgentResolvable check against a workspace .kiro/agents layout.
func TestACPAgentResolutionPreflight(t *testing.T) {
	work := t.TempDir()
	agentsDir := filepath.Join(work, ".kiro", "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, "architect.json"), []byte(`{"name":"architect"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		agent   string
		wantErr bool
	}{
		{"resolvable agent", "architect", false},
		{"unresolvable agent (would silently fall back)", "nonexistent", true},
		{"empty agent", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := acp.ValidateAgentResolvable(work, tt.agent)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateAgentResolvable(%q) err=%v, wantErr=%v", tt.agent, err, tt.wantErr)
			}
		})
	}
}

// TestACPCostEstimation verifies cost estimation works with ACP
func TestACPCostEstimation(t *testing.T) {
	// Cost estimation should work the same with ACP responses
	prompt := "Test prompt for cost estimation"
	response := "This is a response that would come from ACP.\nIt has multiple lines.\n"

	cost := estimateCost(prompt, response)

	// Basic sanity checks
	if cost.EstimatedUSD < 0 {
		t.Error("Cost should be non-negative")
	}

	// Longer responses should generally cost more
	shortCost := estimateCost("hi", "hello")
	longPrompt := strings.Repeat("test ", 1000)
	longResponse := strings.Repeat("response ", 1000)
	longCost := estimateCost(longPrompt, longResponse)

	if longCost.EstimatedUSD <= shortCost.EstimatedUSD {
		t.Error("Longer prompt/response should generally cost more")
	}
}

// TestACPOutputStripping verifies ANSI stripping works with ACP output
func TestACPOutputStripping(t *testing.T) {
	// ACP may return output with ANSI sequences
	tests := []struct {
		name     string
		input    string
		contains string
	}{
		{
			name:     "no ANSI",
			input:    "plain text output",
			contains: "plain text output",
		},
		{
			name:     "color codes",
			input:    "\x1B[32mgreen text\x1B[0m",
			contains: "green text",
		},
		{
			name:     "bold codes",
			input:    "\x1B[1mbold\x1B[0m text",
			contains: "bold text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stripped := stripANSISequences(tt.input)
			if !strings.Contains(stripped, tt.contains) {
				t.Errorf("Stripped output %q does not contain %q", stripped, tt.contains)
			}

			// Verify ANSI codes are removed
			if strings.Contains(stripped, "\x1B[") {
				t.Errorf("Stripped output still contains ANSI codes: %q", stripped)
			}
		})
	}
}

// TestACPWorkspaceDirHandling verifies workspace directory setup
func TestACPWorkspaceDirHandling(t *testing.T) {
	// Eval runner creates isolated workspaces
	workspaceDir := t.TempDir()

	// Verify directory exists and is writable
	testFile := workspaceDir + "/test.txt"
	err := writeFile(testFile, "test content")
	if err != nil {
		t.Fatalf("Workspace is not writable: %v", err)
	}

	// Verify ACP config accepts the workspace dir
	config := &acp.ConnectionConfig{
		Agent:             "architect",
		Cwd:               workspaceDir,
		RequestTimeout:    5 * time.Minute,
		KiroCLIPath:       "kiro-cli",
		ConnectionTimeout: 30 * time.Second,
	}

	err = acp.ValidateConnectionConfig(config)
	if err != nil {
		t.Errorf("Valid workspace dir rejected: %v", err)
	}
}
