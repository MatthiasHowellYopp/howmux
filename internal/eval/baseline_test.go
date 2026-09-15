package eval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// caseScores must use the runner's pooled, skip-aware formula
// (sum(score)/sum(max) over non-skipped criteria) so baseline and run values
// are computed identically. A drift here produces phantom regressions.
func TestCaseScores_SkipAware(t *testing.T) {
	result := AgentResult{
		Agent: "architect",
		Cases: []CaseResult{
			{
				CaseName: "c1",
				Scores: []CriterionScore{
					{Name: "a", Score: 4, MaxScore: 5},
					{Name: "b", Score: 4, MaxScore: 5},
					{Name: "skipme", Score: 0, MaxScore: 5, Skipped: true},
				},
			},
		},
	}
	got := caseScores(result)
	// pooled non-skipped: (4+4)/(5+5) = 0.8; the skipped 0/5 must NOT drag it to 8/15.
	if v := got["c1"]; v < 0.7999 || v > 0.8001 {
		t.Errorf("caseScores skip-aware = %v, want 0.8 (skipped criterion excluded)", v)
	}
}

func TestLoadBaseline_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	b := Baseline{
		GitHash: "abc123",
		Agents: map[string]BaselineAgent{
			"architect": {Average: 0.757, Cases: map[string]BaselineCase{
				"basic": {Score: 1.0, Raw: "20/20"},
			}},
		},
	}
	p := filepath.Join(dir, "baseline.json")
	data, _ := json.MarshalIndent(b, "", "  ")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := loadBaseline(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Agents["architect"].Average != 0.757 {
		t.Errorf("average round-trip = %v, want 0.757", got.Agents["architect"].Average)
	}
	if got.Agents["architect"].Cases["basic"].Score != 1.0 {
		t.Errorf("case round-trip = %v, want 1.0", got.Agents["architect"].Cases["basic"].Score)
	}
}

func TestLoadBaseline_Missing(t *testing.T) {
	if _, err := loadBaseline(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Error("expected error for missing baseline file")
	}
}
