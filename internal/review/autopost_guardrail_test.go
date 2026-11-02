package review

import (
	"errors"
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
		language    string
		prior       *LoadedPriorFeedback
		wantClean   bool
		wantFail    string // substring of firstFailure() when !wantClean
	}{
		{
			name:        "generic review is forced to a human",
			verdict:     "COMMENT",
			body:        cleanBody,
			headSHA:     head,
			reviewedSHA: head,
			commentable: commentableFixture(),
			maxFindings: 25,
			language:    "generic",
			wantClean:   false,
			wantFail:    "not_generic",
		},
		{
			name:        "specialist review passes not_generic",
			verdict:     "COMMENT",
			body:        cleanBody,
			headSHA:     head,
			reviewedSHA: head,
			commentable: commentableFixture(),
			maxFindings: 25,
			language:    "go",
			wantClean:   true,
		},
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
			name:        "unknown verdict matching body fails",
			verdict:     "LGTM",
			body:        strings.Replace(cleanBody, "VERDICT: COMMENT", "VERDICT: LGTM", 1),
			headSHA:     head,
			reviewedSHA: head,
			commentable: commentableFixture(),
			maxFindings: 25,
			wantClean:   false,
			wantFail:    "event_legal",
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
		{
			name:    "dropped finding under Reviewer notes does not need to anchor",
			verdict: "COMMENT",
			body: "# Review\n\n## Summary\nTidies foo.\n\n## Findings\n" +
				"- internal/foo.go:10 - low - unused var -> remove it\n\n" +
				"## Reviewer notes\nDropped: internal/other.go:99 - nit - naming -> rename\n\n" +
				"VERDICT: COMMENT\n",
			headSHA:     head,
			reviewedSHA: head,
			commentable: commentableFixture(),
			maxFindings: 25,
			wantClean:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := evaluateAutoPostGuardrail(tt.verdict, tt.body, tt.headSHA, tt.reviewedSHA, tt.commentable, tt.maxFindings, tt.language, tt.prior)
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

// priorWithThreads builds a loaded prior-feedback fixture holding threads.
func priorWithThreads(threads ...PriorThread) *LoadedPriorFeedback {
	return &LoadedPriorFeedback{Path: "/prior.json", Raw: []byte("{}"), Data: &PriorFeedback{Threads: threads}}
}

func intPtr(n int) *int { return &n }

func TestCheckNotAlreadyRaised(t *testing.T) {
	const plain = "## Findings\n- internal/foo.go:10 - medium - leaks fd -> close it\n\nVERDICT: COMMENT\n"
	const reRaised = "## Findings\n- internal/foo.go:10 - medium - leaks fd -> close it (re-raised: thread by alice)\n\nVERDICT: COMMENT\n"
	thread := func(resolved, outdated bool) PriorThread {
		return PriorThread{Path: "internal/foo.go", Line: intPtr(10), IsResolved: resolved, IsOutdated: outdated,
			Comments: []PriorThreadComment{{Author: "alice", Body: "this leaks"}}}
	}

	tests := []struct {
		name       string
		body       string
		prior      *LoadedPriorFeedback
		wantPass   bool
		wantDetail string
	}{
		{"absent key passes", plain, nil, true, ""},
		{"unreadable file fails closed", plain,
			&LoadedPriorFeedback{Path: "/missing.json", Err: errors.New("read prior feedback /missing.json: no such file")},
			false, "/missing.json"},
		{"unresolved match fails", plain, priorWithThreads(thread(false, false)), false, "internal/foo.go:10"},
		{"outdated thread ignored", plain, priorWithThreads(thread(false, true)), true, ""},
		{"resolved + re-raised passes", reRaised, priorWithThreads(thread(true, false)), true, ""},
		{"unresolved + re-raised fails", reRaised, priorWithThreads(thread(false, false)), false, "internal/foo.go:10"},
		{"different line passes", plain,
			priorWithThreads(PriorThread{Path: "internal/foo.go", Line: intPtr(11)}), true, ""},
		{"null line ignored", plain,
			priorWithThreads(PriorThread{Path: "internal/foo.go", Line: nil}), true, ""},
		{"threads fetch error fails closed", plain,
			&LoadedPriorFeedback{Path: "/p.json", Data: &PriorFeedback{Errors: []string{"threads: gh api graphql: exit 1"}}},
			false, "could not be fetched"},
		{"non-thread fetch error passes", plain,
			&LoadedPriorFeedback{Path: "/p.json", Data: &PriorFeedback{Errors: []string{"issue search 'foo.go': exit 1"}}},
			true, ""},
		{"blank key fails closed", plain,
			&LoadedPriorFeedback{Err: errors.New("prior_feedback is blank: the prior-feedback snapshot was not written")},
			false, "blank"},
		{"single-line finding inside a multi-line thread span fails", plain,
			priorWithThreads(PriorThread{Path: "internal/foo.go", StartLine: intPtr(8), Line: intPtr(12)}), false, "internal/foo.go:10"},
		{"range finding overlapping a single-line thread fails",
			"## Findings\n- internal/foo.go:8-10 - medium - leaks fd -> close it\n\nVERDICT: COMMENT\n",
			priorWithThreads(PriorThread{Path: "internal/foo.go", Line: intPtr(9)}), false, "internal/foo.go:8"},
		{"range finding next to a thread span passes",
			"## Findings\n- internal/foo.go:8-10 - medium - leaks fd -> close it\n\nVERDICT: COMMENT\n",
			priorWithThreads(PriorThread{Path: "internal/foo.go", StartLine: intPtr(11), Line: intPtr(14)}), true, ""},
		{"thread match in reviewer notes is not a finding", "## Summary\nok\n\n## Reviewer notes\n" +
			"Already raised (dropped): internal/foo.go:10 - medium - leaks fd -> close it\n\nVERDICT: COMMENT\n",
			priorWithThreads(thread(false, false)), true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := checkNotAlreadyRaised(tt.body, tt.prior)
			if got.name != "not_already_raised" {
				t.Errorf("name = %q", got.name)
			}
			if got.passed != tt.wantPass {
				t.Fatalf("passed = %v, want %v (detail %q)", got.passed, tt.wantPass, got.detail)
			}
			if !strings.Contains(got.detail, tt.wantDetail) {
				t.Errorf("detail = %q, want it to contain %q", got.detail, tt.wantDetail)
			}
		})
	}
}

// TestEvaluateAutoPostGuardrail_NotAlreadyRaised checks the new check is
// wired into the aggregate result.
func TestEvaluateAutoPostGuardrail_NotAlreadyRaised(t *testing.T) {
	prior := priorWithThreads(PriorThread{Path: "internal/foo.go", Line: intPtr(10)})
	got := evaluateAutoPostGuardrail("COMMENT", cleanBody, "abc", "abc", commentableFixture(), 25, "go", prior)
	if got.clean {
		t.Fatal("clean = true, want the not_already_raised check to fail")
	}
	if ff := got.firstFailure(); !strings.Contains(ff, "not_already_raised") || !strings.Contains(ff, "internal/foo.go:10") {
		t.Errorf("firstFailure() = %q", ff)
	}
	if got := evaluateAutoPostGuardrail("COMMENT", cleanBody, "abc", "abc", commentableFixture(), 25, "go", nil); !got.clean {
		t.Errorf("nil prior should be clean, got %q", got.firstFailure())
	}
}
