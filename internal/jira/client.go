// Package jira wraps the `jtk` (jira-ticket-cli) command-line tool to list and
// retrieve Jira issues. It mirrors the shape of the internal/github package,
// which wraps `gh`. Unlike `gh`, `jtk` has no JSON output mode, so the search
// results are parsed from its pipe-delimited table format.
package jira

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// jtkBinary is the command used to talk to Jira. It is a package variable so
// tests can substitute a stub script.
var jtkBinary = "jtk"

// ErrNotAuthenticated indicates jtk could not authenticate against Jira.
var ErrNotAuthenticated = errors.New("jtk is not authenticated (run 'jtk init')")

// Issue is a single row from a `jtk issues search` result. Fields correspond to
// the columns: KEY | STATUS | TYPE | PTS | ASSIGNEE | SUMMARY.
type Issue struct {
	Key      string
	Status   string
	Type     string
	Points   string
	Assignee string
	Summary  string
}

// IssueDetails holds the full text of an issue as returned by
// `jtk issues get <KEY> --fulltext`. The body is kept as raw text because the
// human-oriented detail format is not reliably field-parseable.
type IssueDetails struct {
	Key      string
	FullText string
}

// searchColumnCount is the number of columns emitted by `jtk issues search`.
const searchColumnCount = 6

// ListTodoIssues runs a JQL search and returns the matching issues. The caller
// supplies the full JQL (the config layer derives a sensible default). max
// bounds the number of results requested from jtk.
func ListTodoIssues(jql string, max int) ([]Issue, error) {
	if strings.TrimSpace(jql) == "" {
		return nil, fmt.Errorf("jql must not be empty")
	}
	if max <= 0 {
		max = 50
	}

	cmd := exec.Command(jtkBinary, "issues", "search",
		"--jql", jql,
		"--max", fmt.Sprintf("%d", max),
		"--no-color",
	)
	output, err := cmd.Output()
	if err != nil {
		if isAuthError(err) {
			return nil, ErrNotAuthenticated
		}
		return nil, fmt.Errorf("jtk issues search failed: %w", err)
	}

	return parseSearchOutput(string(output)), nil
}

// GetIssue retrieves the full text of a single issue.
func GetIssue(key string) (*IssueDetails, error) {
	if strings.TrimSpace(key) == "" {
		return nil, fmt.Errorf("issue key must not be empty")
	}

	cmd := exec.Command(jtkBinary, "issues", "get", key, "--fulltext", "--no-color")
	output, err := cmd.Output()
	if err != nil {
		if isAuthError(err) {
			return nil, ErrNotAuthenticated
		}
		return nil, fmt.Errorf("jtk issues get failed for %s: %w", key, err)
	}

	return &IssueDetails{
		Key:      key,
		FullText: strings.TrimRight(string(output), "\n"),
	}, nil
}

// parseSearchOutput parses the pipe-delimited table emitted by
// `jtk issues search`. It tolerates:
//   - the header row (KEY | STATUS | ...), which is skipped
//   - a "No issues found" message (returns empty)
//   - a trailing "More results available (next: ...)" pagination line
//   - blank lines
//
// Rows are split with a bounded SplitN so that summaries containing " | " are
// preserved intact in the final column.
func parseSearchOutput(output string) []Issue {
	var issues []Issue

	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		// Skip the empty-result message and pagination trailer.
		if strings.EqualFold(line, "No issues found") {
			continue
		}
		if strings.HasPrefix(line, "More results available") {
			continue
		}
		// Only rows with the pipe delimiter are data/header rows.
		if !strings.Contains(line, "|") {
			continue
		}

		fields := strings.SplitN(line, "|", searchColumnCount)
		if len(fields) < searchColumnCount {
			// Malformed / partial row — skip rather than guess.
			continue
		}
		for i := range fields {
			fields[i] = strings.TrimSpace(fields[i])
		}

		// Skip the header row.
		if fields[0] == "KEY" && fields[1] == "STATUS" {
			continue
		}

		issues = append(issues, Issue{
			Key:      fields[0],
			Status:   fields[1],
			Type:     fields[2],
			Points:   fields[3],
			Assignee: fields[4],
			Summary:  fields[5],
		})
	}

	return issues
}

// isAuthError inspects a jtk exec error for authentication failure signals.
func isAuthError(err error) bool {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		stderr := strings.ToLower(string(exitErr.Stderr))
		return strings.Contains(stderr, "unauthorized") ||
			strings.Contains(stderr, "authentication") ||
			strings.Contains(stderr, "not authenticated") ||
			strings.Contains(stderr, "401")
	}
	return false
}
