package review

import (
	"strconv"
	"strings"
	"testing"
)

// cleanBody is a well-formed review: a VERDICT line and one finding that
// anchors to the diff fixture below.
const cleanBody = `# Review

Looks fine overall.

- internal/foo.go:10 - low - unused var -> remove it

VERDICT: COMMENT
`

// commentableFixture: internal/foo.go has commentable right-side lines 9-11.
func commentableFixture() map[string]map[int]bool {
	return map[string]map[int]bool{
		"internal/foo.go": {9: true, 10: true, 11: true},
	}
}

func TestEvaluateAutoPostGuardrail(t *testing.T) {
	const head = "abc123"

	tests := []struct {
		name        string
		verdict     string
		body        string
		headSHA     string
		reviewedSHA string
		commentable map[string]map[int]bool
		maxFindings int
		wantClean   bool
		wantFail    string // substring of firstFailure() when !wantClean
	}{
		{
			name:        "clean comment review posts",
			verdict:     "COMMENT",
			body:        cleanBody,
			headSHA:     head,
			reviewedSHA: head,
			commentable: commentableFixture(),
			maxFindings: 25,
			wantClean:   true,
		},
		{
			name:        "clean approve with zero findings",
			verdict:     "APPROVE",
			body:        "# Review\n\nLGTM.\n\nVERDICT: APPROVE\n",
			headSHA:     head,
			reviewedSHA: head,
			commentable: commentableFixture(),
			maxFindings: 25,
			wantClean:   true,
		},
		{
			name:        "unparseable verdict fails",
			verdict:     "COMMENT",
			body:        "# Review\n\nno verdict line here\n",
			headSHA:     head,
			reviewedSHA: head,
			commentable: commentableFixture(),
			maxFindings: 25,
			wantClean:   false,
			wantFail:    "verdict_parseable",
		},
		{
			name:        "finding off the diff fails anchor",
			verdict:     "COMMENT",
			body:        "# Review\n\n- internal/foo.go:999 - low - x -> y\n\nVERDICT: COMMENT\n",
			headSHA:     head,
			reviewedSHA: head,
			commentable: commentableFixture(),
			maxFindings: 25,
			wantClean:   false,
			wantFail:    "findings_anchor",
		},
		{
			name:        "nil commentable fails closed on anchor",
			verdict:     "COMMENT",
			body:        cleanBody,
			headSHA:     head,
			reviewedSHA: head,
			commentable: nil,
			maxFindings: 25,
			wantClean:   false,
			wantFail:    "findings_anchor",
		},
		{
			name:        "stale sha fails",
			verdict:     "COMMENT",
			body:        cleanBody,
			headSHA:     head,
			reviewedSHA: "oldsha",
			commentable: commentableFixture(),
			maxFindings: 25,
			wantClean:   false,
			wantFail:    "sha_fresh",
		},
		{
			name:        "empty reviewed sha fails",
			verdict:     "COMMENT",
			body:        cleanBody,
			headSHA:     head,
			reviewedSHA: "",
			commentable: commentableFixture(),
			maxFindings: 25,
			wantClean:   false,
			wantFail:    "sha_fresh",
		},
		{
			name:        "approve verdict is auto-postable",
			verdict:     "APPROVE",
			body:        strings.Replace(cleanBody, "VERDICT: COMMENT", "VERDICT: APPROVE", 1),
			headSHA:     head,
			reviewedSHA: head,
			commentable: commentableFixture(),
			maxFindings: 25,
			wantClean:   true,
		},
		{
			name:        "request_changes verdict is auto-postable",
			verdict:     "REQUEST_CHANGES",
			body:        strings.Replace(cleanBody, "VERDICT: COMMENT", "VERDICT: REQUEST_CHANGES", 1),
			headSHA:     head,
			reviewedSHA: head,
			commentable: commentableFixture(),
			maxFindings: 25,
			wantClean:   true,
		},
		{
			name:        "record verdict disagreeing with body fails",
			verdict:     "APPROVE",
			body:        cleanBody,
			headSHA:     head,
			reviewedSHA: head,
			commentable: commentableFixture(),
			maxFindings: 25,
			wantClean:   false,
			wantFail:    "event_legal",
		},
		{
			name:        "machine path in body fails secrets",
			verdict:     "COMMENT",
			body:        "# Review\n\n- internal/foo.go:10 - low - see /Users/alice/secret -> fix\n\nVERDICT: COMMENT\n",
			headSHA:     head,
			reviewedSHA: head,
			commentable: commentableFixture(),
			maxFindings: 25,
			wantClean:   false,
			wantFail:    "no_secrets",
		},
		{
			name:        "github token in body fails secrets",
			verdict:     "COMMENT",
			body:        "# Review\n\ntoken ghp_abcdefghijklmnopqrstuvwxyz0123\n\nVERDICT: COMMENT\n",
			headSHA:     head,
			reviewedSHA: head,
			commentable: commentableFixture(),
			maxFindings: 25,
			wantClean:   false,
			wantFail:    "no_secrets",
		},
		{
			name:        "finding count over cap fails",
			verdict:     "COMMENT",
			body:        manyFindings(5),
			headSHA:     head,
			reviewedSHA: head,
			commentable: map[string]map[int]bool{"internal/foo.go": {1: true, 2: true, 3: true, 4: true, 5: true}},
			maxFindings: 3,
			wantClean:   false,
			wantFail:    "finding_count",
		},
		{
			name:        "finding count at cap passes",
			verdict:     "COMMENT",
			body:        manyFindings(3),
			headSHA:     head,
			reviewedSHA: head,
			commentable: map[string]map[int]bool{"internal/foo.go": {1: true, 2: true, 3: true}},
			maxFindings: 3,
			wantClean:   true,
		},
		{
			name:        "zero cap disables the count check",
			verdict:     "COMMENT",
			body:        manyFindings(3),
			headSHA:     head,
			reviewedSHA: head,
			commentable: map[string]map[int]bool{"internal/foo.go": {1: true, 2: true, 3: true}},
			maxFindings: 0,
			wantClean:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := evaluateAutoPostGuardrail(tt.verdict, tt.body, tt.headSHA, tt.reviewedSHA, tt.commentable, tt.maxFindings)
			if got.clean != tt.wantClean {
				t.Fatalf("clean = %v, want %v (checks: %+v)", got.clean, tt.wantClean, got.checks)
			}
			if !tt.wantClean {
				ff := got.firstFailure()
				if !strings.Contains(ff, tt.wantFail) {
					t.Errorf("firstFailure() = %q, want it to contain %q", ff, tt.wantFail)
				}
			}
		})
	}
}

// manyFindings builds a review body with n findings on internal/foo.go lines
// 1..n, all anchorable, plus a COMMENT verdict.
func manyFindings(n int) string {
	var b strings.Builder
	b.WriteString("# Review\n\n")
	for i := 1; i <= n; i++ {
		b.WriteString("- internal/foo.go:")
		b.WriteString(strconv.Itoa(i))
		b.WriteString(" - low - issue -> fix\n")
	}
	b.WriteString("\nVERDICT: COMMENT\n")
	return b.String()
}
