package review

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- Task 8: hermetic test harness -----------------------------------------

// fakeKiroCall records one invocation of the faked kiroOneshotFunc:
// the agent name it was called with and the prompt text passed.
type fakeKiroCall struct {
	agentName string
	prompt    string
}

// withFakeKiroOneshot substitutes kiroOneshotFunc with a fake that records
// every call into the returned slice and returns a scripted response
// (configurable per agent name via responses, falling back to
// defaultResponse for any agent not present in the map). If errors[agentName]
// is set, that call returns the given error instead of a response. Restores
// the real kiroOneshotFunc on test cleanup.
//
// Mirrors decisionwriter_test.go's withFakeExecAndScriptPath convention:
// a fake-substitution helper that returns the recording slice(s) the test
// then asserts against, with t.Cleanup restoring the real seam.
func withFakeKiroOneshot(t *testing.T, responses map[string]string, errors map[string]error) *[]fakeKiroCall {
	t.Helper()
	orig := kiroOneshotFunc
	var mu sync.Mutex
	calls := []fakeKiroCall{}

	kiroOneshotFunc = func(ctx context.Context, agentName string, prompt string) (string, error) {
		mu.Lock()
		calls = append(calls, fakeKiroCall{agentName: agentName, prompt: prompt})
		mu.Unlock()

		if errors != nil {
			if err, ok := errors[agentName]; ok {
				return "", err
			}
		}
		if responses != nil {
			if resp, ok := responses[agentName]; ok {
				return resp, nil
			}
		}
		return fmt.Sprintf("default response for %s", agentName), nil
	}

	t.Cleanup(func() {
		kiroOneshotFunc = orig
	})

	return &calls
}

// fakeGhCall records one invocation of the faked postReviewCommandFunc.
type fakeGhCall struct {
	payloadFile string
	repo        string
	pr          int
}

// withFakeGhPost substitutes postReviewCommandFunc with a fake that records
// every call into the returned slice and either returns fixedOutput (nil
// error) or, if err is non-nil, returns that error instead. Restores the
// real postReviewCommandFunc on test cleanup.
func withFakeGhPost(t *testing.T, fixedOutput []byte, err error) *[]fakeGhCall {
	t.Helper()
	orig := postReviewCommandFunc
	var mu sync.Mutex
	calls := []fakeGhCall{}

	postReviewCommandFunc = func(ctx context.Context, payloadFile string, repo string, pr int) ([]byte, error) {
		mu.Lock()
		calls = append(calls, fakeGhCall{payloadFile: payloadFile, repo: repo, pr: pr})
		mu.Unlock()

		if err != nil {
			return nil, err
		}
		return fixedOutput, nil
	}

	t.Cleanup(func() {
		postReviewCommandFunc = orig
	})

	return &calls
}

// writeFakeSpoolFile writes a spool fixture file under dir/pending/ (or
// dir/done/ if inDone is true) with the given front-matter fields and
// body, and returns its absolute path. frontMatter keys are written in a
// stable (sorted) order for deterministic test fixtures; callers that need
// a specific key ORDER should construct the raw fixture string directly
// instead.
func writeFakeSpoolFile(t *testing.T, dir string, frontMatter map[string]string, body string) string {
	t.Helper()

	sub := "pending"
	pendingDir := filepath.Join(dir, sub)
	if err := os.MkdirAll(pendingDir, 0o755); err != nil {
		t.Fatalf("writeFakeSpoolFile: mkdir: %v", err)
	}

	// Stable key order: canonical spool keys first (matching spool.py's
	// _FM_KEYS order), then any extra keys sorted.
	canonical := []string{"repo", "pr", "verdict", "decision", "decision_notes", "diff_file", "generated"}
	seen := map[string]bool{}

	var sb strings.Builder
	sb.WriteString("---\n")
	for _, k := range canonical {
		if v, ok := frontMatter[k]; ok {
			sb.WriteString(k + ": " + v + "\n")
			seen[k] = true
		}
	}
	for k, v := range frontMatter {
		if !seen[k] {
			sb.WriteString(k + ": " + v + "\n")
		}
	}
	sb.WriteString("---\n\n")
	sb.WriteString(body)

	repo := strings.ReplaceAll(frontMatter["repo"], "/", "-")
	if repo == "" {
		repo = "owner-repo"
	}
	pr := frontMatter["pr"]
	if pr == "" {
		pr = "1"
	}
	filename := fmt.Sprintf("pr-review-%s-%s.md", repo, pr)
	path := filepath.Join(pendingDir, filename)
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		t.Fatalf("writeFakeSpoolFile: write: %v", err)
	}
	return path
}

// recordForSpool builds a minimal Record pointing at spoolPath, for tests
// that only need PostReview/DiscardReview/etc.'s Repo/PR/SpoolPath fields.
func recordForSpool(repo string, pr int, spoolPath string) Record {
	return Record{
		Repo:      repo,
		PR:        pr,
		URL:       fmt.Sprintf("https://github.com/%s/pull/%d", repo, pr),
		SpoolPath: spoolPath,
	}
}

// withFakeDecisionScript substitutes DecisionWriter's own subprocess seams
// (execCommandFunc, scriptPathFunc, statFunc — defined in
// decisionwriter.go) with a fake that actually performs the "decision:"
// front-matter write DecisionWriter.SetDecision would otherwise delegate to
// set-review-decision.sh, since DiscardReview/PostReview both call through
// DecisionWriter.SetDecision as their first step. Mirrors
// decisionwriter_test.go's withFakeExecAndScriptPath convention, but the
// fake script here actually mutates the spool file's "decision:" field
// (via the same RewriteSpoolEntry-adjacent front-matter patch every other
// helper in this package uses) rather than just controlling an exit code,
// so DiscardReview/PostReview's subsequent archiveToDone call sees a
// correctly-decided file on disk, exactly like the real script would leave
// behind. Restores all three seams on test cleanup.
func withFakeDecisionScript(t *testing.T) {
	t.Helper()
	origExec := execCommandFunc
	origScriptPath := scriptPathFunc
	origStat := statFunc

	execCommandFunc = func(name string, arg ...string) *exec.Cmd {
		// arg is [scriptPath, spoolPath, decision] per DecisionWriter.SetDecision.
		spoolPath := arg[1]
		decision := arg[2]
		if err := setDecisionFieldForTest(spoolPath, decision); err != nil {
			// Surface as a nonzero-exit fake command so SetDecision reports
			// a wrapped failure, matching the real script's contract.
			return exec.Command("/bin/sh", "-c", "echo "+shellQuoteForTest(err.Error())+" 1>&2; exit 1")
		}
		return exec.Command("/bin/sh", "-c", "exit 0")
	}
	scriptPathFunc = func() string { return "/fake/set-review-decision.sh" }
	statFunc = func(string) (os.FileInfo, error) { return nil, nil }

	t.Cleanup(func() {
		execCommandFunc = origExec
		scriptPathFunc = origScriptPath
		statFunc = origStat
	})
}

// setDecisionFieldForTest performs the actual "decision:" front-matter
// write the real set-review-decision.sh would perform, so
// withFakeDecisionScript's fake exec seam has a real effect on disk for
// DiscardReview/PostReview's subsequent steps to observe.
func setDecisionFieldForTest(spoolPath, decision string) error {
	data, err := os.ReadFile(spoolPath)
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "decision:") {
			lines[i] = "decision: " + decision
		}
	}
	return os.WriteFile(spoolPath, []byte(strings.Join(lines, "\n")), 0o644)
}

// shellQuoteForTest wraps s in single quotes for safe interpolation into a
// generated /bin/sh -c script within this test file only.
func shellQuoteForTest(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// --- Task 2: subprocess seam tests -----------------------------------------

func TestDefaultKiroOneshot_StripsAnsiAndPrompt(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "leading angle-bracket prompt echo stripped",
			in:   "\x1b[32m> \x1b[0mHello, world.",
			want: "Hello, world.",
		},
		{
			name: "only the first leading angle bracket is stripped",
			in:   "> Quoting > something inside the body",
			want: "Quoting > something inside the body",
		},
		{
			name: "carriage returns removed",
			in:   "line one\r\nline two\r\n",
			want: "line one\nline two",
		},
		{
			name: "no ansi or prompt prefix is a no-op besides trimming",
			in:   "  plain text answer  ",
			want: "plain text answer",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cleanKiroStdout(tt.in)
			if got != tt.want {
				t.Errorf("cleanKiroStdout(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestDefaultKiroOneshot_EmptyOutput_ReturnsError(t *testing.T) {
	orig := lookPathForDecideActionsFunc
	defer func() { lookPathForDecideActionsFunc = orig }()

	// Use a fake shell script substituted in place of "kiro-cli" via a
	// wrapping exec — since defaultKiroOneshot hardcodes the binary name
	// "kiro-cli", we test it indirectly through kiroOneshotFunc's contract
	// using a temporary PATH entry providing a fake kiro-cli that prints
	// nothing and exits 0.
	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "kiro-cli")
	script := "#!/bin/sh\nexit 0\n"
	if err := os.WriteFile(fakeBin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake kiro-cli: %v", err)
	}
	restorePath := prependPath(t, dir)
	defer restorePath()

	_, err := defaultKiroOneshot(context.Background(), "review-consolidator", "prompt")
	if err == nil {
		t.Fatal("defaultKiroOneshot() error = nil, want non-nil error for empty output")
	}
	if !strings.Contains(err.Error(), "no answer") {
		t.Errorf("error = %v, want it to mention 'no answer'", err)
	}
}

func TestDefaultKiroOneshot_NonZeroExit_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "kiro-cli")
	script := "#!/bin/sh\necho 'boom' 1>&2\nexit 1\n"
	if err := os.WriteFile(fakeBin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake kiro-cli: %v", err)
	}
	restorePath := prependPath(t, dir)
	defer restorePath()

	_, err := defaultKiroOneshot(context.Background(), "review-consolidator", "prompt")
	if err == nil {
		t.Fatal("defaultKiroOneshot() error = nil, want non-nil error for non-zero exit")
	}
	if !strings.Contains(err.Error(), "exited") {
		t.Errorf("error = %v, want it to mention the exit", err)
	}
}

func TestDefaultKiroOneshot_Success_ReturnsCleanedAnswer(t *testing.T) {
	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "kiro-cli")
	script := "#!/bin/sh\nprintf '> hello there\\n'\nexit 0\n"
	if err := os.WriteFile(fakeBin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake kiro-cli: %v", err)
	}
	restorePath := prependPath(t, dir)
	defer restorePath()

	got, err := defaultKiroOneshot(context.Background(), "review-consolidator", "prompt")
	if err != nil {
		t.Fatalf("defaultKiroOneshot() unexpected error: %v", err)
	}
	if got != "hello there" {
		t.Errorf("defaultKiroOneshot() = %q, want %q", got, "hello there")
	}
}

func TestPostReviewCommandFunc_Success(t *testing.T) {
	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "gh")
	script := "#!/bin/sh\necho '{\"id\":1}'\nexit 0\n"
	if err := os.WriteFile(fakeBin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake gh: %v", err)
	}
	restorePath := prependPath(t, dir)
	defer restorePath()

	out, err := defaultPostReviewCommand(context.Background(), "/tmp/payload.json", "owner/repo", 42)
	if err != nil {
		t.Fatalf("defaultPostReviewCommand() unexpected error: %v", err)
	}
	if !strings.Contains(string(out), `"id":1`) {
		t.Errorf("output = %q, want it to contain gh's stdout", out)
	}
}

func TestPostReviewCommandFunc_GhError_WrapsStderr(t *testing.T) {
	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "gh")
	script := "#!/bin/sh\necho 'HTTP 422: Unprocessable Entity' 1>&2\nexit 1\n"
	if err := os.WriteFile(fakeBin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake gh: %v", err)
	}
	restorePath := prependPath(t, dir)
	defer restorePath()

	_, err := defaultPostReviewCommand(context.Background(), "/tmp/payload.json", "owner/repo", 42)
	if err == nil {
		t.Fatal("defaultPostReviewCommand() error = nil, want non-nil error")
	}
	if !strings.Contains(err.Error(), "Unprocessable Entity") {
		t.Errorf("error = %v, want it to contain gh's stderr", err)
	}
}

// prependPath prepends dir to $PATH for the duration of the test, returning
// a restore function. Used by the seam tests above so a fake "kiro-cli"/"gh"
// script is found first, without touching the *Func seams (those tests
// specifically exercise the default* implementations, which hardcode the
// binary name and resolve it via PATH like any other exec.Command call).
func prependPath(t *testing.T, dir string) func() {
	t.Helper()
	origPath := os.Getenv("PATH")
	if err := os.Setenv("PATH", dir+string(os.PathListSeparator)+origPath); err != nil {
		t.Fatalf("prependPath: setenv: %v", err)
	}
	return func() {
		os.Setenv("PATH", origPath)
	}
}

// --- Task 3: countFindings --------------------------------------------------

func TestCountFindings(t *testing.T) {
	tests := []struct {
		name          string
		body          string
		wantTotal     int
		wantBreakdown string
	}{
		{
			name:          "no findings",
			body:          "# Review\n\nEverything looks fine.\n\nVERDICT: APPROVE\n",
			wantTotal:     0,
			wantBreakdown: "",
		},
		{
			name: "mixed severities with hyphen separator",
			body: `# Review

src/foo.py:10 - warning - unchecked error -> handle it
src/bar.py:22 - nit - naming -> rename
src/baz.py:5 - critical - sql injection -> parameterize

VERDICT: REQUEST_CHANGES
`,
			wantTotal: 3,
			// breakdown lists severities in order of first appearance in the
			// body, not a fixed severity ranking.
			wantBreakdown: "1 warning, 1 nit, 1 critical",
		},
		{
			name:          "em-dash separator variant",
			body:          "src/foo.py:10 — warning — unchecked error → handle it\n",
			wantTotal:     1,
			wantBreakdown: "1 warning",
		},
		{
			name:          "malformed lines ignored, not crashed on",
			body:          "this is not a finding line\nfile-without-line - warning - x -> y\nsrc/ok.py:3 - nit - trivial -> fix\n",
			wantTotal:     1,
			wantBreakdown: "1 nit",
		},
		{
			name:          "multiple of the same severity pluralizes",
			body:          "a.py:1 - nit - x -> y\nb.py:2 - nit - x -> y\n",
			wantTotal:     2,
			wantBreakdown: "2 nits",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			total, breakdown := countFindings(tt.body)
			if total != tt.wantTotal {
				t.Errorf("countFindings() total = %d, want %d", total, tt.wantTotal)
			}
			if breakdown != tt.wantBreakdown {
				t.Errorf("countFindings() breakdown = %q, want %q", breakdown, tt.wantBreakdown)
			}
		})
	}
}

// --- Task 3: humanizeReviewBody ---------------------------------------------

func TestHumanizeReviewBody_OptOutEnvVar_ReturnsOriginal(t *testing.T) {
	os.Setenv("HOWMUX_NO_HUMANIZE", "1")
	defer os.Unsetenv("HOWMUX_NO_HUMANIZE")

	calls := withFakeKiroOneshot(t, map[string]string{"humanizer": "should never be used"}, nil)

	got := humanizeReviewBody(context.Background(), "original body text")
	if got != "original body text" {
		t.Errorf("humanizeReviewBody() = %q, want original body unchanged", got)
	}
	if len(*calls) != 0 {
		t.Errorf("expected kiroOneshotFunc to never be called with opt-out set, got %d calls", len(*calls))
	}
}

func TestHumanizeReviewBody_KiroError_ReturnsOriginal(t *testing.T) {
	os.Unsetenv("HOWMUX_NO_HUMANIZE")
	calls := withFakeKiroOneshot(t, nil, map[string]error{"humanizer": fmt.Errorf("kiro-cli exited 1")})

	got := humanizeReviewBody(context.Background(), "original body text")
	if got != "original body text" {
		t.Errorf("humanizeReviewBody() = %q, want original body unchanged on kiro error", got)
	}
	if len(*calls) != 1 {
		t.Errorf("expected exactly one kiroOneshotFunc call, got %d", len(*calls))
	}
}

func TestHumanizeReviewBody_Success_ReturnsHumanized(t *testing.T) {
	os.Unsetenv("HOWMUX_NO_HUMANIZE")
	calls := withFakeKiroOneshot(t, map[string]string{"humanizer": "nicer prose here"}, nil)

	got := humanizeReviewBody(context.Background(), "original body text")
	if got != "nicer prose here" {
		t.Errorf("humanizeReviewBody() = %q, want %q", got, "nicer prose here")
	}
	if len(*calls) != 1 {
		t.Fatalf("expected exactly one kiroOneshotFunc call, got %d", len(*calls))
	}
	if (*calls)[0].agentName != "humanizer" {
		t.Errorf("agentName = %q, want %q", (*calls)[0].agentName, "humanizer")
	}
}

// --- Task 3: extractReviewBody / parseVerdict ------------------------------

func TestExtractReviewBody(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "preamble before H1 is trimmed",
			raw:  "I'm going to read the diff now.\nOne moment please.\n\n# Consolidated Review — owner/repo PR #17\n\nfindings here\nVERDICT: APPROVE",
			want: "# Consolidated Review — owner/repo PR #17\n\nfindings here\nVERDICT: APPROVE",
		},
		{
			name: "no H1 present falls back to full trimmed text",
			raw:  "  just some plain text answer  \n",
			want: "just some plain text answer",
		},
		{
			name: "H1 with leading whitespace is still detected",
			raw:  "preamble\n  # Indented Heading\nbody",
			want: "# Indented Heading\nbody",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractReviewBody(tt.raw)
			if got != tt.want {
				t.Errorf("extractReviewBody(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestParseVerdict(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{
			name: "simple verdict line",
			text: "# Review\n\nfindings\nVERDICT: APPROVE",
			want: "APPROVE",
		},
		{
			name: "last verdict line wins when multiple present",
			text: "VERDICT: COMMENT\nmore text\nVERDICT: REQUEST_CHANGES",
			want: "REQUEST_CHANGES",
		},
		{
			name: "case-insensitive prefix, value upper-cased",
			text: "verdict: approve",
			want: "APPROVE",
		},
		{
			name: "no verdict line returns UNKNOWN",
			text: "# Review\n\nno verdict here",
			want: "UNKNOWN",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseVerdict(tt.text)
			if got != tt.want {
				t.Errorf("parseVerdict(%q) = %q, want %q", tt.text, got, tt.want)
			}
		})
	}
}

// --- Task 3: buildReviewPayload ---------------------------------------------

func TestBuildReviewPayload_MapsVerdictToEvent(t *testing.T) {
	tests := []struct {
		name    string
		verdict string
		want    string
	}{
		{name: "approve maps to APPROVE", verdict: "APPROVE", want: "APPROVE"},
		{name: "request_changes maps to REQUEST_CHANGES", verdict: "REQUEST_CHANGES", want: "REQUEST_CHANGES"},
		{name: "comment maps to COMMENT", verdict: "COMMENT", want: "COMMENT"},
		{name: "empty maps to COMMENT", verdict: "", want: "COMMENT"},
		{name: "unrecognized maps to COMMENT", verdict: "BOGUS", want: "COMMENT"},
		{name: "lowercase approve still maps to APPROVE", verdict: "approve", want: "APPROVE"},
	}

	body := "src/foo.py:10 - warning - issue -> fix\n"
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, event, _, err := buildReviewPayload("owner/repo", 1, tt.verdict, body)
			if err != nil {
				t.Fatalf("buildReviewPayload() unexpected error: %v", err)
			}
			if event != tt.want {
				t.Errorf("event = %q, want %q", event, tt.want)
			}
		})
	}
}

func TestBuildReviewPayload_ParsesFindingsIntoComments(t *testing.T) {
	body := "# Review\n\nsrc/foo.py:10 - warning - unchecked error -> handle it\nsrc/bar.py:22 - nit - naming -> rename\n\nVERDICT: COMMENT\n"

	payloadJSON, event, count, err := buildReviewPayload("owner/repo", 5, "COMMENT", body)
	if err != nil {
		t.Fatalf("buildReviewPayload() unexpected error: %v", err)
	}
	if count != 2 {
		t.Fatalf("commentCount = %d, want 2", count)
	}
	if event != "COMMENT" {
		t.Errorf("event = %q, want %q", event, "COMMENT")
	}

	var decoded struct {
		Event    string `json:"event"`
		Body     string `json:"body"`
		Comments []struct {
			Path string `json:"path"`
			Line int    `json:"line"`
			Body string `json:"body"`
		} `json:"comments"`
	}
	if err := json.Unmarshal(payloadJSON, &decoded); err != nil {
		t.Fatalf("failed to unmarshal payload: %v", err)
	}
	if len(decoded.Comments) != 2 {
		t.Fatalf("decoded comments = %d, want 2", len(decoded.Comments))
	}
	if decoded.Comments[0].Path != "src/foo.py" || decoded.Comments[0].Line != 10 {
		t.Errorf("comment[0] = %+v, want path=src/foo.py line=10", decoded.Comments[0])
	}
	if decoded.Body != body {
		t.Errorf("decoded.Body = %q, want the full original body verbatim", decoded.Body)
	}
}

// --- Task 3: DiscardReview ---------------------------------------------------

func TestDiscardReview_Success(t *testing.T) {
	withFakeDecisionScript(t)
	base := t.TempDir()
	spoolPath := writeFakeSpoolFile(t, base, map[string]string{
		"repo": "owner/repo", "pr": "1", "verdict": "APPROVE", "decision": "",
	}, "review body\n")
	rec := recordForSpool("owner/repo", 1, spoolPath)

	if err := DiscardReview(rec); err != nil {
		t.Fatalf("DiscardReview() unexpected error: %v", err)
	}

	if _, err := os.Stat(spoolPath); !os.IsNotExist(err) {
		t.Errorf("expected spool file to be moved out of pending/, stat err = %v", err)
	}
	donePath := swapPendingDone(spoolPath, true)
	data, err := os.ReadFile(donePath)
	if err != nil {
		t.Fatalf("expected archived file at %s: %v", donePath, err)
	}
	fields := ParseSpoolFrontMatter(data)
	if fields["decision"] != "discard" {
		t.Errorf("decision = %q, want %q", fields["decision"], "discard")
	}
}

func TestDiscardReview_SpoolNotFound(t *testing.T) {
	base := t.TempDir()
	missing := filepath.Join(base, "pending", "pr-review-owner-repo-9.md")
	rec := recordForSpool("owner/repo", 9, missing)

	err := DiscardReview(rec)
	if err == nil || !strings.Contains(err.Error(), "no spool file") {
		t.Errorf("DiscardReview() error = %v, want it to mention 'no spool file'", err)
	}
}

func TestDiscardReview_AlreadyInDone(t *testing.T) {
	base := t.TempDir()
	doneDir := filepath.Join(base, "done")
	if err := os.MkdirAll(doneDir, 0o755); err != nil {
		t.Fatalf("mkdir done: %v", err)
	}
	filename := "pr-review-owner-repo-3.md"
	if err := os.WriteFile(filepath.Join(doneDir, filename), []byte("---\ndecision: post\n---\nbody\n"), 0o644); err != nil {
		t.Fatalf("write done fixture: %v", err)
	}
	pendingPath := filepath.Join(base, "pending", filename)
	rec := recordForSpool("owner/repo", 3, pendingPath)

	err := DiscardReview(rec)
	if err == nil || !strings.Contains(err.Error(), "already finalized") {
		t.Errorf("DiscardReview() error = %v, want it to mention 'already finalized'", err)
	}
}

// --- Task 3: PostReview -------------------------------------------------------

func TestPostReview_Success_ArchivesToDone(t *testing.T) {
	withFakeDecisionScript(t)
	os.Setenv("HOWMUX_NO_HUMANIZE", "1")
	defer os.Unsetenv("HOWMUX_NO_HUMANIZE")

	base := t.TempDir()
	body := "# Review\n\nsrc/foo.py:10 - warning - issue -> fix\n\nVERDICT: COMMENT\n"
	spoolPath := writeFakeSpoolFile(t, base, map[string]string{
		"repo": "owner/repo", "pr": "7", "verdict": "COMMENT", "decision": "",
	}, body)
	rec := recordForSpool("owner/repo", 7, spoolPath)

	ghCalls := withFakeGhPost(t, []byte(`{"id":1}`), nil)

	commentsPosted, err := PostReview(context.Background(), rec)
	if err != nil {
		t.Fatalf("PostReview() unexpected error: %v", err)
	}
	if commentsPosted != 1 {
		t.Errorf("commentsPosted = %d, want 1", commentsPosted)
	}
	if len(*ghCalls) != 1 {
		t.Fatalf("expected exactly one gh call, got %d", len(*ghCalls))
	}
	if (*ghCalls)[0].repo != "owner/repo" || (*ghCalls)[0].pr != 7 {
		t.Errorf("gh call = %+v, want repo=owner/repo pr=7", (*ghCalls)[0])
	}

	if _, err := os.Stat(spoolPath); !os.IsNotExist(err) {
		t.Errorf("expected spool file archived out of pending/, stat err = %v", err)
	}
	donePath := swapPendingDone(spoolPath, true)
	if _, err := os.Stat(donePath); err != nil {
		t.Errorf("expected archived file at %s: %v", donePath, err)
	}
}

func TestPostReview_GhFails_LeavesInPending_DecisionAlreadyWritten(t *testing.T) {
	withFakeDecisionScript(t)
	os.Setenv("HOWMUX_NO_HUMANIZE", "1")
	defer os.Unsetenv("HOWMUX_NO_HUMANIZE")

	base := t.TempDir()
	body := "# Review\n\nsrc/foo.py:10 - warning - issue -> fix\n\nVERDICT: COMMENT\n"
	spoolPath := writeFakeSpoolFile(t, base, map[string]string{
		"repo": "owner/repo", "pr": "8", "verdict": "COMMENT", "decision": "",
	}, body)
	rec := recordForSpool("owner/repo", 8, spoolPath)

	withFakeGhPost(t, nil, fmt.Errorf("gh api failed: HTTP 500"))

	_, err := PostReview(context.Background(), rec)
	if err == nil {
		t.Fatal("PostReview() error = nil, want non-nil error on gh failure")
	}

	// Crash-recovery contract: file must still be in pending/, with
	// decision: post already written.
	if _, statErr := os.Stat(spoolPath); statErr != nil {
		t.Fatalf("expected spool file to remain in pending/ after gh failure, stat err = %v", statErr)
	}
	data, readErr := os.ReadFile(spoolPath)
	if readErr != nil {
		t.Fatalf("failed to re-read spool file: %v", readErr)
	}
	fields := ParseSpoolFrontMatter(data)
	if fields["decision"] != "post" {
		t.Errorf("decision = %q, want %q (must already be written despite gh failure)", fields["decision"], "post")
	}

	donePath := swapPendingDone(spoolPath, true)
	if _, statErr := os.Stat(donePath); !os.IsNotExist(statErr) {
		t.Errorf("expected no archived copy in done/ after gh failure, stat err = %v", statErr)
	}
}

func TestPostReview_HumanizeFailureDoesNotBlockPost(t *testing.T) {
	withFakeDecisionScript(t)
	os.Unsetenv("HOWMUX_NO_HUMANIZE")

	base := t.TempDir()
	body := "# Review\n\nsrc/foo.py:10 - warning - issue -> fix\n\nVERDICT: COMMENT\n"
	spoolPath := writeFakeSpoolFile(t, base, map[string]string{
		"repo": "owner/repo", "pr": "9", "verdict": "COMMENT", "decision": "",
	}, body)
	rec := recordForSpool("owner/repo", 9, spoolPath)

	withFakeKiroOneshot(t, nil, map[string]error{"humanizer": fmt.Errorf("kiro-cli exited 1")})
	ghCalls := withFakeGhPost(t, []byte(`{"id":1}`), nil)

	commentsPosted, err := PostReview(context.Background(), rec)
	if err != nil {
		t.Fatalf("PostReview() unexpected error (humanize failure must not block post): %v", err)
	}
	if commentsPosted != 1 {
		t.Errorf("commentsPosted = %d, want 1", commentsPosted)
	}
	if len(*ghCalls) != 1 {
		t.Fatalf("expected exactly one gh call, got %d", len(*ghCalls))
	}
}

// --- Task 4: ReviseReview -----------------------------------------------------

func TestReviseReview_Success_ClearsDecisionAndRewritesBody(t *testing.T) {
	base := t.TempDir()
	spoolPath := writeFakeSpoolFile(t, base, map[string]string{
		"repo": "owner/repo", "pr": "11", "verdict": "COMMENT", "decision": "revise",
		"decision_notes": "please recheck the auth flow",
	}, "# Old Review\n\nold findings\nVERDICT: COMMENT\n")
	rec := recordForSpool("owner/repo", 11, spoolPath)

	newRaw := "some narration\n# Revised Review\n\nsrc/foo.py:1 - warning - x -> y\nVERDICT: REQUEST_CHANGES"
	withFakeKiroOneshot(t, map[string]string{"review-consolidator": newRaw}, nil)

	verdict, err := ReviseReview(context.Background(), rec)
	if err != nil {
		t.Fatalf("ReviseReview() unexpected error: %v", err)
	}
	if verdict != "REQUEST_CHANGES" {
		t.Errorf("verdict = %q, want %q", verdict, "REQUEST_CHANGES")
	}

	data, readErr := os.ReadFile(spoolPath)
	if readErr != nil {
		t.Fatalf("failed to re-read spool file: %v", readErr)
	}
	fields := ParseSpoolFrontMatter(data)
	if fields["decision"] != "" {
		t.Errorf("decision = %q, want empty (cleared)", fields["decision"])
	}
	if fields["verdict"] != "REQUEST_CHANGES" {
		t.Errorf("verdict field = %q, want %q", fields["verdict"], "REQUEST_CHANGES")
	}
	body := extractSpoolBody(data)
	if !strings.Contains(body, "# Revised Review") {
		t.Errorf("body = %q, want it to contain the revised content", body)
	}
	if strings.Contains(body, "old findings") {
		t.Errorf("body still contains old content: %q", body)
	}
}

func TestReviseReview_NoNotesProvided_UsesPlaceholderText(t *testing.T) {
	base := t.TempDir()
	spoolPath := writeFakeSpoolFile(t, base, map[string]string{
		"repo": "owner/repo", "pr": "12", "verdict": "COMMENT", "decision": "revise",
		"decision_notes": "",
	}, "# Review\n\nfindings\nVERDICT: COMMENT\n")
	rec := recordForSpool("owner/repo", 12, spoolPath)

	calls := withFakeKiroOneshot(t, map[string]string{
		"review-consolidator": "# Revised\nVERDICT: COMMENT",
	}, nil)

	if _, err := ReviseReview(context.Background(), rec); err != nil {
		t.Fatalf("ReviseReview() unexpected error: %v", err)
	}

	if len(*calls) != 1 {
		t.Fatalf("expected exactly one kiroOneshotFunc call, got %d", len(*calls))
	}
	if !strings.Contains((*calls)[0].prompt, "(no notes provided)") {
		t.Errorf("prompt = %q, want it to contain the literal placeholder %q", (*calls)[0].prompt, "(no notes provided)")
	}
}

func TestReviseReview_KiroCallFails_ReturnsError_SpoolUnchanged(t *testing.T) {
	base := t.TempDir()
	original := "# Original Review\n\nfindings\nVERDICT: COMMENT\n"
	spoolPath := writeFakeSpoolFile(t, base, map[string]string{
		"repo": "owner/repo", "pr": "13", "verdict": "COMMENT", "decision": "revise",
	}, original)
	rec := recordForSpool("owner/repo", 13, spoolPath)

	beforeData, err := os.ReadFile(spoolPath)
	if err != nil {
		t.Fatalf("failed to read fixture before call: %v", err)
	}

	withFakeKiroOneshot(t, nil, map[string]error{"review-consolidator": fmt.Errorf("kiro-cli exited 1")})

	_, callErr := ReviseReview(context.Background(), rec)
	if callErr == nil {
		t.Fatal("ReviseReview() error = nil, want non-nil error on kiro-cli failure")
	}

	afterData, readErr := os.ReadFile(spoolPath)
	if readErr != nil {
		t.Fatalf("failed to re-read spool file: %v", readErr)
	}
	if string(afterData) != string(beforeData) {
		t.Errorf("spool file was modified despite kiro-cli failure.\nbefore: %q\nafter:  %q", beforeData, afterData)
	}
}

// --- Task 4: RereviewReview ---------------------------------------------------

func TestRereviewReview_DiffFileMissing_DegradesToRevise(t *testing.T) {
	base := t.TempDir()
	spoolPath := writeFakeSpoolFile(t, base, map[string]string{
		"repo": "owner/repo", "pr": "21", "verdict": "COMMENT", "decision": "rereview",
		"diff_file": filepath.Join(base, "nonexistent.diff"),
	}, "# Review\n\nfindings\nVERDICT: COMMENT\n")
	rec := recordForSpool("owner/repo", 21, spoolPath)

	calls := withFakeKiroOneshot(t, map[string]string{
		"review-consolidator": "# Revised\nVERDICT: APPROVE",
	}, nil)

	verdict, degraded, err := RereviewReview(context.Background(), rec)
	if err != nil {
		t.Fatalf("RereviewReview() unexpected error: %v", err)
	}
	if !degraded {
		t.Error("degradedToRevise = false, want true when diff_file is missing")
	}
	if verdict != "APPROVE" {
		t.Errorf("verdict = %q, want %q", verdict, "APPROVE")
	}

	if len(*calls) != 1 {
		t.Fatalf("expected exactly one kiroOneshotFunc call, got %d", len(*calls))
	}
	if (*calls)[0].agentName != "review-consolidator" {
		t.Errorf("agentName = %q, want %q (should call the consolidator directly, not a lens agent)", (*calls)[0].agentName, "review-consolidator")
	}
}

func TestRereviewReview_DiffFileEmpty_DegradesToRevise(t *testing.T) {
	base := t.TempDir()
	spoolPath := writeFakeSpoolFile(t, base, map[string]string{
		"repo": "owner/repo", "pr": "22", "verdict": "COMMENT", "decision": "rereview",
		"diff_file": "",
	}, "# Review\n\nfindings\nVERDICT: COMMENT\n")
	rec := recordForSpool("owner/repo", 22, spoolPath)

	withFakeKiroOneshot(t, map[string]string{
		"review-consolidator": "# Revised\nVERDICT: APPROVE",
	}, nil)

	_, degraded, err := RereviewReview(context.Background(), rec)
	if err != nil {
		t.Fatalf("RereviewReview() unexpected error: %v", err)
	}
	if !degraded {
		t.Error("degradedToRevise = false, want true when diff_file is empty")
	}
}

func TestRereviewReview_DiffFileExists_FansOutToAllLenses(t *testing.T) {
	base := t.TempDir()
	diffPath := filepath.Join(base, "pr.diff")
	if err := os.WriteFile(diffPath, []byte("diff --git a/foo.py b/foo.py\n"), 0o644); err != nil {
		t.Fatalf("write diff fixture: %v", err)
	}
	spoolPath := writeFakeSpoolFile(t, base, map[string]string{
		"repo": "owner/repo", "pr": "23", "verdict": "COMMENT", "decision": "rereview",
		"diff_file": diffPath,
	}, "# Review\n\nfindings\nVERDICT: COMMENT\n")
	rec := recordForSpool("owner/repo", 23, spoolPath)

	calls := withFakeKiroOneshot(t, map[string]string{
		"python-reviewer":          "language lens output",
		"review-security-agent":    "security lens output",
		"review-performance-agent": "performance lens output",
		"review-testing-agent":     "testing lens output",
		"review-consolidator":      "# Consolidated\nVERDICT: REQUEST_CHANGES",
	}, nil)

	verdict, degraded, err := RereviewReview(context.Background(), rec)
	if err != nil {
		t.Fatalf("RereviewReview() unexpected error: %v", err)
	}
	if degraded {
		t.Error("degradedToRevise = true, want false when diff_file exists")
	}
	if verdict != "REQUEST_CHANGES" {
		t.Errorf("verdict = %q, want %q", verdict, "REQUEST_CHANGES")
	}

	// One call per lens (4) plus one consolidation call = 5.
	if len(*calls) != 5 {
		t.Fatalf("expected 5 kiroOneshotFunc calls (4 lenses + consolidate), got %d: %+v", len(*calls), *calls)
	}
	agentsCalled := map[string]int{}
	for _, c := range *calls {
		agentsCalled[c.agentName]++
	}
	for _, want := range []string{"python-reviewer", "review-security-agent", "review-performance-agent", "review-testing-agent", "review-consolidator"} {
		if agentsCalled[want] != 1 {
			t.Errorf("agent %q called %d times, want exactly 1", want, agentsCalled[want])
		}
	}
}

func TestRereviewReview_LensCallFails_PropagatesError(t *testing.T) {
	base := t.TempDir()
	diffPath := filepath.Join(base, "pr.diff")
	if err := os.WriteFile(diffPath, []byte("diff --git a/foo.py b/foo.py\n"), 0o644); err != nil {
		t.Fatalf("write diff fixture: %v", err)
	}
	original := "# Original\n\nfindings\nVERDICT: COMMENT\n"
	spoolPath := writeFakeSpoolFile(t, base, map[string]string{
		"repo": "owner/repo", "pr": "24", "verdict": "COMMENT", "decision": "rereview",
		"diff_file": diffPath,
	}, original)
	rec := recordForSpool("owner/repo", 24, spoolPath)

	beforeData, err := os.ReadFile(spoolPath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	withFakeKiroOneshot(t, map[string]string{
		"python-reviewer":          "ok",
		"review-performance-agent": "ok",
		"review-testing-agent":     "ok",
	}, map[string]error{
		"review-security-agent": fmt.Errorf("kiro-cli exited 1 for security lens"),
	})

	_, _, callErr := RereviewReview(context.Background(), rec)
	if callErr == nil {
		t.Fatal("RereviewReview() error = nil, want non-nil error when a lens call fails")
	}

	afterData, readErr := os.ReadFile(spoolPath)
	if readErr != nil {
		t.Fatalf("re-read spool file: %v", readErr)
	}
	if string(afterData) != string(beforeData) {
		t.Errorf("spool file was modified despite a lens failure.\nbefore: %q\nafter:  %q", beforeData, afterData)
	}
}

// --- Task 4 / concurrency: race-condition-free lens fan-out -----------------

// TestRereviewReview_LensFanOut_NoRaceCondition exercises RereviewReview's
// concurrent lens fan-out with a fake kiroOneshotFunc that sleeps a small
// random duration per call (maximizing the chance of exposing any
// accidental shared-state write if the disjoint-slice-index pattern is not
// followed correctly). Must be run with `go test -race`.
func TestRereviewReview_LensFanOut_NoRaceCondition(t *testing.T) {
	base := t.TempDir()
	diffPath := filepath.Join(base, "pr.diff")
	if err := os.WriteFile(diffPath, []byte("diff --git a/foo.py b/foo.py\n"), 0o644); err != nil {
		t.Fatalf("write diff fixture: %v", err)
	}
	spoolPath := writeFakeSpoolFile(t, base, map[string]string{
		"repo": "owner/repo", "pr": "25", "verdict": "COMMENT", "decision": "rereview",
		"diff_file": diffPath,
	}, "# Review\n\nfindings\nVERDICT: COMMENT\n")
	rec := recordForSpool("owner/repo", 25, spoolPath)

	orig := kiroOneshotFunc
	defer func() { kiroOneshotFunc = orig }()

	var counter int64
	var mu sync.Mutex
	kiroOneshotFunc = func(ctx context.Context, agentName string, prompt string) (string, error) {
		// Small jittered sleep to widen the window in which a shared-state
		// race (if present) would manifest.
		sleepFor := time.Duration(1+(len(agentName)%3)) * time.Millisecond
		time.Sleep(sleepFor)

		mu.Lock()
		counter++
		mu.Unlock()

		if agentName == "review-consolidator" {
			return "# Consolidated\nVERDICT: APPROVE", nil
		}
		return fmt.Sprintf("output from %s", agentName), nil
	}

	verdict, degraded, err := RereviewReview(context.Background(), rec)
	if err != nil {
		t.Fatalf("RereviewReview() unexpected error: %v", err)
	}
	if degraded {
		t.Error("degradedToRevise = true, want false")
	}
	if verdict != "APPROVE" {
		t.Errorf("verdict = %q, want %q", verdict, "APPROVE")
	}
	if counter != 5 {
		t.Errorf("counter = %d, want 5 (4 lenses + 1 consolidation)", counter)
	}
}
