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

// ReviewRequest represents a pending review request
type ReviewRequest struct {
	Login string `json:"login"` // GitHub username
}

// IsTerminal returns true if the PR is merged or closed
func (pr PR) IsTerminal() bool {
	return pr.State == "MERGED" || pr.State == "CLOSED"
}

// IsReviewRequestedFor returns true if the given login appears in reviewRequests
func (pr PR) IsReviewRequestedFor(login string) bool {
	for _, req := range pr.ReviewRequests {
		if req.Login == login {
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

// ResolvePRURL parses a GitHub PR URL and extracts owner, repo, and PR number
func ResolvePRURL(prURL string) (owner, repo string, pr int, err error) {
	parsed, err := url.Parse(prURL)
	if err != nil {
		return "", "", 0, fmt.Errorf("invalid URL: %w", err)
	}

	// Check if it's a github.com URL
	if parsed.Host != "github.com" {
		return "", "", 0, fmt.Errorf("invalid GitHub PR URL format: not a github.com URL")
	}

	// Path should be /owner/repo/pull/number
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) != 4 || parts[2] != "pull" {
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
