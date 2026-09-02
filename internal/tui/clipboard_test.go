package tui

import (
	"testing"
)

// TestInsertAtCursor is the core regression test for the paste path. The bug in
// the original implementation byte-sliced the string with a rune index, which
// corrupted multi-byte characters. These cases lock in rune-safe behavior and
// exercise the boundary/clamping cases. No OS clipboard is touched.
func TestInsertAtCursor(t *testing.T) {
	tests := []struct {
		name       string
		current    string
		insert     string
		cursor     int // rune index
		wantValue  string
		wantCursor int // rune index
	}{
		{"empty into empty", "", "hello", 0, "hello", 5},
		{"append at end (ascii)", "abc", "XY", 3, "abcXY", 5},
		{"insert in middle (ascii)", "abc", "X", 1, "aXbc", 2},
		{"insert at start", "abc", "X", 0, "Xabc", 1},
		// The bug: cursor after a multi-byte char. Byte-slicing "é"[:1] splits
		// the 2-byte rune; rune-slicing must keep it intact.
		{"insert after multibyte rune", "é", "x", 1, "éx", 2},
		{"insert before multibyte rune", "é", "x", 0, "xé", 1},
		{"multibyte insert into multibyte", "aé", "世界", 1, "a世界é", 3},
		{"emoji insert", "ab", "🚀", 1, "a🚀b", 2},
		// Clamping: out-of-range cursor indices must not panic.
		{"cursor past end is clamped", "ab", "X", 99, "abX", 3},
		{"negative cursor is clamped", "ab", "X", -5, "Xab", 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotValue, gotCursor := insertAtCursor(tt.current, tt.insert, tt.cursor)
			if gotValue != tt.wantValue {
				t.Errorf("value = %q, want %q", gotValue, tt.wantValue)
			}
			if gotCursor != tt.wantCursor {
				t.Errorf("cursor = %d, want %d", gotCursor, tt.wantCursor)
			}
		})
	}
}

// TestPlanningTabPlainTextContent verifies copy pulls the underlying, unstyled
// conversation text (all of it) rather than the rendered viewport frame.
func TestPlanningTabPlainTextContent(t *testing.T) {
	styles := NewStyles(createMinimalTheme())
	pt := NewPlanningTabWithSession("copy-test", "Copy Test", styles, NewContextTracker(), nil, nil)

	pt.AddMessage("user", "hello")
	pt.AddMessage("assistant", "hi there")

	got := pt.plainTextContent()
	want := "[planner] hello\nhi there"
	if got != want {
		t.Errorf("plainTextContent() = %q, want %q", got, want)
	}

	// In-progress streaming response is included.
	pt.streamingResponse = true
	pt.currentResponse.WriteString("streaming...")
	got = pt.plainTextContent()
	want = "[planner] hello\nhi there\nstreaming..."
	if got != want {
		t.Errorf("plainTextContent() with stream = %q, want %q", got, want)
	}
}

// TestClipboardGracefulDegradation verifies the wrappers never panic and treat
// empty copy as a no-op, regardless of whether a system clipboard is available.
func TestClipboardGracefulDegradation(t *testing.T) {
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

	t.Run("copy empty is a no-op", func(t *testing.T) {
		if err := CopyToClipboard(""); err != nil {
			t.Errorf("empty string should return nil, got: %v", err)
		}
	})
}
