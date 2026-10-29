package github

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// PR represents a pull request's state fetched from GitHub
type PR struct {
	State          string          `json:"state"`          // "OPEN", "MERGED", "CLOSED"
	IsDraft        bool            `json:"isDraft"`        // true if draft PR
	MergedAt       *time.Time      `json:"mergedAt"`       // nil if not merged
	ClosedAt       *time.Time      `json:"closedAt"`       // nil if not closed
	HeadRefOid     string          `json:"headRefOid"`     // commit SHA
	HeadRefName    string          `json:"headRefName"`    // branch name
	ReviewRequests []ReviewRequest `json:"reviewRequests"` // pending reviewers
	URL            string          `json:"url"`            // full PR URL
	Number         int             `json:"number"`         // PR number
}

// ReviewRequest represents a pending review request. GitHub's reviewRequests
// list mixes user reviewers (which carry a login) and team reviewers (which
// carry a slug/name and no login), so both shapes are captured here.
type ReviewRequest struct {
	Login string `json:"login"` // GitHub username (empty for team requests)
	Slug  string `json:"slug"`  // Team slug (empty for user requests)
	Name  string `json:"name"`  // Team name (empty for user requests)
}

// IsTerminal returns true if the PR is merged or closed
func (pr PR) IsTerminal() bool {
	return pr.State == "MERGED" || pr.State == "CLOSED"
}

// IsReviewRequestedFor reports whether the given reviewer is a pending
// reviewer on the PR. GitHub logins and team slugs are case-insensitive, so
// the match is case-insensitive. The reviewer may be a user login or a team
// slug/name.
func (pr PR) IsReviewRequestedFor(reviewer string) bool {
	for _, req := range pr.ReviewRequests {
		if req.Login != "" && strings.EqualFold(req.Login, reviewer) {
			return true
		}
		if req.Slug != "" && strings.EqualFold(req.Slug, reviewer) {
			return true
		}
		if req.Name != "" && strings.EqualFold(req.Name, reviewer) {
			return true
		}
	}
	return false
}

// HeadSHA returns the commit SHA of the PR head
func (pr PR) HeadSHA() string {
	return pr.HeadRefOid
}

// prViewArgs returns the exact argv slice for gh pr view with required JSON fields
func prViewArgs(repo string, pr int) []string {
	return []string{
		"pr", "view", fmt.Sprintf("%d", pr),
		"--repo", repo,
		"--json", "state,isDraft,mergedAt,closedAt,headRefOid,headRefName,reviewRequests,url,number",
	}
}

// parsePRView unmarshals JSON from gh pr view into a PR struct
func parsePRView(data []byte) (PR, error) {
	var pr PR
	if err := json.Unmarshal(data, &pr); err != nil {
		return PR{}, fmt.Errorf("failed to parse PR JSON: %w", err)
	}
	return pr, nil
}

// GetPR fetches a pull request's state from GitHub using gh CLI
func GetPR(repo string, pr int) (PR, error) {
	args := prViewArgs(repo, pr)
	cmd := exec.Command("gh", args...)
	output, err := cmd.Output()
	if err != nil {
		return PR{}, fmt.Errorf("gh pr view failed for %s #%d: %w", repo, pr, err)
	}

	parsed, err := parsePRView(output)
	if err != nil {
		return PR{}, fmt.Errorf("failed to parse PR %s #%d: %w", repo, pr, err)
	}

	return parsed, nil
}

// PRRef is the minimal identity of an open PR returned by a list query.
type PRRef struct {
	Number  int    `json:"number"`
	URL     string `json:"url"`
	IsDraft bool   `json:"isDraft"`
}

// reviewRequestedListLimit caps how many open review-requested PRs one list
// call returns per repo. Well above any realistic pending-request count.
const reviewRequestedListLimit = 100

// ValidateReviewerLogin checks that reviewer is usable as a GitHub user login
// for the user-review-requested: search qualifier. Discovery only matches
// direct requests to a user, so empty values and team identifiers (org/team)
// are rejected; GitHub's search API returns HTTP 422 for a team there.
func ValidateReviewerLogin(reviewer string) error {
	if strings.TrimSpace(reviewer) == "" {
		return fmt.Errorf("reviewer is required to list review-requested PRs")
	}
	if strings.ContainsAny(reviewer, "/ \t") {
		return fmt.Errorf("reviewer %q is not a GitHub user login; discovery only matches direct user review requests", reviewer)
	}
	return nil
}

// reviewRequestedArgs returns the argv for listing open PRs in repo with a
// pending review request made directly to the user login. It uses
// user-review-requested: rather than review-requested:, because the latter
// also matches requests to any team the user belongs to (e.g. CODEOWNERS
// teams), which would make discovery far broader than intended.
func reviewRequestedArgs(repo, login string) ([]string, error) {
	if err := ValidateReviewerLogin(login); err != nil {
		return nil, err
	}
	return []string{
		"pr", "list",
		"--repo", repo,
		"--state", "open",
		"--search", "user-review-requested:" + login,
		"--json", "number,url,isDraft",
		"--limit", strconv.Itoa(reviewRequestedListLimit),
	}, nil
}

// parsePRRefs unmarshals JSON from gh pr list into PR refs.
func parsePRRefs(data []byte) ([]PRRef, error) {
	var refs []PRRef
	if err := json.Unmarshal(data, &refs); err != nil {
		return nil, fmt.Errorf("failed to parse PR list JSON: %w", err)
	}
	return refs, nil
}

// ListReviewRequested returns the open PRs in repo that have a pending review
// request made directly to the user login (team requests are not matched).
// Drafts are included; callers decide whether to skip them. A login that
// fails ValidateReviewerLogin is rejected before anything is executed.
func ListReviewRequested(repo, login string) ([]PRRef, error) {
	args, err := reviewRequestedArgs(repo, login)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("gh", args...)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gh pr list failed for %s: %w", repo, err)
	}
	refs, err := parsePRRefs(output)
	if err != nil {
		return nil, fmt.Errorf("failed to parse PR list for %s: %w", repo, err)
	}
	return refs, nil
}

// ResolvePRURL parses a GitHub PR URL and extracts owner, repo, and PR number
func ResolvePRURL(prURL string) (owner, repo string, pr int, err error) {
	parsed, err := url.Parse(prURL)
	if err != nil {
		return "", "", 0, fmt.Errorf("invalid URL: %w", err)
	}

	// Check that it's a github.com URL (tolerate a leading www.)
	host := strings.TrimPrefix(parsed.Host, "www.")
	if host != "github.com" {
		return "", "", 0, fmt.Errorf("invalid GitHub PR URL format: not a github.com URL")
	}

	// Path is /owner/repo/pull/number, optionally followed by deep-link
	// segments such as /files, /commits, or /commits/<sha>. Accept anything
	// with at least those four leading segments and ignore the rest.
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) < 4 || parts[2] != "pull" {
		return "", "", 0, fmt.Errorf("invalid GitHub PR URL format: expected /owner/repo/pull/number")
	}

	owner = parts[0]
	repo = parts[1]

	prNum, err := strconv.Atoi(parts[3])
	if err != nil {
		return "", "", 0, fmt.Errorf("invalid PR number in URL: %w", err)
	}

	return owner, repo, prNum, nil
}
