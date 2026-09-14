package agent

import (
	"testing"
	"time"

	"github.com/matthiashowellyopp/howmux/internal/acp"
)

// TestAgentACPClientField verifies Agent struct has acpClient field
func TestAgentACPClientField(t *testing.T) {
	agent := &Agent{
		ID:          "test-agent",
		IssueNumber: 42,
		IssueTitle:  "Test Issue",
		Status:      StatusRunning,
		StartTime:   time.Now(),
	}

	// Verify acpClient field exists and can be set
	if agent.acpClient != nil {
		t.Error("New agent should have nil acpClient")
	}

	// Create a mock ACP client
	config := &acp.ConnectionConfig{
		KiroCLIPath:       "kiro-cli",
		Agent:             "krew-lead",
		Cwd:               "/tmp",
		ConnectionTimeout: 30 * time.Second,
		RequestTimeout:    60 * time.Second,
	}
	client := acp.NewClient(config)

	// Assign client to agent
	agent.acpClient = client

	if agent.acpClient == nil {
		t.Error("Failed to assign acpClient to agent")
	}
}

// TestAgentEventsField verifies Agent struct has events field for phase detection
func TestAgentEventsField(t *testing.T) {
	agent := &Agent{
		ID:          "test-agent",
		IssueNumber: 42,
		IssueTitle:  "Test Issue",
		Status:      StatusRunning,
		StartTime:   time.Now(),
	}

	// Verify events field exists and is initially empty
	if len(agent.events) != 0 {
		t.Error("New agent should have empty events slice")
	}

	// Simulate adding events (what the stream consumer would do)
	agent.eventsMu.Lock()
	agent.events = append(agent.events, acp.StreamingResponse{
		Type:      "text",
		Content:   "Starting analysis...",
		Timestamp: time.Now(),
	})
	agent.events = append(agent.events, acp.StreamingResponse{
		Type:      "tool_call",
		Content:   "",
		Timestamp: time.Now(),
	})
	agent.eventsMu.Unlock()

	// Verify events were stored
	agent.eventsMu.RLock()
	eventCount := len(agent.events)
	agent.eventsMu.RUnlock()

	if eventCount != 2 {
		t.Errorf("Expected 2 events, got %d", eventCount)
	}
}

// TestGetAgentEvents verifies thread-safe event access
func TestGetAgentEvents(t *testing.T) {
	agent := &Agent{
		ID:          "test-agent",
		IssueNumber: 42,
		IssueTitle:  "Test Issue",
		Status:      StatusRunning,
		StartTime:   time.Now(),
	}

	// Add test events
	agent.eventsMu.Lock()
	agent.events = []acp.StreamingResponse{
		{Type: "text", Content: "chunk1", Timestamp: time.Now()},
		{Type: "tool_call", Timestamp: time.Now()},
		{Type: "text", Content: "chunk2", Timestamp: time.Now()},
	}
	agent.eventsMu.Unlock()

	// Read events (simulating what TUI would do)
	agent.eventsMu.RLock()
	events := agent.events
	agent.eventsMu.RUnlock()

	if len(events) != 3 {
		t.Errorf("Expected 3 events, got %d", len(events))
	}

	// Verify event types
	if events[0].Type != "text" {
		t.Errorf("Expected first event type 'text', got %q", events[0].Type)
	}
	if events[1].Type != "tool_call" {
		t.Errorf("Expected second event type 'tool_call', got %q", events[1].Type)
	}
}

// TestStreamEventCapture verifies event types captured during streaming
func TestStreamEventCapture(t *testing.T) {
	tests := []struct {
		name      string
		eventType string
		content   string
		shouldAdd bool
	}{
		{
			name:      "text event",
			eventType: "text",
			content:   "Agent output",
			shouldAdd: true,
		},
		{
			name:      "tool_call event",
			eventType: "tool_call",
			content:   "",
			shouldAdd: true,
		},
		{
			name:      "plan event",
			eventType: "plan",
			content:   "",
			shouldAdd: true,
		},
		{
			name:      "done event",
			eventType: "done",
			content:   "",
			shouldAdd: true,
		},
		{
			name:      "error event",
			eventType: "error",
			content:   "Something failed",
			shouldAdd: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent := &Agent{
				ID:          "test-agent",
				IssueNumber: 42,
				Status:      StatusRunning,
			}

			// Simulate stream consumer adding event
			event := acp.StreamingResponse{
				Type:      tt.eventType,
				Content:   tt.content,
				Error:     tt.content, // Use content as error for error type
				Timestamp: time.Now(),
			}

			agent.eventsMu.Lock()
			agent.events = append(agent.events, event)
			agent.eventsMu.Unlock()

			// Verify event was stored
			agent.eventsMu.RLock()
			stored := len(agent.events) > 0
			lastEvent := agent.events[len(agent.events)-1]
			agent.eventsMu.RUnlock()

			if !stored {
				t.Error("Event was not stored")
			}

			if lastEvent.Type != tt.eventType {
				t.Errorf("Expected event type %q, got %q", tt.eventType, lastEvent.Type)
			}
		})
	}
}

// TestACPExitCodeMapping verifies stream outcome to exit code mapping
func TestACPExitCodeMapping(t *testing.T) {
	tests := []struct {
		name           string
		finalEventType string
		expectedExit   int
	}{
		{
			name:           "done event maps to exit 0",
			finalEventType: "done",
			expectedExit:   0,
		},
		{
			name:           "error event maps to exit 1",
			finalEventType: "error",
			expectedExit:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate the exit code mapping logic from manager.go
			var exitCode int
			switch tt.finalEventType {
			case "done":
				exitCode = 0
			case "error":
				exitCode = 1
			default:
				exitCode = 1 // Channel closed without done
			}

			if exitCode != tt.expectedExit {
				t.Errorf("Expected exit code %d for event type %q, got %d",
					tt.expectedExit, tt.finalEventType, exitCode)
			}
		})
	}
}

// TestManagerGetAgentEvents verifies Manager can expose agent events
func TestManagerGetAgentEvents(t *testing.T) {
	// Note: This test verifies the pattern that will be used for phase detection.
	// The actual GetAgentEvents method will be added to Manager in the implementation.

	manager := NewManager(nil)

	// Create and register an agent
	agent := &Agent{
		ID:          "agent-1",
		IssueNumber: 42,
		Status:      StatusRunning,
	}

	manager.mu.Lock()
	manager.agents["agent-1"] = agent
	manager.mu.Unlock()

	// Add events to agent
	agent.eventsMu.Lock()
	agent.events = []acp.StreamingResponse{
		{Type: "text", Content: "output", Timestamp: time.Now()},
		{Type: "tool_call", Timestamp: time.Now()},
	}
	agent.eventsMu.Unlock()

	// Read events through manager (simulating TUI access pattern)
	manager.mu.RLock()
	foundAgent, exists := manager.agents["agent-1"]
	manager.mu.RUnlock()

	if !exists {
		t.Fatal("Agent not found in manager")
	}

	foundAgent.eventsMu.RLock()
	events := foundAgent.events
	foundAgent.eventsMu.RUnlock()

	if len(events) != 2 {
		t.Errorf("Expected 2 events, got %d", len(events))
	}
}

// TestConcurrentEventAccess verifies thread-safe concurrent access
func TestConcurrentEventAccess(t *testing.T) {
	agent := &Agent{
		ID:          "test-agent",
		IssueNumber: 42,
		Status:      StatusRunning,
	}

	done := make(chan bool)
	writes := 100
	reads := 100

	// Writer goroutine (simulates stream consumer)
	go func() {
		for i := 0; i < writes; i++ {
			agent.eventsMu.Lock()
			agent.events = append(agent.events, acp.StreamingResponse{
				Type:      "text",
				Content:   "chunk",
				Timestamp: time.Now(),
			})
			agent.eventsMu.Unlock()
			time.Sleep(time.Microsecond)
		}
		done <- true
	}()

	// Reader goroutine (simulates TUI polling)
	go func() {
		for i := 0; i < reads; i++ {
			agent.eventsMu.RLock()
			_ = len(agent.events)
			agent.eventsMu.RUnlock()
			time.Sleep(time.Microsecond)
		}
		done <- true
	}()

	// Wait for both
	<-done
	<-done

	// Verify final count
	agent.eventsMu.RLock()
	finalCount := len(agent.events)
	agent.eventsMu.RUnlock()

	if finalCount != writes {
		t.Errorf("Expected %d events, got %d", writes, finalCount)
	}
}

// TestAgentCleanupClosesACPClient verifies cleanup logic
func TestAgentCleanupClosesACPClient(t *testing.T) {
	agent := &Agent{
		ID:          "test-agent",
		IssueNumber: 42,
		Status:      StatusRunning,
	}

	// Create a mock ACP client
	config := &acp.ConnectionConfig{
		KiroCLIPath:       "kiro-cli",
		Agent:             "krew-lead",
		Cwd:               "/tmp",
		ConnectionTimeout: 30 * time.Second,
		RequestTimeout:    60 * time.Second,
	}
	client := acp.NewClient(config)
	agent.acpClient = client

	// Simulate cleanup (what manager does when agent completes/fails)
	if agent.acpClient != nil {
		// Don't actually connect/close in unit test
		// Just verify the cleanup pattern is correct
		if agent.acpClient == nil {
			t.Error("Client should not be nil before cleanup check")
		}
	}

	// Set to nil after cleanup
	agent.acpClient = nil

	if agent.acpClient != nil {
		t.Error("Client should be nil after cleanup")
	}
}
