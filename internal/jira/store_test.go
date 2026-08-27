package jira

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStore_SaveAndHas(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	issue := Issue{Key: "AEA-629", Status: "To Do", Type: "Task", Points: "-", Assignee: "Me", Summary: "Do the thing"}
	details := &IssueDetails{Key: "AEA-629", FullText: "Full body text here."}

	if s.Has("AEA-629") {
		t.Fatal("Has should be false before Save")
	}

	path, err := s.Save(issue, details)
	if err != nil {
		t.Fatalf("Save error: %v", err)
	}
	if !s.Has("AEA-629") {
		t.Error("Has should be true after Save")
	}

	if filepath.Base(path) != "AEA-629.md" {
		t.Errorf("path basename = %s, want AEA-629.md", filepath.Base(path))
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading saved file: %v", err)
	}
	content := string(data)
	for _, want := range []string{
		"key: AEA-629",
		"status: To Do",
		"summary: Do the thing",
		"retrieved_at:",
		"Full body text here.",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("saved content missing %q\n---\n%s", want, content)
		}
	}
}

func TestStore_DefaultDir(t *testing.T) {
	s := NewStore("")
	if s.dir != DefaultStoreDir {
		t.Errorf("dir = %q, want %q", s.dir, DefaultStoreDir)
	}
}

func TestStore_SafeKey(t *testing.T) {
	tests := map[string]string{
		"AEA-629":     "AEA-629",
		"proj_1":      "proj_1",
		"../evil":     "___evil",
		"a/b\\c":      "a_b_c",
		"  spaced  ":  "spaced",
		"":            "unknown",
	}
	for in, want := range tests {
		if got := safeKey(in); got != want {
			t.Errorf("safeKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStore_HasFalseForMissing(t *testing.T) {
	s := NewStore(t.TempDir())
	if s.Has("NOPE-1") {
		t.Error("Has should be false for a key never saved")
	}
}
