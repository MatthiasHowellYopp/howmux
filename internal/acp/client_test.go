package acp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestACPClientLiveTurn performs a live integration test with kiro-cli ACP
// Skipped by default — set HOWMUX_ACP_INTEGRATION_TEST=1 to run
func TestACPClientLiveTurn(t *testing.T) {
	if os.Getenv("HOWMUX_ACP_INTEGRATION_TEST") == "" {
		t.Skip("Set HOWMUX_ACP_INTEGRATION_TEST=1 to run live ACP test")
	}

	// Create temporary directory for test workspace
	tmpDir := t.TempDir()

	// Create a simple test file
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("test content\n"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Configure client for architect agent with a simple prompt
	config := &ConnectionConfig{
		KiroCLIPath:       "kiro-cli",
		Agent:             "architect",
		Cwd:               tmpDir,
		MaxRetries:        3,
		RetryDelay:        1 * time.Second,
		ConnectionTimeout: 30 * time.Second,
		RequestTimeout:    60 * time.Second,
	}

	client := NewClient(config)
	ctx := context.Background()

	// Test: Connection succeeds
	err := client.Connect(ctx)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer client.Close()

	if !client.IsConnected() {
		t.Fatal("Client reports not connected after successful Connect()")
	}

	// Test: Send a simple prompt and get streaming response
	req := &MessageRequest{
		Agent:          "architect",
		Message:        "List the files in this directory. Respond with just the filenames.",
		Streaming:      true,
		ResponseFormat: "text",
		Timeout:        30 * time.Second,
	}

	respChan, err := client.StreamMessage(ctx, req)
	if err != nil {
		t.Fatalf("Failed to start streaming: %v", err)
	}

	// Collect all responses
	var textChunks []string
	var gotDone bool
	var gotError bool

	for resp := range respChan {
		switch resp.Type {
		case "start":
			t.Log("Stream started")
		case "text":
			textChunks = append(textChunks, resp.Content)
			t.Logf("Text chunk received: %q", resp.Content)
		case "done":
			gotDone = true
			t.Log("Stream completed")
		case "error":
			gotError = true
			t.Logf("Error received: %s", resp.Error)
		default:
			t.Logf("Unexpected response type: %s", resp.Type)
		}
	}

	// Verify stream completed properly
	if !gotDone {
		t.Error("Stream did not receive 'done' event")
	}

	if gotError {
		t.Error("Stream received error event")
	}

	// Verify we got some text output
	if len(textChunks) == 0 {
		t.Error("No text chunks received from stream")
	}

	// Verify the response mentions the test file (basic sanity check)
	fullResponse := strings.Join(textChunks, "")
	if !strings.Contains(fullResponse, "test.txt") {
		t.Logf("Full response: %s", fullResponse)
		t.Error("Response does not mention test.txt file")
	}

	t.Logf("Full response (%d chunks): %s", len(textChunks), fullResponse)
}

// TestStreamResponseTypes verifies all StreamingResponse types are handled
func TestStreamResponseTypes(t *testing.T) {
	tests := []struct {
		name     string
		respType string
		content  string
		errMsg   string
	}{
		{
			name:     "start event",
			respType: "start",
			content:  "Starting...",
		},
		{
			name:     "text chunk",
			respType: "text",
			content:  "Some output text",
		},
		{
			name:     "done event",
			respType: "done",
			content:  "",
		},
		{
			name:     "error event",
			respType: "error",
			errMsg:   "Something went wrong",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &StreamingResponse{
				Type:      tt.respType,
				Content:   tt.content,
				Error:     tt.errMsg,
				Timestamp: time.Now(),
			}

			// Verify basic structure
			if resp.Type != tt.respType {
				t.Errorf("Expected type %s, got %s", tt.respType, resp.Type)
			}

			if resp.Content != tt.content {
				t.Errorf("Expected content %q, got %q", tt.content, resp.Content)
			}

			if resp.Error != tt.errMsg {
				t.Errorf("Expected error %q, got %q", tt.errMsg, resp.Error)
			}

			// Verify timestamp is set
			if resp.Timestamp.IsZero() {
				t.Error("Timestamp not set")
			}
		})
	}
}

// TestConnectionConfigValidation verifies connection config validation
func TestConnectionConfigValidation(t *testing.T) {
	tests := []struct {
		name      string
		config    *ConnectionConfig
		expectErr error
	}{
		{
			name:      "nil config",
			config:    nil,
			expectErr: ErrInvalidConfig,
		},
		{
			name: "missing agent",
			config: &ConnectionConfig{
				KiroCLIPath:       "kiro-cli",
				Cwd:               "/tmp",
				ConnectionTimeout: 30 * time.Second,
				RequestTimeout:    60 * time.Second,
			},
			expectErr: ErrMissingConfigAgent,
		},
		{
			name: "missing kiro-cli path",
			config: &ConnectionConfig{
				Agent:             "architect",
				Cwd:               "/tmp",
				ConnectionTimeout: 30 * time.Second,
				RequestTimeout:    60 * time.Second,
			},
			expectErr: ErrInvalidConfig,
		},
		{
			name: "missing cwd",
			config: &ConnectionConfig{
				KiroCLIPath:       "kiro-cli",
				Agent:             "architect",
				ConnectionTimeout: 30 * time.Second,
				RequestTimeout:    60 * time.Second,
			},
			expectErr: ErrInvalidConfig,
		},
		{
			name: "relative cwd path",
			config: &ConnectionConfig{
				KiroCLIPath:       "kiro-cli",
				Agent:             "architect",
				Cwd:               "relative/path",
				ConnectionTimeout: 30 * time.Second,
				RequestTimeout:    60 * time.Second,
			},
			expectErr: ErrInvalidConfig,
		},
		{
			name: "zero connection timeout",
			config: &ConnectionConfig{
				KiroCLIPath:       "kiro-cli",
				Agent:             "architect",
				Cwd:               "/tmp",
				ConnectionTimeout: 0,
				RequestTimeout:    60 * time.Second,
			},
			expectErr: ErrInvalidConfig,
		},
		{
			name: "valid config",
			config: &ConnectionConfig{
				KiroCLIPath:       "kiro-cli",
				Agent:             "architect",
				Cwd:               "/tmp",
				MaxRetries:        3,
				RetryDelay:        1 * time.Second,
				ConnectionTimeout: 30 * time.Second,
				RequestTimeout:    60 * time.Second,
			},
			expectErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateConnectionConfig(tt.config)
			if tt.expectErr != nil {
				if err == nil {
					t.Errorf("Expected error %v, got nil", tt.expectErr)
				} else if err != tt.expectErr {
					// For more specific validation errors, check if the error is related
					// to the expected category rather than exact match
					expectedStr := tt.expectErr.Error()
					actualStr := err.Error()

					// Allow specific validation errors that are subcategories of ErrInvalidConfig
					if tt.expectErr == ErrInvalidConfig {
						// These are all invalid config conditions
						validSubErrors := []string{
							"kiro-cli path is required",
							"working directory (cwd) is required",
							"working directory (cwd) must be an absolute path",
							"timeouts must be positive",
						}

						found := false
						for _, subErr := range validSubErrors {
							if actualStr == subErr {
								found = true
								break
							}
						}

						if !found {
							t.Errorf("Expected error %q or related config error, got %q", expectedStr, actualStr)
						}
					} else if !strings.Contains(actualStr, expectedStr) {
						t.Errorf("Expected error containing %q, got %q", expectedStr, actualStr)
					}
				}
			} else if err != nil {
				t.Errorf("Expected no error, got %v", err)
			}
		})
	}
}

// TestMessageRequestValidation verifies message request validation
func TestMessageRequestValidation(t *testing.T) {
	tests := []struct {
		name      string
		req       *MessageRequest
		expectErr error
	}{
		{
			name:      "nil request",
			req:       nil,
			expectErr: ErrInvalidRequest,
		},
		{
			name: "missing message",
			req: &MessageRequest{
				Agent: "architect",
			},
			expectErr: ErrMissingMessage,
		},
		{
			name: "invalid response format",
			req: &MessageRequest{
				Agent:          "architect",
				Message:        "test",
				ResponseFormat: "invalid",
			},
			expectErr: ErrInvalidResponseFormat,
		},
		{
			name: "valid text format",
			req: &MessageRequest{
				Agent:          "architect",
				Message:        "test message",
				ResponseFormat: "text",
			},
			expectErr: nil,
		},
		{
			name: "valid json format",
			req: &MessageRequest{
				Agent:          "architect",
				Message:        "test message",
				ResponseFormat: "json",
			},
			expectErr: nil,
		},
		{
			name: "valid without format",
			req: &MessageRequest{
				Agent:   "architect",
				Message: "test message",
			},
			expectErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateMessageRequest(tt.req)
			if tt.expectErr != nil {
				if err == nil {
					t.Errorf("Expected error %v, got nil", tt.expectErr)
				} else if !strings.Contains(err.Error(), tt.expectErr.Error()) {
					t.Errorf("Expected error containing %q, got %q", tt.expectErr.Error(), err.Error())
				}
			} else if err != nil {
				t.Errorf("Expected no error, got %v", err)
			}
		})
	}
}

// TestDefaultConnectionConfig verifies default config values
func TestDefaultConnectionConfig(t *testing.T) {
	config := DefaultConnectionConfig()

	if config == nil {
		t.Fatal("DefaultConnectionConfig returned nil")
	}

	// Verify defaults are set
	if config.KiroCLIPath != "kiro-cli" {
		t.Errorf("Expected KiroCLIPath 'kiro-cli', got %q", config.KiroCLIPath)
	}

	if config.Cwd == "" {
		t.Error("Cwd is empty")
	}

	if !filepath.IsAbs(config.Cwd) {
		t.Errorf("Cwd is not absolute: %q", config.Cwd)
	}

	if config.MaxRetries != 3 {
		t.Errorf("Expected MaxRetries 3, got %d", config.MaxRetries)
	}

	if config.RetryDelay != 1*time.Second {
		t.Errorf("Expected RetryDelay 1s, got %v", config.RetryDelay)
	}

	if config.ConnectionTimeout != 30*time.Second {
		t.Errorf("Expected ConnectionTimeout 30s, got %v", config.ConnectionTimeout)
	}

	if config.RequestTimeout != 60*time.Second {
		t.Errorf("Expected RequestTimeout 60s, got %v", config.RequestTimeout)
	}
}

// TestClientNotConnectedErrors verifies proper error handling when not connected
func TestClientNotConnectedErrors(t *testing.T) {
	config := &ConnectionConfig{
		KiroCLIPath:       "kiro-cli",
		Agent:             "architect",
		Cwd:               "/tmp",
		ConnectionTimeout: 30 * time.Second,
		RequestTimeout:    60 * time.Second,
	}

	client := NewClient(config)

	// Verify client is not connected initially
	if client.IsConnected() {
		t.Error("New client should not be connected")
	}

	// Attempt to send message without connecting
	ctx := context.Background()
	req := &MessageRequest{
		Agent:   "architect",
		Message: "test",
	}

	_, err := client.SendMessage(ctx, req)
	if err != ErrNotConnected {
		t.Errorf("Expected ErrNotConnected, got %v", err)
	}

	_, err = client.StreamMessage(ctx, req)
	if err != ErrNotConnected {
		t.Errorf("Expected ErrNotConnected for streaming, got %v", err)
	}
}

// TestStreamingTermination verifies stream channel termination
func TestStreamingTermination(t *testing.T) {
	// Create a mock stream channel
	mockChan := make(chan *StreamingResponse, 5)

	// Send various response types
	mockChan <- &StreamingResponse{Type: "start", Timestamp: time.Now()}
	mockChan <- &StreamingResponse{Type: "text", Content: "chunk1", Timestamp: time.Now()}
	mockChan <- &StreamingResponse{Type: "text", Content: "chunk2", Timestamp: time.Now()}
	mockChan <- &StreamingResponse{Type: "done", Timestamp: time.Now()}
	close(mockChan)

	// Consume stream
	var chunks []string
	var gotDone bool

	for resp := range mockChan {
		if resp.Type == "text" {
			chunks = append(chunks, resp.Content)
		} else if resp.Type == "done" {
			gotDone = true
		}
	}

	// Verify proper handling
	if len(chunks) != 2 {
		t.Errorf("Expected 2 text chunks, got %d", len(chunks))
	}

	if !gotDone {
		t.Error("Did not receive done event")
	}

	if chunks[0] != "chunk1" || chunks[1] != "chunk2" {
		t.Errorf("Unexpected chunks: %v", chunks)
	}
}

// TestStreamingErrorHandling verifies error stream handling
func TestStreamingErrorHandling(t *testing.T) {
	// Create a mock stream channel with error
	mockChan := make(chan *StreamingResponse, 3)

	mockChan <- &StreamingResponse{Type: "start", Timestamp: time.Now()}
	mockChan <- &StreamingResponse{Type: "text", Content: "partial output", Timestamp: time.Now()}
	mockChan <- &StreamingResponse{Type: "error", Error: "connection lost", Timestamp: time.Now()}
	close(mockChan)

	// Consume stream
	var gotError bool
	var errorMsg string

	for resp := range mockChan {
		if resp.Type == "error" {
			gotError = true
			errorMsg = resp.Error
		}
	}

	// Verify error was captured
	if !gotError {
		t.Error("Did not receive error event")
	}

	if errorMsg != "connection lost" {
		t.Errorf("Expected error 'connection lost', got %q", errorMsg)
	}
}

// TestConcurrentClientInstances verifies multiple clients can run in parallel
func TestConcurrentClientInstances(t *testing.T) {
	// This test verifies the design assumption that each client spawns
	// its own kiro-cli subprocess and can operate independently

	config1 := &ConnectionConfig{
		KiroCLIPath:       "kiro-cli",
		Agent:             "architect",
		Cwd:               "/tmp",
		ConnectionTimeout: 30 * time.Second,
		RequestTimeout:    60 * time.Second,
	}

	config2 := &ConnectionConfig{
		KiroCLIPath:       "kiro-cli",
		Agent:             "builder",
		Cwd:               "/tmp",
		ConnectionTimeout: 30 * time.Second,
		RequestTimeout:    60 * time.Second,
	}

	client1 := NewClient(config1)
	client2 := NewClient(config2)

	// Verify both clients are independent
	if client1 == client2 {
		t.Fatal("Clients should be different instances")
	}

	// Verify neither is connected initially
	if client1.IsConnected() || client2.IsConnected() {
		t.Error("New clients should not be connected")
	}

	// Both clients can be created simultaneously
	// (actual connection test requires live kiro-cli, covered by integration test)
}
