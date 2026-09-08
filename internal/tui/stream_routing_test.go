package tui

import (
	"testing"

	"github.com/jbrinkman/kiro-krew/internal/acp"
)

// TestStreamMessageRoutesToOriginatingTab is a regression test for the bug where
// planning stream messages were dispatched to the active tab instead of the tab
// that started the stream. Switching tabs mid-stream must not misroute chunks.
func TestStreamMessageRoutesToOriginatingTab(t *testing.T) {
	m := createTestModelWithTab(t, TabTypePlanning)

	// Tab A is the one created by the harness ("test-plan-1"); grab it by type.
	tabA := activePlanningTab(t, m)
	tabAID := tabA.ID()

	// Add tab B and make it the ACTIVE tab.
	ct := NewContextTracker()
	tabB := NewPlanningTabWithSession("test-plan-B", "Plan B", m.styles, ct, nil, nil)
	m.tabManager.AddTab(tabB)
	tabs := m.tabManager.GetTabs()
	for i, tab := range tabs {
		if tab.ID() == tabB.ID() {
			m.tabManager.SetActiveTab(i)
			break
		}
	}
	if m.tabManager.GetActiveTab().ID() != tabB.ID() {
		t.Fatal("tab B should be active")
	}

	// Tab A is mid-stream.
	tabA.streamingResponse = true

	// A text chunk addressed to tab A arrives while B is active.
	msg := planningStreamMsg{
		tabID:    tabAID,
		response: &acp.StreamingResponse{Type: "text", Content: "hello-A"},
	}
	updated, _ := m.Update(msg)
	m = updated.(model)

	// The chunk must have landed on A, not the active tab B.
	gotA := m.tabManager.GetPlanningTabByID(tabAID).currentResponse.String()
	gotB := m.tabManager.GetPlanningTabByID(tabB.ID()).currentResponse.String()

	if gotA != "hello-A" {
		t.Errorf("tab A currentResponse = %q, want %q", gotA, "hello-A")
	}
	if gotB != "" {
		t.Errorf("tab B currentResponse = %q, want empty (chunk misrouted to active tab)", gotB)
	}
}
