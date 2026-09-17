package review

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseSpoolFrontMatter(t *testing.T) {
	tests := []struct {
		name string
		data string
		want map[string]string
	}{
		{
			name: "well-formed front-matter with blank decision/decision_notes",
			data: `---
repo: owner/repo
pr: 17
verdict: APPROVE
decision:
decision_notes:
diff_file: /abs/path/to.diff
generated: 2026-08-21T11:00Z
---

review body here
`,
			want: map[string]string{
				"repo":           "owner/repo",
				"pr":             "17",
				"verdict":        "APPROVE",
				"decision":       "",
				"decision_notes": "",
				"diff_file":      "/abs/path/to.diff",
				"generated":      "2026-08-21T11:00Z",
			},
		},
		{
			name: "no fence at all returns empty map",
			data: "repo: owner/repo\npr: 17\n",
			want: map[string]string{},
		},
		{
			name: "opening fence with no closing fence returns empty map",
			data: "---\nrepo: owner/repo\npr: 17\n",
			want: map[string]string{},
		},
		{
			name: "line with no colon is skipped",
			data: `---
repo: owner/repo
this line has no colon
pr: 17
---
`,
			want: map[string]string{
				"repo": "owner/repo",
				"pr":   "17",
			},
		},
		{
			name: "value containing a colon is preserved via first-colon split",
			data: `---
diff_file: https://example.com/path:with:colons
---
`,
			want: map[string]string{
				"diff_file": "https://example.com/path:with:colons",
			},
		},
		{
			name: "empty input returns empty map",
			data: "",
			want: map[string]string{},
		},
		{
			name: "blank lines inside fence are skipped without error",
			data: `---
repo: owner/repo

pr: 17

---
`,
			want: map[string]string{
				"repo": "owner/repo",
				"pr":   "17",
			},
		},
		{
			name: "leading/trailing whitespace around fence lines is tolerated",
			data: "  ---  \nrepo: owner/repo\n  ---  \n",
			want: map[string]string{
				"repo": "owner/repo",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseSpoolFrontMatter([]byte(tt.data))
			if len(got) != len(tt.want) {
				t.Fatalf("ParseSpoolFrontMatter() = %#v, want %#v", got, tt.want)
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("ParseSpoolFrontMatter()[%q] = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

func TestParseSpoolFrontMatterNeverPanics(t *testing.T) {
	// Malformed/absent front-matter must degrade gracefully, never panic.
	inputs := [][]byte{
		nil,
		[]byte(""),
		[]byte("---"),
		[]byte("not front matter at all"),
		[]byte("---\n"),
		[]byte(":::::"),
	}

	for _, in := range inputs {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("ParseSpoolFrontMatter panicked on input %q: %v", in, r)
				}
			}()
			_ = ParseSpoolFrontMatter(in)
		}()
	}
}

func TestClassifySpoolState(t *testing.T) {
	tests := []struct {
		name      string
		found     bool
		inDoneDir bool
		decision  string
		want      string
	}{
		{
			name:      "not found regardless of other args",
			found:     false,
			inDoneDir: true,
			decision:  "post",
			want:      "no spool",
		},
		{
			name:      "not found with defaults",
			found:     false,
			inDoneDir: false,
			decision:  "",
			want:      "no spool",
		},
		{
			name:      "found in done dir with decision=post -> posted",
			found:     true,
			inDoneDir: true,
			decision:  "post",
			want:      "posted",
		},
		{
			name:      "found in done dir with decision=POST (case-insensitive) -> posted",
			found:     true,
			inDoneDir: true,
			decision:  "POST",
			want:      "posted",
		},
		{
			name:      "found in done dir with decision=discard -> discarded (not posted)",
			found:     true,
			inDoneDir: true,
			decision:  "discard",
			want:      "discarded",
		},
		{
			name:      "found in done dir with empty decision -> done",
			found:     true,
			inDoneDir: true,
			decision:  "",
			want:      "done",
		},
		{
			name:      "found in done dir with revise decision -> done: revise",
			found:     true,
			inDoneDir: true,
			decision:  "revise",
			want:      "done: revise",
		},
		{
			name:      "found in pending with blank decision -> pending",
			found:     true,
			inDoneDir: false,
			decision:  "",
			want:      "pending",
		},
		{
			name:      "found in pending with decision=post -> decided: post",
			found:     true,
			inDoneDir: false,
			decision:  "post",
			want:      "decided: post",
		},
		{
			name:      "found in pending with decision=revise -> decided: revise",
			found:     true,
			inDoneDir: false,
			decision:  "revise",
			want:      "decided: revise",
		},
		{
			name:      "found in pending with decision=rereview -> decided: rereview",
			found:     true,
			inDoneDir: false,
			decision:  "rereview",
			want:      "decided: rereview",
		},
		{
			name:      "found in pending with decision=discard -> decided: discard",
			found:     true,
			inDoneDir: false,
			decision:  "discard",
			want:      "decided: discard",
		},
		{
			name:      "found in pending with unrecognized decision -> pass-through, not an error",
			found:     true,
			inDoneDir: false,
			decision:  "something-unexpected",
			want:      "decided: something-unexpected",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifySpoolState(tt.found, tt.inDoneDir, tt.decision)
			if got != tt.want {
				t.Errorf("ClassifySpoolState(%v, %v, %q) = %q, want %q", tt.found, tt.inDoneDir, tt.decision, got, tt.want)
			}
		})
	}
}

func TestReadSpoolInfoEmptyPath(t *testing.T) {
	// spoolPath == "" must short-circuit with zero filesystem calls. We pass
	// a homeDir value that would be nonsensical to actually touch, to help
	// prove (in spirit) that no I/O occurs; the real guarantee is enforced
	// by inspection of ReadSpoolInfo's implementation and by this test never
	// creating any files on disk.
	got := ReadSpoolInfo("", "/nonexistent/should/never/be/touched")

	want := SpoolInfo{Found: false, DecisionState: "no spool"}
	if got != want {
		t.Errorf("ReadSpoolInfo(\"\", ...) = %#v, want %#v", got, want)
	}
}

func TestReadSpoolInfoPendingFile(t *testing.T) {
	tmp := t.TempDir()
	pendingDir := filepath.Join(tmp, "pending")
	if err := os.MkdirAll(pendingDir, 0o755); err != nil {
		t.Fatalf("failed to set up test pending dir: %v", err)
	}

	spoolPath := filepath.Join(pendingDir, "owner-repo-17.md")
	content := "---\nverdict: APPROVE\ndecision:\n---\n\nbody\n"
	if err := os.WriteFile(spoolPath, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write test spool file: %v", err)
	}

	got := ReadSpoolInfo(spoolPath, tmp)

	if !got.Found {
		t.Errorf("Found = false, want true")
	}
	if got.InDoneDir {
		t.Errorf("InDoneDir = true, want false")
	}
	if got.Verdict != "APPROVE" {
		t.Errorf("Verdict = %q, want %q", got.Verdict, "APPROVE")
	}
	if got.Decision != "" {
		t.Errorf("Decision = %q, want empty", got.Decision)
	}
	if got.DecisionState != "pending" {
		t.Errorf("DecisionState = %q, want %q", got.DecisionState, "pending")
	}
}

func TestReadSpoolInfoResolvesToDoneWhenMoved(t *testing.T) {
	tmp := t.TempDir()
	pendingDir := filepath.Join(tmp, "pending")
	doneDir := filepath.Join(tmp, "done")
	if err := os.MkdirAll(pendingDir, 0o755); err != nil {
		t.Fatalf("failed to set up test pending dir: %v", err)
	}
	if err := os.MkdirAll(doneDir, 0o755); err != nil {
		t.Fatalf("failed to set up test done dir: %v", err)
	}

	// Record.SpoolPath still points at pending/, but the file itself has
	// been moved to done/ by the external finalize pipeline.
	filename := "owner-repo-17.md"
	spoolPath := filepath.Join(pendingDir, filename)
	donePath := filepath.Join(doneDir, filename)

	content := "---\nverdict: REQUEST_CHANGES\ndecision: post\n---\n\nbody\n"
	if err := os.WriteFile(donePath, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write test done spool file: %v", err)
	}

	got := ReadSpoolInfo(spoolPath, tmp)

	if !got.Found {
		t.Errorf("Found = false, want true")
	}
	if !got.InDoneDir {
		t.Errorf("InDoneDir = false, want true")
	}
	if got.Verdict != "REQUEST_CHANGES" {
		t.Errorf("Verdict = %q, want %q", got.Verdict, "REQUEST_CHANGES")
	}
	// In done/ with decision "post" -> "posted". (Other decisions map to
	// "discarded" / "done: <decision>"; see TestClassifySpoolState.)
	if got.DecisionState != "posted" {
		t.Errorf("DecisionState = %q, want %q", got.DecisionState, "posted")
	}
}

func TestReadSpoolInfoNeitherLocationExists(t *testing.T) {
	tmp := t.TempDir()
	pendingDir := filepath.Join(tmp, "pending")
	doneDir := filepath.Join(tmp, "done")
	if err := os.MkdirAll(pendingDir, 0o755); err != nil {
		t.Fatalf("failed to set up test pending dir: %v", err)
	}
	if err := os.MkdirAll(doneDir, 0o755); err != nil {
		t.Fatalf("failed to set up test done dir: %v", err)
	}

	spoolPath := filepath.Join(pendingDir, "does-not-exist.md")

	got := ReadSpoolInfo(spoolPath, tmp)

	want := SpoolInfo{Found: false, DecisionState: "no spool"}
	if got != want {
		t.Errorf("ReadSpoolInfo() = %#v, want %#v", got, want)
	}
}

func TestReadSpoolInfoUnparsablePendingFileDoesNotErrorOrPanic(t *testing.T) {
	tmp := t.TempDir()
	pendingDir := filepath.Join(tmp, "pending")
	if err := os.MkdirAll(pendingDir, 0o755); err != nil {
		t.Fatalf("failed to set up test pending dir: %v", err)
	}

	spoolPath := filepath.Join(pendingDir, "empty.md")
	if err := os.WriteFile(spoolPath, []byte(""), 0o644); err != nil {
		t.Fatalf("failed to write test spool file: %v", err)
	}

	got := ReadSpoolInfo(spoolPath, tmp)

	if !got.Found {
		t.Errorf("Found = false, want true (file exists, just has no parsable front-matter)")
	}
	if got.InDoneDir {
		t.Errorf("InDoneDir = true, want false")
	}
	if got.Verdict != "" {
		t.Errorf("Verdict = %q, want empty", got.Verdict)
	}
	if got.Decision != "" {
		t.Errorf("Decision = %q, want empty", got.Decision)
	}
	if got.DecisionState != "pending" {
		t.Errorf("DecisionState = %q, want %q", got.DecisionState, "pending")
	}
}

func TestReadSpoolInfoPathWithoutPendingSegmentNotFound(t *testing.T) {
	// A spoolPath with no "pending" segment to swap for "done" should still
	// degrade to "no spool" rather than erroring, when the direct path
	// doesn't exist either.
	tmp := t.TempDir()
	spoolPath := filepath.Join(tmp, "somewhere-else", "file.md")

	got := ReadSpoolInfo(spoolPath, tmp)

	want := SpoolInfo{Found: false, DecisionState: "no spool"}
	if got != want {
		t.Errorf("ReadSpoolInfo() = %#v, want %#v", got, want)
	}
}
