package review

import (
	"fmt"
	"path/filepath"
	"strings"
)

// recordDir returns the directory name for a PR record
// Example: recordDir("owner", "name", 123) → "owner-name-123"
func recordDir(owner, repo string, pr int) string {
	return fmt.Sprintf("%s-%s-%d", owner, repo, pr)
}

// RecordDir returns the directory name for a given repo and PR
// This is a pure function exported for testing and external use
// Handles "owner/name" format by splitting on the slash
// For repos with multiple slashes (like "owner/name/extra"), only the first two parts are used
func RecordDir(repo string, pr int) string {
	parts := strings.SplitN(repo, "/", 3)
	if len(parts) < 2 {
		return fmt.Sprintf("%s-%d", repo, pr)
	}
	// Use only the first two parts (owner and repo name)
	return recordDir(parts[0], parts[1], pr)
}

// recordFilename returns the full path to record.json
func recordFilename(baseDir, dirName string) string {
	return filepath.Join(baseDir, dirName, "record.json")
}

// reviewsDir returns the path to the reviews/ subdirectory
func reviewsDir(baseDir, dirName string) string {
	return filepath.Join(baseDir, dirName, "reviews")
}
