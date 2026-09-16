package github

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestPRMethods tests the PR struct methods
func TestPRMethods(t *testing.T) {
	tests := []struct {
		name     string
		pr       PR
		wantTerm bool
	}{
		{
			name:     "open PR is not terminal",
			pr:       PR{State: "OPEN"},
			wantTerm: false,
		},
		{
			name:     "merged PR is terminal",
			pr:       PR{State: "MERGED"},
			wantTerm: true,
		},
		{
			name:     "closed PR is terminal",
			pr:       PR{State: "CLOSED"},
			wantTerm: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.pr.IsTerminal(); got != tt.wantTerm {
				t.Errorf("IsTerminal() = %v, want %v", got, tt.wantTerm)
			}
		})
	}
}

func TestIsReviewRequestedFor(t *testing.T) {
	tests := []struct {
		name  string
		pr    PR
		login string
		want  bool
	}{
		{
			name: "login present in reviewRequests",
			pr: PR{
				ReviewRequests: []ReviewRequest{
					{Login: "user1"},
					{Login: "testuser"},
					{Login: "user3"},
				},
			},
			login: "testuser",
			want:  true,
		},
		{
			name: "login absent from reviewRequests",
			pr: PR{
				ReviewRequests: []ReviewRequest{
					{Login: "user1"},
					{Login: "user2"},
				},
			},
			login: "testuser",
			want:  false,
		},
		{
			name:  "empty reviewRequests",
			pr:    PR{ReviewRequests: []ReviewRequest{}},
			login: "testuser",
			want:  false,
		},
		{
			name:  "nil reviewRequests",
			pr:    PR{ReviewRequests: nil},
			login: "testuser",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.pr.IsReviewRequestedFor(tt.login); got != tt.want {
				t.Errorf("IsReviewRequestedFor(%q) = %v, want %v", tt.login, got, tt.want)
			}
		})
	}
}

func TestHeadSHA(t *testing.T) {
	expectedSHA := "abc123def456789012345678901234567890abcd"
	pr := PR{HeadRefOid: expectedSHA}

	if got := pr.HeadSHA(); got != expectedSHA {
		t.Errorf("HeadSHA() = %q, want %q", got, expectedSHA)
	}
}

// TestPRViewArgs tests the argv construction for gh pr view
func TestPRViewArgs(t *testing.T) {
	tests := []struct {
		name string
		repo string
		pr   int
		want []string
	}{
		{
			name: "basic repo and PR number",
			repo: "owner/name",
			pr:   123,
			want: []string{
				"pr", "view", "123",
				"--repo", "owner/name",
				"--json", "state,isDraft,mergedAt,closedAt,headRefOid,headRefName,reviewRequests,url,number",
			},
		},
		{
			name: "org with hyphens",
			repo: "my-org/my-repo",
			pr:   456,
			want: []string{
				"pr", "view", "456",
				"--repo", "my-org/my-repo",
				"--json", "state,isDraft,mergedAt,closedAt,headRefOid,headRefName,reviewRequests,url,number",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := prViewArgs(tt.repo, tt.pr)
			if len(got) != len(tt.want) {
				t.Fatalf("prViewArgs() returned %d args, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("prViewArgs()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestParsePRView tests parsing of gh pr view JSON output using fixtures
func TestParsePRView(t *testing.T) {
	tests := []struct {
		name         string
		fixture      string
		wantState    string
		wantDraft    bool
		wantMergedAt bool
		wantClosedAt bool
		wantNumber   int
		wantURL      string
		wantHeadSHA  string
		wantBranch   string
		wantReviews  int
	}{
		{
			name:         "open PR",
			fixture:      "pr_open.json",
			wantState:    "OPEN",
			wantDraft:    false,
			wantMergedAt: false,
			wantClosedAt: false,
			wantNumber:   123,
			wantURL:      "https://github.com/owner/repo/pull/123",
			wantHeadSHA:  "abc123def456789012345678901234567890abcd",
			wantBranch:   "feature/add-pr-queries",
			wantReviews:  0,
		},
		{
			name:         "merged PR",
			fixture:      "pr_merged.json",
			wantState:    "MERGED",
			wantDraft:    false,
			wantMergedAt: true,
			wantClosedAt: true,
			wantNumber:   456,
			wantURL:      "https://github.com/owner/repo/pull/456",
			wantHeadSHA:  "def456abc789012345678901234567890abcdef1",
			wantBranch:   "fix/some-bug",
			wantReviews:  0,
		},
		{
			name:         "closed PR",
			fixture:      "pr_closed.json",
			wantState:    "CLOSED",
			wantDraft:    false,
			wantMergedAt: false,
			wantClosedAt: true,
			wantNumber:   789,
			wantURL:      "https://github.com/owner/repo/pull/789",
			wantHeadSHA:  "789abc012345678901234567890abcdef123456",
			wantBranch:   "feature/abandoned",
			wantReviews:  0,
		},
		{
			name:         "draft PR",
			fixture:      "pr_draft.json",
			wantState:    "OPEN",
			wantDraft:    true,
			wantMergedAt: false,
			wantClosedAt: false,
			wantNumber:   999,
			wantURL:      "https://github.com/owner/repo/pull/999",
			wantHeadSHA:  "012345678901234567890abcdef123456789abc",
			wantBranch:   "wip/experimental-feature",
			wantReviews:  0,
		},
		{
			name:         "PR with reviewers",
			fixture:      "pr_with_reviewers.json",
			wantState:    "OPEN",
			wantDraft:    false,
			wantMergedAt: false,
			wantClosedAt: false,
			wantNumber:   234,
			wantURL:      "https://github.com/owner/repo/pull/234",
			wantHeadSHA:  "345678901234567890abcdef123456789abc012",
			wantBranch:   "feature/needs-review",
			wantReviews:  2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Read fixture
			data, err := os.ReadFile(filepath.Join("testdata", tt.fixture))
			if err != nil {
				t.Fatalf("failed to read fixture %s: %v", tt.fixture, err)
			}

			// Parse
			pr, err := parsePRView(data)
			if err != nil {
				t.Fatalf("parsePRView() error = %v", err)
			}

			// Verify fields
			if pr.State != tt.wantState {
				t.Errorf("State = %q, want %q", pr.State, tt.wantState)
			}
			if pr.IsDraft != tt.wantDraft {
				t.Errorf("IsDraft = %v, want %v", pr.IsDraft, tt.wantDraft)
			}
			if (pr.MergedAt != nil) != tt.wantMergedAt {
				t.Errorf("MergedAt != nil = %v, want %v", pr.MergedAt != nil, tt.wantMergedAt)
			}
			if (pr.ClosedAt != nil) != tt.wantClosedAt {
				t.Errorf("ClosedAt != nil = %v, want %v", pr.ClosedAt != nil, tt.wantClosedAt)
			}
			if pr.Number != tt.wantNumber {
				t.Errorf("Number = %d, want %d", pr.Number, tt.wantNumber)
			}
			if pr.URL != tt.wantURL {
				t.Errorf("URL = %q, want %q", pr.URL, tt.wantURL)
			}
			if pr.HeadRefOid != tt.wantHeadSHA {
				t.Errorf("HeadRefOid = %q, want %q", pr.HeadRefOid, tt.wantHeadSHA)
			}
			if pr.HeadRefName != tt.wantBranch {
				t.Errorf("HeadRefName = %q, want %q", pr.HeadRefName, tt.wantBranch)
			}
			if len(pr.ReviewRequests) != tt.wantReviews {
				t.Errorf("ReviewRequests count = %d, want %d", len(pr.ReviewRequests), tt.wantReviews)
			}

			// For PR with reviewers, verify specific login
			if tt.fixture == "pr_with_reviewers.json" {
				if !pr.IsReviewRequestedFor("testuser") {
					t.Error("IsReviewRequestedFor(\"testuser\") = false, want true")
				}
				if pr.IsReviewRequestedFor("nonexistent") {
					t.Error("IsReviewRequestedFor(\"nonexistent\") = true, want false")
				}
			}
		})
	}
}

// TestParsePRViewMalformed tests error handling for malformed JSON
func TestParsePRViewMalformed(t *testing.T) {
	malformed := []byte(`{"state": "OPEN", "number": `)
	_, err := parsePRView(malformed)
	if err == nil {
		t.Error("parsePRView() with malformed JSON should return error")
	}
}

// TestResolvePRURL tests URL parsing
func TestResolvePRURL(t *testing.T) {
	tests := []struct {
		name      string
		url       string
		wantOwner string
		wantRepo  string
		wantPR    int
		wantErr   bool
	}{
		{
			name:      "valid HTTPS URL",
			url:       "https://github.com/owner/repo/pull/123",
			wantOwner: "owner",
			wantRepo:  "repo",
			wantPR:    123,
			wantErr:   false,
		},
		{
			name:      "valid HTTP URL",
			url:       "http://github.com/owner/repo/pull/456",
			wantOwner: "owner",
			wantRepo:  "repo",
			wantPR:    456,
			wantErr:   false,
		},
		{
			name:      "repo with hyphens and underscores",
			url:       "https://github.com/my-org/my_repo/pull/789",
			wantOwner: "my-org",
			wantRepo:  "my_repo",
			wantPR:    789,
			wantErr:   false,
		},
		{
			name:    "invalid - issue URL not PR",
			url:     "https://github.com/owner/repo/issues/123",
			wantErr: true,
		},
		{
			name:    "invalid - malformed URL",
			url:     "not-a-url",
			wantErr: true,
		},
		{
			name:    "invalid - missing PR number",
			url:     "https://github.com/owner/repo/pull/",
			wantErr: true,
		},
		{
			name:    "invalid - non-github domain",
			url:     "https://gitlab.com/owner/repo/pull/123",
			wantErr: true,
		},
		{
			name:    "invalid - too short path",
			url:     "https://github.com/owner/repo",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			owner, repo, pr, err := ResolvePRURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("ResolvePRURL() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if owner != tt.wantOwner {
					t.Errorf("owner = %q, want %q", owner, tt.wantOwner)
				}
				if repo != tt.wantRepo {
					t.Errorf("repo = %q, want %q", repo, tt.wantRepo)
				}
				if pr != tt.wantPR {
					t.Errorf("pr = %d, want %d", pr, tt.wantPR)
				}
			}
		})
	}
}

// TestPRWorkflow demonstrates the full workflow without calling gh
func TestPRWorkflow(t *testing.T) {
	// Step 1: Get argv for gh pr view
	argv := prViewArgs("owner/name", 123)

	// Step 2: Verify argv is correct
	expectedArgv := []string{
		"pr", "view", "123",
		"--repo", "owner/name",
		"--json", "state,isDraft,mergedAt,closedAt,headRefOid,headRefName,reviewRequests,url,number",
	}
	if len(argv) != len(expectedArgv) {
		t.Fatalf("argv length = %d, want %d", len(argv), len(expectedArgv))
	}
	for i := range argv {
		if argv[i] != expectedArgv[i] {
			t.Errorf("argv[%d] = %q, want %q", i, argv[i], expectedArgv[i])
		}
	}

	// Step 3: Simulate gh pr view output by reading fixture
	data, err := os.ReadFile(filepath.Join("testdata", "pr_open.json"))
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}

	// Step 4: Parse the JSON
	pr, err := parsePRView(data)
	if err != nil {
		t.Fatalf("parsePRView() error = %v", err)
	}

	// Step 5: Verify parsed PR
	if pr.State != "OPEN" {
		t.Errorf("State = %q, want OPEN", pr.State)
	}
	if pr.Number != 123 {
		t.Errorf("Number = %d, want 123", pr.Number)
	}
	if pr.HeadRefOid != "abc123def456789012345678901234567890abcd" {
		t.Errorf("HeadRefOid = %q, want abc123def456789012345678901234567890abcd", pr.HeadRefOid)
	}

	// Step 6: Call derived methods
	if pr.IsTerminal() {
		t.Error("IsTerminal() = true, want false for OPEN PR")
	}
	if pr.IsReviewRequestedFor("testuser") {
		t.Error("IsReviewRequestedFor(\"testuser\") = true, want false (no reviewers in fixture)")
	}
	if sha := pr.HeadSHA(); sha != "abc123def456789012345678901234567890abcd" {
		t.Errorf("HeadSHA() = %q, want abc123def456789012345678901234567890abcd", sha)
	}
}

// TestPRTimestampParsing verifies that timestamps are correctly parsed
func TestPRTimestampParsing(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "pr_merged.json"))
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}

	pr, err := parsePRView(data)
	if err != nil {
		t.Fatalf("parsePRView() error = %v", err)
	}

	// Verify mergedAt is parsed correctly
	if pr.MergedAt == nil {
		t.Fatal("MergedAt should not be nil for merged PR")
	}
	expectedTime, _ := time.Parse(time.RFC3339, "2024-09-16T12:34:56Z")
	if !pr.MergedAt.Equal(expectedTime) {
		t.Errorf("MergedAt = %v, want %v", pr.MergedAt, expectedTime)
	}
}
