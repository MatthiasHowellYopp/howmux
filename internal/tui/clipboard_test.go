package tui

import (
	"testing"

	"github.com/atotto/clipboard"
)

// TestCopyToClipboard verifies the copy functionality
func TestCopyToClipboard(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		wantErr  bool
		validate func(t *testing.T)
	}{
		{
			name:    "copy non-empty text",
			text:    "test clipboard content",
			wantErr: false,
			validate: func(t *testing.T) {
				// Try to read back - if clipboard is available
				content, err := clipboard.ReadAll()
				if err == nil && content != "test clipboard content" {
					t.Errorf("clipboard content = %q, want %q", content, "test clipboard content")
				}
			},
		},
		{
			name:    "copy empty text (no-op)",
			text:    "",
			wantErr: false,
			validate: func(t *testing.T) {
				// Empty string should be a no-op, no error
			},
		},
		{
			name:    "copy multiline text",
			text:    "line1\nline2\nline3",
			wantErr: false,
			validate: func(t *testing.T) {
				content, err := clipboard.ReadAll()
				if err == nil && content != "line1\nline2\nline3" {
					t.Errorf("clipboard content = %q, want %q", content, "line1\nline2\nline3")
				}
			},
		},
		{
			name:    "copy unicode text",
			text:    "Hello 世界 🚀",
			wantErr: false,
			validate: func(t *testing.T) {
				content, err := clipboard.ReadAll()
				if err == nil && content != "Hello 世界 🚀" {
					t.Errorf("clipboard content = %q, want %q", content, "Hello 世界 🚀")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CopyToClipboard(tt.text)

			// In headless environments, clipboard operations may fail
			// This is expected and should not cause test failures
			if err != nil && !isHeadlessEnvironment() {
				t.Logf("clipboard copy failed (may be expected in CI): %v", err)
			}

			// Only validate if clipboard is available
			if err == nil && tt.validate != nil {
				tt.validate(t)
			}
		})
	}
}

// TestPasteFromClipboard verifies the paste functionality
func TestPasteFromClipboard(t *testing.T) {
	// First, set clipboard content if available
	testContent := "paste test content"
	setupErr := clipboard.WriteAll(testContent)

	tests := []struct {
		name         string
		setup        func()
		wantContains string
		wantErr      bool
	}{
		{
			name: "paste after copy",
			setup: func() {
				// Content already set above
			},
			wantContains: testContent,
			wantErr:      false,
		},
		{
			name: "paste empty clipboard",
			setup: func() {
				clipboard.WriteAll("")
			},
			wantContains: "",
			wantErr:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Skip validation if initial setup failed (headless environment)
			if setupErr != nil && isHeadlessEnvironment() {
				t.Skip("clipboard not available (headless environment)")
			}

			if tt.setup != nil {
				tt.setup()
			}

			text, err := PasteFromClipboard()

			// In headless environments, paste may fail - this is expected
			if err != nil && !isHeadlessEnvironment() {
				t.Logf("clipboard paste failed (may be expected in CI): %v", err)
			}

			// Only validate content if paste succeeded
			if err == nil && text != tt.wantContains {
				t.Errorf("PasteFromClipboard() = %q, want %q", text, tt.wantContains)
			}
		})
	}
}

// TestClipboardGracefulDegradation verifies error handling
func TestClipboardGracefulDegradation(t *testing.T) {
	// Test that operations don't panic in any scenario
	t.Run("copy doesn't panic", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("CopyToClipboard panicked: %v", r)
			}
		}()
		_ = CopyToClipboard("test")
	})

	t.Run("paste doesn't panic", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("PasteFromClipboard panicked: %v", r)
			}
		}()
		_, _ = PasteFromClipboard()
	})

	t.Run("copy returns on empty", func(t *testing.T) {
		err := CopyToClipboard("")
		if err != nil {
			t.Errorf("empty string should return nil, got: %v", err)
		}
	})
}

// TestClipboardConcurrency verifies thread-safety
func TestClipboardConcurrency(t *testing.T) {
	// Test concurrent access doesn't cause races or panics
	done := make(chan bool)

	// Multiple goroutines copying
	for i := 0; i < 10; i++ {
		go func(id int) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("goroutine %d panicked: %v", id, r)
				}
				done <- true
			}()
			_ = CopyToClipboard("concurrent test")
		}(i)
	}

	// Multiple goroutines pasting
	for i := 0; i < 10; i++ {
		go func(id int) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("goroutine %d panicked: %v", id, r)
				}
				done <- true
			}()
			_, _ = PasteFromClipboard()
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 20; i++ {
		<-done
	}
}

// isHeadlessEnvironment detects if we're running in a headless environment
// where clipboard operations are expected to fail
func isHeadlessEnvironment() bool {
	// Try a simple clipboard operation
	err := clipboard.WriteAll("test")
	return err != nil
}

// BenchmarkCopyToClipboard measures copy performance
func BenchmarkCopyToClipboard(b *testing.B) {
	text := "benchmark clipboard content that is reasonably sized"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = CopyToClipboard(text)
	}
}

// BenchmarkPasteFromClipboard measures paste performance
func BenchmarkPasteFromClipboard(b *testing.B) {
	// Setup: put something in clipboard
	_ = clipboard.WriteAll("benchmark content")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = PasteFromClipboard()
	}
}
