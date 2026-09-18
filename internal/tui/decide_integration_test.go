package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/matthiashowellyopp/howmux/internal/review"
)

// TestDecideIntegration is the end-to-end proof of AC5 (issue #85): both the
// REPL command (`decide post`) and the keypress-driven path (`d`) must
// actually rewrite a real spool file's `decision:` front-matter on disk,
// and the very next ReviewsTab.View() render must reflect that change in
// the DECISION STATE column — not just an in-memory activity line.
//
// Unlike commands_decide_test.go / decide_routing_test.go, this test does
// not fake set-review-decision.sh. It runs the real script shipped at
// .howmux/scripts/set-review-decision.sh (scriptPathFunc resolves that path
// relative to CWD, so t.Chdir into the actual repo root — found by walking
// up from the test's own file location — makes DecisionWriter shell out to
// the genuine script rather than a stand-in).
func TestDecideIntegration(t *testing.T) {
	repoRoot := findRepoRootForTest(t)

	newSpoolFile := func(t *testing.T, dir, name string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		content := "---\n" +
			"repo: owner/repo\n" +
			"pr: 99\n" +
			"verdict: APPROVE\n" +
			"decision:\n" +
			"decision_notes:\n" +
			"diff_file: /abs/path/to.diff\n" +
			"generated: 2026-08-21T11:00Z\n" +
			"---\n\n" +
			"# Review\n\nThis body mentions decision: post as plain text and must never be touched.\n"
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("failed to write test spool file: %v", err)
		}
		return path
	}

	readDecision := func(t *testing.T, path string) string {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("failed to read spool file back: %v", err)
		}
		fields := review.ParseSpoolFrontMatter(data)
		return fields["decision"]
	}

	t.Run("decide post via executeCommand rewrites real spool file", func(t *testing.T) {
		t.Chdir(repoRoot)
		tmp := t.TempDir()
		spoolPath := newSpoolFile(t, tmp, "owner-repo-99.md")

		rec := review.Record{Repo: "owner/repo", PR: 99, SpoolPath: spoolPath}
		store := &fakeReviewStore{records: []review.Record{rec}}
		rt := NewReviewsTab("reviews", store, testReviewsStyles())
		_ = rt.View() // seed selection to the only row

		m := newDecideTestModel()
		m.tabManager.AddTab(rt)

		result, cmd := m.executeCommand("decide post")
		if cmd != nil {
			t.Errorf("expected nil cmd, got %v", cmd)
		}
		if !anyLineContains(result.activityLines, "Set decision on PR #99 to 'post'") {
			t.Fatalf("expected success activity line, got: %v", result.activityLines)
		}

		if got := readDecision(t, spoolPath); got != "post" {
			t.Errorf("decision on disk = %q, want %q", got, "post")
		}

		// Every other front-matter field and the body must be untouched.
		data, err := os.ReadFile(spoolPath)
		if err != nil {
			t.Fatalf("failed to read spool file: %v", err)
		}
		fields := review.ParseSpoolFrontMatter(data)
		if fields["verdict"] != "APPROVE" {
			t.Errorf("verdict = %q, want %q (must be untouched)", fields["verdict"], "APPROVE")
		}
		if fields["diff_file"] != "/abs/path/to.diff" {
			t.Errorf("diff_file = %q, want untouched value", fields["diff_file"])
		}
		if !contains(string(data), "This body mentions decision: post as plain text") {
			t.Errorf("body's decision-like text was altered, got: %s", data)
		}

		// AC5: the rendered View() output now shows the decided state.
		view := rt.View()
		if !contains(view, "decided: post") {
			t.Errorf("expected View() to show 'decided: post' in DECISION STATE column, got:\n%s", view)
		}
	})

	t.Run("keypress d drives model.Update and rewrites real spool file", func(t *testing.T) {
		t.Chdir(repoRoot)
		tmp := t.TempDir()
		spoolPath := newSpoolFile(t, tmp, "owner-repo-100.md")

		rec := review.Record{Repo: "owner/repo", PR: 100, SpoolPath: spoolPath}
		store := &fakeReviewStore{records: []review.Record{rec}}
		rt := NewReviewsTab("reviews", store, testReviewsStyles())
		_ = rt.View() // seed selection to the only row

		m := newDecideRoutingTestModel()
		m.tabManager.AddTab(rt)
		m.input.SetFocus(false) // footer unfocused: AC5b, key reaches the tab

		result, cmd := m.Update(pressKey('d'))
		resultModel, ok := result.(model)
		if !ok {
			t.Fatalf("Update did not return a model")
		}
		if cmd == nil {
			t.Fatalf("expected a tea.Cmd emitting decideRequestMsg, got nil")
		}

		// Drive the emitted decideRequestMsg through Update, mirroring the
		// real Bubble Tea event loop (the cmd's message is delivered on the
		// subsequent Update call, not synchronously within this one).
		msg := cmd()
		finalResult, finalCmd := resultModel.Update(msg)
		if finalCmd != nil {
			t.Errorf("expected nil cmd from decideRequestMsg handling, got %v", finalCmd)
		}
		finalModel, ok := finalResult.(model)
		if !ok {
			t.Fatalf("Update did not return a model")
		}

		if !anyLineContains(finalModel.activityLines, "Set decision on PR #100 to 'discard'") {
			t.Fatalf("expected success activity line, got: %v", finalModel.activityLines)
		}

		if got := readDecision(t, spoolPath); got != "discard" {
			t.Errorf("decision on disk = %q, want %q", got, "discard")
		}

		// AC5: the rendered View() output now shows the discarded state.
		view := rt.View()
		if !contains(view, "decided: discard") {
			t.Errorf("expected View() to show 'decided: discard' in DECISION STATE column, got:\n%s", view)
		}
	})

	t.Run("invalid decision value leaves file unmodified and produces an error line", func(t *testing.T) {
		t.Chdir(repoRoot)
		tmp := t.TempDir()
		spoolPath := newSpoolFile(t, tmp, "owner-repo-101.md")

		before, err := os.ReadFile(spoolPath)
		if err != nil {
			t.Fatalf("failed to read spool file before: %v", err)
		}

		rec := review.Record{Repo: "owner/repo", PR: 101, SpoolPath: spoolPath}
		store := &fakeReviewStore{records: []review.Record{rec}}
		rt := NewReviewsTab("reviews", store, testReviewsStyles())
		_ = rt.View()

		m := newDecideTestModel()
		m.tabManager.AddTab(rt)

		result, cmd := m.executeCommand("decide bogus")
		if cmd != nil {
			t.Errorf("expected nil cmd, got %v", cmd)
		}
		if !anyLineContains(result.activityLines, "Invalid decision: bogus (must be post, revise, rereview, or discard)") {
			t.Fatalf("expected invalid-vocabulary error line, got: %v", result.activityLines)
		}

		after, err := os.ReadFile(spoolPath)
		if err != nil {
			t.Fatalf("failed to read spool file after: %v", err)
		}
		if string(before) != string(after) {
			t.Errorf("spool file was modified by an invalid decision value:\nbefore:\n%s\nafter:\n%s", before, after)
		}
	})

	t.Run("no row selected produces an error line and the script is never invoked", func(t *testing.T) {
		t.Chdir(repoRoot)

		// Empty store: nothing to select, and thus nothing that could resolve
		// to a spool path — the script must never be invoked. There is no
		// spool file at all in this case, so "never invoked" is verified by
		// there being no file for the (nonexistent) script to have acted on,
		// combined with the specific no-selection error line below.
		store := &fakeReviewStore{records: nil}
		rt := NewReviewsTab("reviews", store, testReviewsStyles())
		_ = rt.View()

		m := newDecideTestModel()
		m.tabManager.AddTab(rt)

		result, cmd := m.executeCommand("decide post")
		if cmd != nil {
			t.Errorf("expected nil cmd, got %v", cmd)
		}
		if !anyLineContains(result.activityLines, "No review selected — switch to the Reviews tab and select a row first") {
			t.Fatalf("expected no-selection error line, got: %v", result.activityLines)
		}
		// Exactly one activity line: no success/error-from-writer line was
		// also appended, confirming the writer path was never reached.
		if len(result.activityLines) != 1 {
			t.Errorf("expected exactly one activity line (writer never invoked), got: %v", result.activityLines)
		}
	})
}

// findRepoRootForTest walks up from the current working directory (the
// package directory, per Go's test-execution convention) to find the repo
// root — identified by the presence of .howmux/scripts/set-review-decision.sh
// — so this test can t.Chdir there and exercise the real script rather than
// a fake. Fails the test if no such ancestor is found, rather than silently
// running against the wrong script.
func findRepoRootForTest(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	for {
		candidate := filepath.Join(dir, ".howmux", "scripts", "set-review-decision.sh")
		if _, err := os.Stat(candidate); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not locate .howmux/scripts/set-review-decision.sh in any ancestor of %s", dir)
		}
		dir = parent
	}
}

// contains is already defined in commands_review_test.go (same package);
// reused here rather than redeclared.
