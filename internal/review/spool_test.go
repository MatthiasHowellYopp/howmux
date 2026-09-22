package review

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// --- ReadSpoolBody ---
//
// These tests extend coverage to the new body-reading function added
// alongside the resolveSpoolPath refactor. They mirror the fixture setup of
// the equivalent TestReadSpoolInfo* tests above wherever a direct analogue
// exists, so the two functions' resolution behavior can be visually compared
// test-by-test.

func TestReadSpoolBodyPendingFileReturnsBodyAfterFrontMatter(t *testing.T) {
	tmp := t.TempDir()
	pendingDir := filepath.Join(tmp, "pending")
	if err := os.MkdirAll(pendingDir, 0o755); err != nil {
		t.Fatalf("failed to set up test pending dir: %v", err)
	}

	spoolPath := filepath.Join(pendingDir, "owner-repo-17.md")
	content := "---\nverdict: APPROVE\ndecision:\n---\n\n# Review\n\nbody text here\n"
	if err := os.WriteFile(spoolPath, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write test spool file: %v", err)
	}

	body, info := ReadSpoolBody(spoolPath, tmp)

	if !info.Found {
		t.Errorf("info.Found = false, want true")
	}
	if info.InDoneDir {
		t.Errorf("info.InDoneDir = true, want false")
	}
	if info.Verdict != "APPROVE" {
		t.Errorf("info.Verdict = %q, want %q", info.Verdict, "APPROVE")
	}

	wantBody := "# Review\n\nbody text here\n"
	if body != wantBody {
		t.Errorf("body = %q, want %q", body, wantBody)
	}
}

func TestReadSpoolBodyFallsBackToDoneDir(t *testing.T) {
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

	content := "---\nverdict: REQUEST_CHANGES\ndecision: post\n---\n\nbody in done dir\n"
	if err := os.WriteFile(donePath, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write test done spool file: %v", err)
	}

	body, info := ReadSpoolBody(spoolPath, tmp)

	if !info.Found {
		t.Errorf("info.Found = false, want true")
	}
	if !info.InDoneDir {
		t.Errorf("info.InDoneDir = false, want true")
	}

	wantBody := "body in done dir\n"
	if body != wantBody {
		t.Errorf("body = %q, want %q", body, wantBody)
	}
}

func TestReadSpoolBodyMissingFileReturnsNotFound(t *testing.T) {
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

	body, info := ReadSpoolBody(spoolPath, tmp)

	if body != "" {
		t.Errorf("body = %q, want empty", body)
	}
	want := SpoolInfo{Found: false, DecisionState: "no spool"}
	if info != want {
		t.Errorf("info = %#v, want %#v", info, want)
	}
}

func TestReadSpoolBodyEmptySpoolPathReturnsNotFoundWithoutFilesystemAccess(t *testing.T) {
	// spoolPath == "" must short-circuit with zero filesystem calls, mirroring
	// TestReadSpoolInfoEmptyPath. The nonsensical homeDir helps show (in
	// spirit) that no I/O occurs; the real guarantee is that this test never
	// creates any files on disk.
	body, info := ReadSpoolBody("", "/nonexistent/should/never/be/touched")

	if body != "" {
		t.Errorf("body = %q, want empty", body)
	}
	want := SpoolInfo{Found: false, DecisionState: "no spool"}
	if info != want {
		t.Errorf("info = %#v, want %#v", info, want)
	}
}

func TestReadSpoolBodyNoFrontMatterFenceReturnsWholeFileAsBody(t *testing.T) {
	tmp := t.TempDir()
	pendingDir := filepath.Join(tmp, "pending")
	if err := os.MkdirAll(pendingDir, 0o755); err != nil {
		t.Fatalf("failed to set up test pending dir: %v", err)
	}

	spoolPath := filepath.Join(pendingDir, "legacy.md")
	content := "This is a legacy spool file with no front-matter fence at all.\n"
	if err := os.WriteFile(spoolPath, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write test spool file: %v", err)
	}

	body, info := ReadSpoolBody(spoolPath, tmp)

	if body != content {
		t.Errorf("body = %q, want %q (whole file, no fence present)", body, content)
	}
	if !info.Found {
		t.Errorf("info.Found = false, want true (file exists, just has no parsable front-matter)")
	}
	if info.Verdict != "" {
		t.Errorf("info.Verdict = %q, want empty", info.Verdict)
	}
	if info.Decision != "" {
		t.Errorf("info.Decision = %q, want empty", info.Decision)
	}
	if info.DecisionState != "pending" {
		t.Errorf("info.DecisionState = %q, want %q", info.DecisionState, "pending")
	}
}

func TestReadSpoolBodyTrimsSingleLeadingNewlineAfterFence(t *testing.T) {
	tmp := t.TempDir()
	pendingDir := filepath.Join(tmp, "pending")
	if err := os.MkdirAll(pendingDir, 0o755); err != nil {
		t.Fatalf("failed to set up test pending dir: %v", err)
	}

	spoolPath := filepath.Join(pendingDir, "owner-repo-17.md")
	content := "---\nverdict: APPROVE\n---\n\n# Review\nmore body\n"
	if err := os.WriteFile(spoolPath, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write test spool file: %v", err)
	}

	body, _ := ReadSpoolBody(spoolPath, tmp)

	if !strings.HasPrefix(body, "# Review") {
		t.Errorf("body = %q, want prefix %q (leading blank line after fence trimmed)", body, "# Review")
	}
	if strings.HasPrefix(body, "\n") {
		t.Errorf("body = %q, should not start with a leading newline", body)
	}
}

func TestReadSpoolBodyUnreadableFileDegradesToNotFound(t *testing.T) {
	tmp := t.TempDir()
	pendingDir := filepath.Join(tmp, "pending")
	if err := os.MkdirAll(pendingDir, 0o755); err != nil {
		t.Fatalf("failed to set up test pending dir: %v", err)
	}

	// A directory at the expected spool path: os.Stat succeeds (so
	// resolveSpoolPath reports found=true), but os.ReadFile fails because
	// it's a directory, not a regular file — a deterministic way to hit the
	// "found but unreadable" branch without relying on permission bits,
	// which behave inconsistently across platforms/CI (e.g. root).
	spoolPath := filepath.Join(pendingDir, "actually-a-dir.md")
	if err := os.MkdirAll(spoolPath, 0o755); err != nil {
		t.Fatalf("failed to set up directory-as-spool-path fixture: %v", err)
	}

	var body string
	var info SpoolInfo
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("ReadSpoolBody panicked on unreadable file: %v", r)
			}
		}()
		body, info = ReadSpoolBody(spoolPath, tmp)
	}()

	if body != "" {
		t.Errorf("body = %q, want empty", body)
	}
	if info.Found {
		t.Errorf("info.Found = true, want false (unreadable file should degrade gracefully)")
	}
	if info.DecisionState != "no spool" {
		t.Errorf("info.DecisionState = %q, want %q", info.DecisionState, "no spool")
	}
}

// --- patchFrontMatterMetadata ---

func TestPatchFrontMatterMetadata(t *testing.T) {
	const bodyWithHorizontalRule = `
Review body starts here.

Some more text.

---

Text after a markdown horizontal rule inside the body.
`

	tests := []struct {
		name        string
		data        string
		headSHA     string
		generated   string
		wantErr     bool
		wantFields  map[string]string
		wantBodyHas string // substring the output must contain verbatim, checked when non-empty
	}{
		{
			name: "blank generated, no reviewed_sha (pr_review.py's current output)",
			data: `---
repo: owner/repo
pr: 17
verdict: APPROVE
decision:
decision_notes:
diff_file: /abs/path/to.diff
generated:
---

review body here
`,
			headSHA:   "abc1234",
			generated: "2026-09-22T10:00:00Z",
			wantFields: map[string]string{
				"repo":           "owner/repo",
				"pr":             "17",
				"verdict":        "APPROVE",
				"decision":       "",
				"decision_notes": "",
				"diff_file":      "/abs/path/to.diff",
				"generated":      "2026-09-22T10:00:00Z",
				"reviewed_sha":   "abc1234",
			},
			wantBodyHas: "review body here\n",
		},
		{
			name: "both keys already present and non-blank (re-review case)",
			data: `---
repo: owner/repo
pr: 17
verdict: APPROVE
generated: 2026-08-21T11:00:00Z
reviewed_sha: oldsha0000
---

review body here
`,
			headSHA:   "newsha1111",
			generated: "2026-09-22T10:00:00Z",
			wantFields: map[string]string{
				"repo":         "owner/repo",
				"pr":           "17",
				"verdict":      "APPROVE",
				"generated":    "2026-09-22T10:00:00Z",
				"reviewed_sha": "newsha1111",
			},
			wantBodyHas: "review body here\n",
		},
		{
			name:    "no opening fence returns an error",
			data:    "repo: owner/repo\npr: 17\n",
			headSHA: "abc1234",
			wantErr: true,
		},
		{
			name:    "opening fence with no closing fence returns an error",
			data:    "---\nrepo: owner/repo\npr: 17\n",
			headSHA: "abc1234",
			wantErr: true,
		},
		{
			name: "body content and body-only blank lines/horizontal-rule preserved exactly",
			data: `---
repo: owner/repo
pr: 17
generated:
---
` + bodyWithHorizontalRule,
			headSHA:   "abc1234",
			generated: "2026-09-22T10:00:00Z",
			wantFields: map[string]string{
				"repo":         "owner/repo",
				"pr":           "17",
				"generated":    "2026-09-22T10:00:00Z",
				"reviewed_sha": "abc1234",
			},
			wantBodyHas: bodyWithHorizontalRule,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := patchFrontMatterMetadata([]byte(tt.data), tt.headSHA, tt.generated)

			if tt.wantErr {
				if err == nil {
					t.Fatalf("patchFrontMatterMetadata() error = nil, want non-nil error")
				}
				return
			}
			if err != nil {
				t.Fatalf("patchFrontMatterMetadata() unexpected error: %v", err)
			}

			fields := ParseSpoolFrontMatter(got)
			for k, v := range tt.wantFields {
				if fields[k] != v {
					t.Errorf("ParseSpoolFrontMatter(output)[%q] = %q, want %q", k, fields[k], v)
				}
			}

			if tt.wantBodyHas != "" && !strings.Contains(string(got), tt.wantBodyHas) {
				t.Errorf("output does not contain expected body substring.\noutput: %q\nwant substring: %q", got, tt.wantBodyHas)
			}

			// Case-specific extra assertions.
			switch tt.name {
			case "both keys already present and non-blank (re-review case)":
				generatedCount := strings.Count(string(got), "generated:")
				if generatedCount != 1 {
					t.Errorf("expected exactly one %q line, got %d", "generated:", generatedCount)
				}
				shaCount := strings.Count(string(got), "reviewed_sha:")
				if shaCount != 1 {
					t.Errorf("expected exactly one %q line, got %d", "reviewed_sha:", shaCount)
				}

				origFields := ParseSpoolFrontMatter([]byte(tt.data))
				if len(fields) != len(origFields) {
					t.Errorf("patched field count = %d, want %d (no duplicate keys introduced); orig=%#v got=%#v", len(fields), len(origFields), origFields, fields)
				}
			}
		})
	}
}

// --- WriteReviewedMetadata ---

const writeReviewedMetadataFixture = `---
repo: owner/repo
pr: 1
verdict: APPROVE
decision:
decision_notes:
diff_file: /abs/path/to.diff
generated:
---

review body here
`

func TestWriteReviewedMetadata(t *testing.T) {
	t.Run("happy path: pending file gets both fields written to disk", func(t *testing.T) {
		pendingDir := filepath.Join(t.TempDir(), "pending")
		if err := os.MkdirAll(pendingDir, 0o755); err != nil {
			t.Fatalf("failed to set up test pending dir: %v", err)
		}
		spoolPath := filepath.Join(pendingDir, "pr-review-owner-repo-1.md")
		if err := os.WriteFile(spoolPath, []byte(writeReviewedMetadataFixture), 0o644); err != nil {
			t.Fatalf("failed to write test spool fixture: %v", err)
		}

		const knownSHA = "deadbeef123"
		knownTime := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)

		if err := WriteReviewedMetadata(spoolPath, knownSHA, knownTime); err != nil {
			t.Fatalf("WriteReviewedMetadata() unexpected error: %v", err)
		}

		data, err := os.ReadFile(spoolPath)
		if err != nil {
			t.Fatalf("failed to re-read spool file: %v", err)
		}

		fields := ParseSpoolFrontMatter(data)
		if fields["reviewed_sha"] != knownSHA {
			t.Errorf("reviewed_sha = %q, want %q", fields["reviewed_sha"], knownSHA)
		}
		wantGenerated := knownTime.Format(time.RFC3339)
		if fields["generated"] != wantGenerated {
			t.Errorf("generated = %q, want %q", fields["generated"], wantGenerated)
		}
	})

	t.Run("preserves the original file mode (atomic write via temp+rename)", func(t *testing.T) {
		pendingDir := filepath.Join(t.TempDir(), "pending")
		if err := os.MkdirAll(pendingDir, 0o755); err != nil {
			t.Fatalf("failed to set up test pending dir: %v", err)
		}
		spoolPath := filepath.Join(pendingDir, "pr-review-owner-repo-2.md")
		// Seed with a non-default mode so a hardcoded 0644 write would be
		// detectable. (This is what the old in-place os.WriteFile(_, 0644)
		// regressed relative to the set-review-*.sh scripts' `cp -p`.)
		const wantMode = os.FileMode(0o600)
		if err := os.WriteFile(spoolPath, []byte(writeReviewedMetadataFixture), wantMode); err != nil {
			t.Fatalf("failed to write test spool fixture: %v", err)
		}
		// Guard against a restrictive umask having masked the seed write.
		if err := os.Chmod(spoolPath, wantMode); err != nil {
			t.Fatalf("failed to chmod test spool fixture: %v", err)
		}

		if err := WriteReviewedMetadata(spoolPath, "cafef00d", time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)); err != nil {
			t.Fatalf("WriteReviewedMetadata() unexpected error: %v", err)
		}

		info, err := os.Stat(spoolPath)
		if err != nil {
			t.Fatalf("failed to stat spool file after write: %v", err)
		}
		if info.Mode().Perm() != wantMode {
			t.Errorf("file mode after write = %v, want %v (original mode must be preserved)", info.Mode().Perm(), wantMode)
		}

		// No stray temp files should remain in the directory.
		entries, err := os.ReadDir(pendingDir)
		if err != nil {
			t.Fatalf("failed to read pending dir: %v", err)
		}
		for _, e := range entries {
			if strings.Contains(e.Name(), ".tmp") {
				t.Errorf("stray temp file left behind after write: %s", e.Name())
			}
		}
	})

	t.Run("empty spoolPath returns an error", func(t *testing.T) {
		err := WriteReviewedMetadata("", "abc1234", time.Now())
		if err == nil {
			t.Fatalf("WriteReviewedMetadata(\"\", ...) error = nil, want non-nil error")
		}
	})

	t.Run("empty headSHA returns an error", func(t *testing.T) {
		pendingDir := filepath.Join(t.TempDir(), "pending")
		if err := os.MkdirAll(pendingDir, 0o755); err != nil {
			t.Fatalf("failed to set up test pending dir: %v", err)
		}
		spoolPath := filepath.Join(pendingDir, "pr-review-owner-repo-1.md")
		if err := os.WriteFile(spoolPath, []byte(writeReviewedMetadataFixture), 0o644); err != nil {
			t.Fatalf("failed to write test spool fixture: %v", err)
		}

		err := WriteReviewedMetadata(spoolPath, "", time.Now())
		if err == nil {
			t.Fatalf("WriteReviewedMetadata(path, \"\", ...) error = nil, want non-nil error")
		}
	})

	t.Run("nonexistent spool file returns an error mentioning no spool file", func(t *testing.T) {
		pendingDir := filepath.Join(t.TempDir(), "pending")
		if err := os.MkdirAll(pendingDir, 0o755); err != nil {
			t.Fatalf("failed to set up test pending dir: %v", err)
		}
		spoolPath := filepath.Join(pendingDir, "does-not-exist.md")

		err := WriteReviewedMetadata(spoolPath, "abc1234", time.Now())
		if err == nil {
			t.Fatalf("WriteReviewedMetadata() error = nil, want non-nil error")
		}
		if !strings.Contains(err.Error(), "no spool file") {
			t.Errorf("error = %q, want it to mention %q", err.Error(), "no spool file")
		}
	})

	t.Run("file already in done/ is refused without modifying it", func(t *testing.T) {
		tmp := t.TempDir()
		pendingDir := filepath.Join(tmp, "pending")
		doneDir := filepath.Join(tmp, "done")
		if err := os.MkdirAll(pendingDir, 0o755); err != nil {
			t.Fatalf("failed to set up test pending dir: %v", err)
		}
		if err := os.MkdirAll(doneDir, 0o755); err != nil {
			t.Fatalf("failed to set up test done dir: %v", err)
		}

		// Record.SpoolPath always points at the pending/ location; the file
		// itself has already been moved to done/ by the external finalize
		// pipeline. resolveSpoolPath falls back pending -> done, mirroring
		// the ReadSpoolInfo tests elsewhere in this file.
		filename := "pr-review-owner-repo-1.md"
		spoolPath := filepath.Join(pendingDir, filename)
		donePath := filepath.Join(doneDir, filename)
		if err := os.WriteFile(donePath, []byte(writeReviewedMetadataFixture), 0o644); err != nil {
			t.Fatalf("failed to write test spool fixture: %v", err)
		}

		err := WriteReviewedMetadata(spoolPath, "abc1234", time.Now())
		if err == nil {
			t.Fatalf("WriteReviewedMetadata() error = nil, want non-nil error")
		}
		if !strings.Contains(err.Error(), "already finalized") {
			t.Errorf("error = %q, want it to mention %q", err.Error(), "already finalized")
		}

		data, err := os.ReadFile(donePath)
		if err != nil {
			t.Fatalf("failed to re-read spool file: %v", err)
		}
		if string(data) != writeReviewedMetadataFixture {
			t.Errorf("file bytes changed after refused write.\ngot:  %q\nwant: %q", data, writeReviewedMetadataFixture)
		}
	})
}

// --- swapPendingDone ---

func TestSwapPendingDone(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		toDone bool
		want   string
	}{
		{
			name:   "pending to done swaps the segment",
			path:   filepath.Join("home", "user", "PR-Review", "pending", "pr-review-owner-repo-1.md"),
			toDone: true,
			want:   filepath.Join("home", "user", "PR-Review", "done", "pr-review-owner-repo-1.md"),
		},
		{
			name:   "done to pending swaps the segment back",
			path:   filepath.Join("home", "user", "PR-Review", "done", "pr-review-owner-repo-1.md"),
			toDone: false,
			want:   filepath.Join("home", "user", "PR-Review", "pending", "pr-review-owner-repo-1.md"),
		},
		{
			name:   "no pending segment present, toDone true, returns empty",
			path:   filepath.Join("home", "user", "PR-Review", "done", "pr-review-owner-repo-1.md"),
			toDone: true,
			want:   "",
		},
		{
			name:   "no done segment present, toDone false, returns empty",
			path:   filepath.Join("home", "user", "PR-Review", "pending", "pr-review-owner-repo-1.md"),
			toDone: false,
			want:   "",
		},
		{
			name:   "empty path returns empty",
			path:   "",
			toDone: true,
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := swapPendingDone(tt.path, tt.toDone)
			if got != tt.want {
				t.Errorf("swapPendingDone(%q, %v) = %q, want %q", tt.path, tt.toDone, got, tt.want)
			}
		})
	}
}

func TestDerivePendingToDoneDelegatesToSwapPendingDone(t *testing.T) {
	path := filepath.Join("home", "user", "PR-Review", "pending", "pr-review-owner-repo-1.md")
	want := swapPendingDone(path, true)
	got := derivePendingToDone(path)
	if got != want {
		t.Errorf("derivePendingToDone(%q) = %q, want %q (should delegate to swapPendingDone(path, true))", path, got, want)
	}
}

// --- RewriteSpoolEntry ---

const rewriteSpoolFixture = `---
repo: owner/repo
pr: 1
verdict: COMMENT
decision: revise
decision_notes: please double check the auth flow
diff_file: /abs/path/to.diff
generated: 2026-09-22T10:00:00Z
---

Old review body here.
`

func TestRewriteSpoolEntry_ReplacesBodyAndVerdict(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pending")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir pending: %v", err)
	}
	spoolPath := filepath.Join(dir, "pr-review-owner-repo-1.md")
	if err := os.WriteFile(spoolPath, []byte(rewriteSpoolFixture), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	newBody := "# Consolidated Review\n\nsrc/foo.py:10 - warning - bad thing -> fix it\nVERDICT: APPROVE\n"
	if err := RewriteSpoolEntry(spoolPath, newBody, "APPROVE", false); err != nil {
		t.Fatalf("RewriteSpoolEntry() unexpected error: %v", err)
	}

	data, err := os.ReadFile(spoolPath)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	fields := ParseSpoolFrontMatter(data)
	if fields["verdict"] != "APPROVE" {
		t.Errorf("verdict = %q, want %q", fields["verdict"], "APPROVE")
	}
	gotBody := extractSpoolBody(data)
	if !strings.Contains(gotBody, "src/foo.py:10 - warning - bad thing -> fix it") {
		t.Errorf("body = %q, want it to contain the new finding line", gotBody)
	}
	if strings.Contains(gotBody, "Old review body here.") {
		t.Errorf("body still contains old content: %q", gotBody)
	}
}

func TestRewriteSpoolEntry_ClearsDecision(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pending")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir pending: %v", err)
	}
	spoolPath := filepath.Join(dir, "pr-review-owner-repo-1.md")
	if err := os.WriteFile(spoolPath, []byte(rewriteSpoolFixture), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	if err := RewriteSpoolEntry(spoolPath, "new body", "APPROVE", true); err != nil {
		t.Fatalf("RewriteSpoolEntry() unexpected error: %v", err)
	}

	data, err := os.ReadFile(spoolPath)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	fields := ParseSpoolFrontMatter(data)
	if fields["decision"] != "" {
		t.Errorf("decision = %q, want empty after clearDecision=true", fields["decision"])
	}
	// decision_notes must be left as-is (only decision is touched).
	if fields["decision_notes"] != "please double check the auth flow" {
		t.Errorf("decision_notes = %q, want it preserved untouched", fields["decision_notes"])
	}
}

func TestRewriteSpoolEntry_PreservesOtherFrontMatterKeys(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pending")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir pending: %v", err)
	}
	spoolPath := filepath.Join(dir, "pr-review-owner-repo-1.md")
	if err := os.WriteFile(spoolPath, []byte(rewriteSpoolFixture), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	if err := RewriteSpoolEntry(spoolPath, "new body", "REQUEST_CHANGES", false); err != nil {
		t.Fatalf("RewriteSpoolEntry() unexpected error: %v", err)
	}

	data, err := os.ReadFile(spoolPath)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	fields := ParseSpoolFrontMatter(data)
	want := map[string]string{
		"repo":      "owner/repo",
		"pr":        "1",
		"diff_file": "/abs/path/to.diff",
		"generated": "2026-09-22T10:00:00Z",
	}
	for k, v := range want {
		if fields[k] != v {
			t.Errorf("fields[%q] = %q, want %q (untouched keys must survive)", k, fields[k], v)
		}
	}
	// decision was left alone (clearDecision=false), so its original value survives.
	if fields["decision"] != "revise" {
		t.Errorf("decision = %q, want %q (clearDecision=false must not touch it)", fields["decision"], "revise")
	}
}

func TestRewriteSpoolEntry_NoOpeningFence_ReturnsError(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pending")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir pending: %v", err)
	}
	spoolPath := filepath.Join(dir, "pr-review-owner-repo-1.md")
	if err := os.WriteFile(spoolPath, []byte("no front matter here\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	err := RewriteSpoolEntry(spoolPath, "new body", "APPROVE", false)
	if err == nil {
		t.Fatal("RewriteSpoolEntry() error = nil, want non-nil error for missing opening fence")
	}
}

func TestRewriteSpoolEntry_NoClosingFence_ReturnsError(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pending")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir pending: %v", err)
	}
	spoolPath := filepath.Join(dir, "pr-review-owner-repo-1.md")
	if err := os.WriteFile(spoolPath, []byte("---\nrepo: owner/repo\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	err := RewriteSpoolEntry(spoolPath, "new body", "APPROVE", false)
	if err == nil {
		t.Fatal("RewriteSpoolEntry() error = nil, want non-nil error for missing closing fence")
	}
}

func TestRewriteSpoolEntry_EmptySpoolPath_ReturnsError(t *testing.T) {
	err := RewriteSpoolEntry("", "new body", "APPROVE", false)
	if err == nil {
		t.Fatal("RewriteSpoolEntry(\"\", ...) error = nil, want non-nil error")
	}
}

// --- archiveToDone ---

func TestArchiveToDone_MovesFile(t *testing.T) {
	base := t.TempDir()
	pendingDir := filepath.Join(base, "pending")
	if err := os.MkdirAll(pendingDir, 0o755); err != nil {
		t.Fatalf("mkdir pending: %v", err)
	}
	spoolPath := filepath.Join(pendingDir, "pr-review-owner-repo-1.md")
	if err := os.WriteFile(spoolPath, []byte(rewriteSpoolFixture), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	if err := archiveToDone(spoolPath); err != nil {
		t.Fatalf("archiveToDone() unexpected error: %v", err)
	}

	if _, err := os.Stat(spoolPath); !os.IsNotExist(err) {
		t.Errorf("expected source file to no longer exist at %s, stat err = %v", spoolPath, err)
	}

	donePath := filepath.Join(base, "done", "pr-review-owner-repo-1.md")
	if _, err := os.Stat(donePath); err != nil {
		t.Errorf("expected archived file at %s, stat err = %v", donePath, err)
	}
}

func TestArchiveToDone_CreatesDoneDirIfMissing(t *testing.T) {
	base := t.TempDir()
	pendingDir := filepath.Join(base, "pending")
	if err := os.MkdirAll(pendingDir, 0o755); err != nil {
		t.Fatalf("mkdir pending: %v", err)
	}
	spoolPath := filepath.Join(pendingDir, "pr-review-owner-repo-1.md")
	if err := os.WriteFile(spoolPath, []byte(rewriteSpoolFixture), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	doneDir := filepath.Join(base, "done")
	if _, err := os.Stat(doneDir); !os.IsNotExist(err) {
		t.Fatalf("expected done/ to not exist yet, stat err = %v", err)
	}

	if err := archiveToDone(spoolPath); err != nil {
		t.Fatalf("archiveToDone() unexpected error: %v", err)
	}

	if _, err := os.Stat(doneDir); err != nil {
		t.Errorf("expected done/ dir to be created, stat err = %v", err)
	}
}

func TestArchiveToDone_SourceMissing_ReturnsError(t *testing.T) {
	base := t.TempDir()
	spoolPath := filepath.Join(base, "pending", "pr-review-owner-repo-1.md")

	err := archiveToDone(spoolPath)
	if err == nil {
		t.Fatal("archiveToDone() error = nil, want non-nil error for missing source file")
	}
}

func TestArchiveToDone_EmptyPath_ReturnsError(t *testing.T) {
	err := archiveToDone("")
	if err == nil {
		t.Fatal("archiveToDone(\"\") error = nil, want non-nil error")
	}
}

func TestArchiveToDone_NoPendingSegment_ReturnsError(t *testing.T) {
	base := t.TempDir()
	spoolPath := filepath.Join(base, "somewhere-else", "pr-review-owner-repo-1.md")
	if err := os.MkdirAll(filepath.Dir(spoolPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(spoolPath, []byte(rewriteSpoolFixture), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	err := archiveToDone(spoolPath)
	if err == nil {
		t.Fatal("archiveToDone() error = nil, want non-nil error when no \"pending\" segment is present")
	}
}
