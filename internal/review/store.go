// Package review provides a filesystem-based persistence layer for PR review state.
// Each PR is stored in its own directory under .howmux/reviews/, with a record.json
// file containing metadata and a reviews/ subdirectory for accumulated artifacts.
//
// All operations are atomic and crash-safe. No network or git operations are performed.
//
// This package has zero external dependencies and performs no network or git operations.
//
// Example usage:
//
//	store := review.NewStore(".howmux/reviews")
//	rec := review.Record{
//	    Repo:       "owner/name",
//	    PR:         123,
//	    URL:        "https://github.com/owner/name/pull/123",
//	    Status:     review.StatusWatching,
//	    EnrolledAt: time.Now().Format(time.RFC3339),
//	    ReviewDir:  ".worktrees/review-owner-name-123",
//	}
//	if err := store.Save(rec); err != nil {
//	    log.Fatal(err)
//	}
//
//	got, found, err := store.Get("owner/name", 123)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	if found {
//	    fmt.Printf("Status: %s\n", got.Status)
//	}
package review

import (
	"fmt"
	"os"
	"path/filepath"
)

// Store manages PR review records on the filesystem
type Store struct {
	baseDir string // Base directory (e.g., ".howmux/reviews")
}

// NewStore creates a new Store with the given base directory
func NewStore(baseDir string) *Store {
	return &Store{baseDir: baseDir}
}

// NewDefaultStore creates a Store using the default base directory
func NewDefaultStore() *Store {
	return NewStore(".howmux/reviews")
}

// Save persists a record to disk atomically
// Creates the PR directory and reviews/ subdirectory if they don't exist
// Returns error if validation fails or write fails
func (s *Store) Save(rec Record) error {
	// Validate first
	if err := rec.Validate(); err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	// Derive paths
	dirName := RecordDir(rec.Repo, rec.PR)
	prDir := filepath.Join(s.baseDir, dirName)
	reviewsSubdir := filepath.Join(prDir, "reviews")
	recordFile := filepath.Join(prDir, "record.json")

	// Create directories
	if err := os.MkdirAll(reviewsSubdir, 0755); err != nil {
		return fmt.Errorf("failed to create directories: %w", err)
	}

	// Serialize
	data, err := rec.ToJSON()
	if err != nil {
		return fmt.Errorf("failed to serialize: %w", err)
	}

	// Atomic, crash-durable write: write to a uniquely-named temp file in the
	// same directory, fsync it, rename over the target, then fsync the parent
	// directory so the rename itself is durable.
	//
	// The temp name is unique (os.CreateTemp) rather than a fixed
	// "record.json.tmp" so concurrent Saves of the *same* PR cannot clobber
	// each other's temp file. Same-directory temp keeps the rename atomic
	// (guaranteed same filesystem).
	tmp, err := os.CreateTemp(prDir, "record-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tempFile := tmp.Name()

	// Best-effort cleanup if we bail out before a successful rename.
	renamed := false
	defer func() {
		if !renamed {
			os.Remove(tempFile)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to write temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	if err := os.Rename(tempFile, recordFile); err != nil {
		return fmt.Errorf("failed to finalize write: %w", err)
	}
	renamed = true

	// Fsync the parent directory so the rename survives a crash. A failure
	// here is best-effort: the data is already durable and the rename has
	// succeeded, so don't fail the Save.
	if dir, err := os.Open(prDir); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}

	return nil
}

// Get retrieves a record by repo and PR number
// Returns (record, true, nil) if found
// Returns (empty, false, nil) if not found
// Returns (empty, false, error) on I/O error
func (s *Store) Get(repo string, pr int) (Record, bool, error) {
	dirName := RecordDir(repo, pr)
	recordFile := filepath.Join(s.baseDir, dirName, "record.json")

	data, err := os.ReadFile(recordFile)
	if err != nil {
		if os.IsNotExist(err) {
			return Record{}, false, nil
		}
		return Record{}, false, fmt.Errorf("failed to read record: %w", err)
	}

	rec, err := FromJSON(data)
	if err != nil {
		return Record{}, false, fmt.Errorf("failed to deserialize record: %w", err)
	}

	return *rec, true, nil
}

// List returns all stored records
// Returns empty slice if no records exist
// Returns error only on directory read failure
func (s *Store) List() ([]Record, error) {
	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []Record{}, nil
		}
		return nil, fmt.Errorf("failed to read reviews directory: %w", err)
	}

	var records []Record
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		recordFile := filepath.Join(s.baseDir, entry.Name(), "record.json")
		data, err := os.ReadFile(recordFile)
		if err != nil {
			// Skip invalid/unparseable records
			continue
		}

		rec, err := FromJSON(data)
		if err != nil {
			// Skip invalid/unparseable records
			continue
		}

		records = append(records, *rec)
	}

	return records, nil
}

// Remove deletes a PR's entire folder (record.json + reviews/)
// Returns nil if folder doesn't exist (idempotent)
// Returns error on deletion failure
func (s *Store) Remove(repo string, pr int) error {
	dirName := RecordDir(repo, pr)
	prDir := filepath.Join(s.baseDir, dirName)

	err := os.RemoveAll(prDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to remove PR directory: %w", err)
	}

	return nil
}
