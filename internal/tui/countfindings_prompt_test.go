package tui

import "testing"

// TestCountFindingsForPrompt covers the display-only finding counter behind
// the post confirm prompt. It must stay in lockstep with internal/review's
// countFindings so the "Post N findings (…)" text the user sees before
// posting matches what PostReview actually anchors. The cases mirror the real
// consolidator output shapes (Bit-Quill/flowdra #78/#81/#84/#85/#86/#88) that
// the original strict regex counted as zero.
func TestCountFindingsForPrompt(t *testing.T) {
	tests := []struct {
		name          string
		body          string
		wantTotal     int
		wantBreakdown string
	}{
		{
			name:          "no findings",
			body:          "# Review\n\nAll good.\n\nVERDICT: APPROVE\n",
			wantTotal:     0,
			wantBreakdown: "",
		},
		{
			name:          "bare canonical lines",
			body:          "src/foo.py:10 - warning - unchecked error -> handle it\nsrc/bar.py:22 - nit - naming -> rename\n",
			wantTotal:     2,
			wantBreakdown: "1 warning, 1 nit",
		},
		{
			name: "decorated + range + bold + compound + no-arrow shapes all counted",
			body: "# Consolidated Review\n\n" +
				"- `internal/agent/runid_unix_test.go:1606` - Low - wraps a constant -> Drop the wrapper.\n" +
				"- `internal/agent/golden_e2e_test.go:621-622` - low/medium (test correctness) - the assertion -> Assert the branch.\n" +
				"- `internal/supervise/supervise.go:11` - **Medium** - recovery logs only %v -> Capture the stack.\n" +
				"  - `internal/inference/stub_route_test.go:70` — single-digit idiom; fragile. Suggest fmt.Sprintf.\n\n" +
				"VERDICT: COMMENT\n",
			// Four findings parse; none of their normalized severities are
			// in the counted set (critical/warning/nit), so breakdown is "".
			wantTotal:     4,
			wantBreakdown: "",
		},
		{
			name:          "counted severities survive bold/qualifier normalization",
			body:          "a.go:1 - **Critical** - x -> y\nb.go:2 - warning (blocking) - z -> w\n",
			wantTotal:     2,
			wantBreakdown: "1 critical, 1 warning",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			total, breakdown := countFindingsForPrompt(tt.body)
			if total != tt.wantTotal {
				t.Errorf("total = %d, want %d", total, tt.wantTotal)
			}
			if breakdown != tt.wantBreakdown {
				t.Errorf("breakdown = %q, want %q", breakdown, tt.wantBreakdown)
			}
		})
	}
}
