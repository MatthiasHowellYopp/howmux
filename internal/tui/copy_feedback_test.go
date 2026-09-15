package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/log"
)

// TestHandleCopy_NothingToCopy verifies that copying a tab with no content
// reports "Nothing to copy" rather than silently doing nothing. The main tab
// has no plain-text buffer, so it is the empty case.
func TestHandleCopy_NothingToCopy(t *testing.T) {
	m := createTestModelWithTab(t, TabTypeMain)

	updated, _ := m.handleCopy()
	got := updated.(model)

	if got.copyStatus != "Nothing to copy" {
		t.Errorf("copyStatus = %q, want %q", got.copyStatus, "Nothing to copy")
	}
	if got.footerManager.transientMessage != "Nothing to copy" {
		t.Errorf("footer transient = %q, want %q", got.footerManager.transientMessage, "Nothing to copy")
	}
}

// TestSetCopyStatus_ClearBySeq verifies the transient copy message is cleared by
// its matching timer, and that a stale timer does not clear a newer message.
func TestSetCopyStatus_ClearBySeq(t *testing.T) {
	m := createTestModelWithTab(t, TabTypeMain)

	// First status.
	updated, _ := m.setCopyStatus("Copied 3 lines to clipboard")
	m1 := updated.(model)
	firstSeq := m1.copyStatusSeq
	if m1.footerManager.transientMessage == "" {
		t.Fatal("expected footer transient message to be set")
	}

	// A newer status replaces it and bumps the sequence.
	updated2, _ := m1.setCopyStatus("Copied 5 lines to clipboard")
	m2 := updated2.(model)
	if m2.copyStatusSeq == firstSeq {
		t.Fatal("expected copyStatusSeq to advance for a newer message")
	}

	// The stale timer (firstSeq) must NOT clear the newer message.
	stale, _ := m2.Update(clearCopyStatusMsg{seq: firstSeq})
	ms := stale.(model)
	if ms.copyStatus != "Copied 5 lines to clipboard" {
		t.Errorf("stale clear wiped a newer message: got %q", ms.copyStatus)
	}

	// The current timer clears it.
	cleared, _ := ms.Update(clearCopyStatusMsg{seq: ms.copyStatusSeq})
	mc := cleared.(model)
	if mc.copyStatus != "" {
		t.Errorf("expected copyStatus cleared, got %q", mc.copyStatus)
	}
	if mc.footerManager.transientMessage != "" {
		t.Errorf("expected footer transient cleared, got %q", mc.footerManager.transientMessage)
	}
}

// TestPlanningTabCopyableContent verifies the planning tab exposes its
// conversation as plain text for copy.
func TestPlanningTabCopyableContent(t *testing.T) {
	m := createTestModelWithTab(t, TabTypePlanning)
	active := m.tabManager.GetActiveTab()
	pt, ok := active.(*PlanningTab)
	if !ok {
		t.Fatalf("active tab is not *PlanningTab: %T", active)
	}

	pt.AddMessage("user", "hello there")
	pt.AddMessage("assistant", "hi, how can I help?")

	content := pt.CopyableContent()
	if !strings.Contains(content, "hello there") || !strings.Contains(content, "hi, how can I help?") {
		t.Errorf("CopyableContent missing conversation text: %q", content)
	}
}

// TestLogTabCopyableContent verifies the log tab renders buffered entries as
// plain, unstyled text (no ANSI escape codes).
func TestLogTabCopyableContent(t *testing.T) {
	lt := NewLogTab("test-log", "info", 100, NewStyles(createMinimalTheme()))
	rb := lt.GetRingBuffer()
	rb.Add(log.InfoLevel, "watcher started", "repo", "owner/name")
	rb.Add(log.ErrorLevel, "boom")

	content := lt.CopyableContent()
	if !strings.Contains(content, "watcher started") {
		t.Errorf("missing message text: %q", content)
	}
	if !strings.Contains(content, "INFO") || !strings.Contains(content, "ERROR") {
		t.Errorf("missing level labels: %q", content)
	}
	if strings.Contains(content, "\x1b[") {
		t.Errorf("copy content contains ANSI escape codes: %q", content)
	}
}
