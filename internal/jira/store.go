package jira

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DefaultStoreDir is where retrieved tickets are persisted. It sits under the
// project's .kiro-krew runtime directory alongside retries/ and sessions/, and
// is gitignored.
const DefaultStoreDir = ".kiro-krew/jira-store"

// Store persists retrieved Jira tickets to disk. Each saved file doubles as a
// sentinel: its presence means the ticket has already been downloaded, so the
// watcher can skip re-fetching it (durable dedup across restarts).
type Store struct {
	dir string
}

// NewStore returns a Store rooted at dir. An empty dir uses DefaultStoreDir.
func NewStore(dir string) *Store {
	if strings.TrimSpace(dir) == "" {
		dir = DefaultStoreDir
	}
	return &Store{dir: dir}
}

// path returns the on-disk path for a ticket key. The key is sanitised so it
// cannot escape the store directory or produce an invalid filename.
func (s *Store) path(key string) string {
	return filepath.Join(s.dir, safeKey(key)+".md")
}

// Has reports whether a ticket has already been saved (the sentinel check).
func (s *Store) Has(key string) bool {
	_, err := os.Stat(s.path(key))
	return err == nil
}

// Save writes the ticket to disk as a small front-matter header plus the full
// retrieved text. It creates the store directory if needed. Save overwrites any
// existing file for the key.
func (s *Store) Save(issue Issue, details *IssueDetails) (string, error) {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return "", fmt.Errorf("failed to create jira store dir: %w", err)
	}

	fullText := ""
	if details != nil {
		fullText = details.FullText
	}

	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "key: %s\n", issue.Key)
	fmt.Fprintf(&b, "status: %s\n", issue.Status)
	fmt.Fprintf(&b, "type: %s\n", issue.Type)
	fmt.Fprintf(&b, "points: %s\n", issue.Points)
	fmt.Fprintf(&b, "assignee: %s\n", issue.Assignee)
	fmt.Fprintf(&b, "summary: %s\n", issue.Summary)
	fmt.Fprintf(&b, "retrieved_at: %s\n", time.Now().UTC().Format(time.RFC3339))
	b.WriteString("---\n\n")
	b.WriteString(fullText)
	b.WriteString("\n")

	filePath := s.path(issue.Key)
	if err := os.WriteFile(filePath, []byte(b.String()), 0o644); err != nil {
		return "", fmt.Errorf("failed to write ticket %s: %w", issue.Key, err)
	}
	return filePath, nil
}

// safeKey reduces a Jira key to characters safe for a filename. Jira keys are
// normally like "AEA-629"; this guards against anything unexpected.
func safeKey(key string) string {
	key = strings.TrimSpace(key)
	var b strings.Builder
	for _, r := range key {
		switch {
		case r >= 'A' && r <= 'Z',
			r >= 'a' && r <= 'z',
			r >= '0' && r <= '9',
			r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "unknown"
	}
	return b.String()
}
