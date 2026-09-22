package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/matthiashowellyopp/howmux/internal/review"
)

// TestDecideIntegration is the end-to-end proof that both the REPL command
// (`decide discard`) and the keypress-driven path (`d`) actually rewrite a
// real spool file on disk, and the very next ReviewsTab.View() render
// reflects that change in the DECISION STATE column — not just an
// in-memory activity line. discard is used (rather than post/revise/
// rereview) because it is the only action that touches the real spool file
// without any internal/review subprocess seam substitution being required
// (DiscardReview shells out to nothing).
func TestDecideIntegration(t *testing.T) {
	repoRoot := findRepoRootForTest(t)

	newSpoolFile := func(t *testing.T, dir, name string) string {
		t.Helper()
		path := filepath.Join(dir, "pending", name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("failed to create pending dir: %v", err)
		}
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

	t.Run("decide discard via executeCommand archives the real spool file", func(t *testing.T) {
		t.Chdir(repoRoot)
		tmp := t.TempDir()
		spoolPath := newSpoolFile(t, tmp, "owner-repo-99.md")

		rec := review.Record{Repo: "owner/repo", PR: 99, SpoolPath: spoolPath}
		store := &fakeReviewStore{records: []review.Record{rec}}
		rt := NewReviewsTab("reviews", store, testReviewsStyles())
		_ = rt.View() // seed selection to the only row

		m := newDecideTestModel()
		m.tabManager.AddTab(rt)

		result, cmd := m.executeCommand("decide discard")
		if cmd == nil {
			t.Fatalf("expected a non-nil tea.Cmd for the discard launch, got nil")
		}

		completeMsg := drainBatchForType[decideDiscardCompleteMsg](t, cmd)
		updated, finalCmd := result.Update(completeMsg)
		if finalCmd != nil {
			t.Errorf("expected nil cmd from decideDiscardCompleteMsg handling, got %v", finalCmd)
		}
		finalModel, ok := updated.(model)
		if !ok {
			t.Fatalf("Update did not return a model")
		}

		if !anyLineContains(finalModel.activityLines, "Discarded review for owner/repo#99 — archived to done/.") {
			t.Fatalf("expected success activity line, got: %v", finalModel.activityLines)
		}

		donePath := filepath.Join(tmp, "done", "owner-repo-99.md")
		if _, err := os.Stat(spoolPath); !os.IsNotExist(err) {
			t.Errorf("expected spool file to no longer exist at pending/ path %s", spoolPath)
		}
		data, err := os.ReadFile(donePath)
		if err != nil {
			t.Fatalf("expected spool file to have been archived to %s: %v", donePath, err)
		}

		// Every front-matter field (other than decision) and the body must
		// be untouched.
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
		if fields["decision"] != "discard" {
			t.Errorf("decision on disk = %q, want %q", fields["decision"], "discard")
		}

		// AC5: the rendered View() output now shows the discarded state.
		view := rt.View()
		if !contains(view, "discarded") {
			t.Errorf("expected View() to show 'discarded' in DECISION STATE column, got:\n%s", view)
		}
	})

	t.Run("keypress d drives model.Update and archives the real spool file", func(t *testing.T) {
		t.Chdir(repoRoot)
		tmp := t.TempDir()
		spoolPath := newSpoolFile(t, tmp, "owner-repo-100.md")

		rec := review.Record{Repo: "owner/repo", PR: 100, SpoolPath: spoolPath}
		store := &fakeReviewStore{records: []review.Record{rec}}
		rt := NewReviewsTab("reviews", store, testReviewsStyles())
		_ = rt.View() // seed selection to the only row

		m := newDecideRoutingTestModel()
		m.tabManager.AddTab(rt)
		m.input.SetFocus(false) // footer unfocused: key reaches the tab

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
		afterRequest, launchCmd := resultModel.Update(msg)
		if launchCmd == nil {
			t.Fatalf("expected a non-nil tea.Cmd for the discard launch, got nil")
		}
		afterRequestModel, ok := afterRequest.(model)
		if !ok {
			t.Fatalf("Update did not return a model")
		}

		completeMsg := drainBatchForType[decideDiscardCompleteMsg](t, launchCmd)
		finalResult, finalCmd := afterRequestModel.Update(completeMsg)
		if finalCmd != nil {
			t.Errorf("expected nil cmd from decideDiscardCompleteMsg handling, got %v", finalCmd)
		}
		finalModel, ok := finalResult.(model)
		if !ok {
			t.Fatalf("Update did not return a model")
		}

		if !anyLineContains(finalModel.activityLines, "Discarded review for owner/repo#100 — archived to done/.") {
			t.Fatalf("expected success activity line, got: %v", finalModel.activityLines)
		}

		if got := readDecisionAtPath(t, filepath.Join(tmp, "done", "owner-repo-100.md")); got != "discard" {
			t.Errorf("decision on disk = %q, want %q", got, "discard")
		}

		// AC5: the rendered View() output now shows the discarded state.
		view := rt.View()
		if !contains(view, "discarded") {
			t.Errorf("expected View() to show 'discarded' in DECISION STATE column, got:\n%s", view)
		}
	})

	t.Run("invalid decision value leaves file unmodified and produces an error line", func(t *testing.T) {
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

	t.Run("no row selected produces an error line and nothing is invoked", func(t *testing.T) {
		// Empty store: nothing to select, and thus nothing that could resolve
		// to a spool path — no action must be launched.
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
		// Exactly one activity line: no confirm-prompt/success/error line
		// was also appended, confirming the dispatch path was never reached.
		if len(result.activityLines) != 1 {
			t.Errorf("expected exactly one activity line (dispatch never invoked), got: %v", result.activityLines)
		}
	})
}

// readDecisionAtPath reads the "decision" front-matter field from the
// spool file at path.
func readDecisionAtPath(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read spool file back: %v", err)
	}
	fields := review.ParseSpoolFrontMatter(data)
	return fields["decision"]
}

// --- Task 9: decide post confirm-gate + immediate-launch integration tests ---

// newPostSpoolFixture writes a minimal, well-formed spool file under
// dir/pending/<name> with the given decision (usually "") and returns its
// path. Shared by the confirm-gate tests below.
func newPostSpoolFixture(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, "pending", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("failed to create pending dir: %v", err)
	}
	content := "---\n" +
		"repo: owner/repo\n" +
		"pr: 55\n" +
		"verdict: REQUEST_CHANGES\n" +
		"decision:\n" +
		"decision_notes:\n" +
		"---\n\n" +
		"# Review\n\n" +
		"src/foo.go:10 - warning - missing nil check -> add a guard\n" +
		"src/bar.go:20 - nit - naming -> rename\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write test spool file: %v", err)
	}
	return path
}

// driveToDecidePostAwaitingConfirmation drives `decide post` through
// handleDecide and asserts the model reaches decidePostConfirmAwaiting —
// the decide-post analog of the deleted finalize.go's
// driveToAwaitingConfirmation.
func driveToDecidePostAwaitingConfirmation(t *testing.T, spoolPath string) model {
	t.Helper()
	m := newDecideRoutingTestModel()
	addReviewsTabWithRecord(m, review.Record{Repo: "owner/repo", PR: 55, SpoolPath: spoolPath})

	result, cmd := m.handleDecide([]string{"post"})
	if cmd != nil {
		t.Fatalf("expected nil cmd from handleDecide(post) (opens confirm gate synchronously), got %v", cmd)
	}
	if result.decidePostConfirmState != decidePostConfirmAwaiting {
		t.Fatalf("expected decidePostConfirmAwaiting, got %v", result.decidePostConfirmState)
	}
	return result
}

// TestDecidePost_ZeroFindings_PromptOmitsCount verifies the confirm prompt
// for a review with no parsed finding lines (e.g. an APPROVE with only
// summary prose + verdict) says "Post review to owner/repo#N?" rather than
// the misleading "Post 0 findings to ...". A zero-finding review is still
// postable — PostReview posts the whole body regardless — so the gate must
// still open; only the wording changes.
func TestDecidePost_ZeroFindings_PromptOmitsCount(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "pending", "owner-repo-55.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("failed to create pending dir: %v", err)
	}
	// Spool body with a verdict and prose but NO "file:line - sev - ..."
	// finding lines, so countFindings returns 0.
	content := "---\n" +
		"repo: owner/repo\n" +
		"pr: 55\n" +
		"verdict: APPROVE\n" +
		"decision:\n" +
		"decision_notes:\n" +
		"---\n\n" +
		"# Review\n\n" +
		"Looks good overall, no blocking issues.\n\n" +
		"VERDICT: APPROVE\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write test spool file: %v", err)
	}

	m := driveToDecidePostAwaitingConfirmation(t, path)

	if !activityContains(m, "Post review to owner/repo#55?") {
		t.Errorf("expected zero-findings prompt %q, activity lines: %v", "Post review to owner/repo#55?", m.activityLines)
	}
	if activityContains(m, "0 findings") {
		t.Errorf("prompt must not contain the misleading \"0 findings\"; activity lines: %v", m.activityLines)
	}
}

// TestDecidePost_NavigationKeyDoesNotCancel is the core regression guard
// for the decide-post confirm gate (the direct analog of issue #102's
// finalize confirm-gate fix, now for decide post): while
// decidePostConfirmAwaiting, pressing a navigation/other key must leave the
// pending confirmation untouched and must not emit a "cancelled" activity
// line.
func TestDecidePost_NavigationKeyDoesNotCancel(t *testing.T) {
	keys := []struct {
		name string
		msg  tea.KeyPressMsg
	}{
		{"left-bracket", tea.KeyPressMsg{Code: '[', Text: "["}},
		{"right-bracket", tea.KeyPressMsg{Code: ']', Text: "]"}},
		{"f2", tea.KeyPressMsg{Code: tea.KeyF2}},
		{"arrow-up", tea.KeyPressMsg{Code: tea.KeyUp}},
		{"unrelated-letter", pressKey('z')},
	}

	for _, k := range keys {
		t.Run(k.name, func(t *testing.T) {
			tmp := t.TempDir()
			spoolPath := newPostSpoolFixture(t, tmp, "owner-repo-55.md")
			m := driveToDecidePostAwaitingConfirmation(t, spoolPath)

			updated, _ := m.Update(k.msg)
			m2, ok := updated.(model)
			if !ok {
				t.Fatalf("Update did not return a model")
			}

			if m2.decidePostConfirmState != decidePostConfirmAwaiting {
				t.Errorf("expected decidePostConfirmAwaiting to survive key %q, got %v", k.name, m2.decidePostConfirmState)
			}
			if activityContains(m2, "cancelled") {
				t.Errorf("expected no 'cancelled' activity line after key %q, got %v", k.name, m2.activityLines)
			}
		})
	}
}

// TestDecidePost_LowercaseNCancels presses "n" while awaiting confirmation
// and asserts the cancel path: idle state, a cancelled activity line, no
// launch cmd, and — critically — the spool file's decision: front-matter
// is still blank on disk (proving "no decision recorded" literally, not
// just via activity-line text).
func TestDecidePost_LowercaseNCancels(t *testing.T) {
	tmp := t.TempDir()
	spoolPath := newPostSpoolFixture(t, tmp, "owner-repo-55.md")
	m := driveToDecidePostAwaitingConfirmation(t, spoolPath)

	updated, cmd := m.Update(pressKey('n'))
	m2, ok := updated.(model)
	if !ok {
		t.Fatalf("Update did not return a model")
	}

	if m2.decidePostConfirmState != decidePostConfirmIdle {
		t.Fatalf("expected decidePostConfirmIdle after 'n', got %v", m2.decidePostConfirmState)
	}
	if !activityContains(m2, "cancelled") {
		t.Errorf("expected a 'cancelled' activity line, got %v", m2.activityLines)
	}
	if !activityContains(m2, "no decision recorded") {
		t.Errorf("expected the cancel line to say no decision was recorded, got %v", m2.activityLines)
	}
	if cmd != nil {
		t.Error("expected no tea.Cmd after cancelling with 'n' (nothing launched)")
	}

	fields := readSpoolFrontMatterAtPath(t, spoolPath)
	if got := fields["decision"]; got != "" {
		t.Errorf("decision on disk = %q, want blank (nothing recorded on cancel)", got)
	}
}

// TestDecidePost_UppercaseNCancels is the uppercase counterpart —
// "N" must normalize the same way "n" does.
func TestDecidePost_UppercaseNCancels(t *testing.T) {
	tmp := t.TempDir()
	spoolPath := newPostSpoolFixture(t, tmp, "owner-repo-55.md")
	m := driveToDecidePostAwaitingConfirmation(t, spoolPath)

	updated, cmd := m.Update(pressKey('N'))
	m2, ok := updated.(model)
	if !ok {
		t.Fatalf("Update did not return a model")
	}

	if m2.decidePostConfirmState != decidePostConfirmIdle {
		t.Fatalf("expected decidePostConfirmIdle after 'N', got %v", m2.decidePostConfirmState)
	}
	if !activityContains(m2, "cancelled") {
		t.Errorf("expected a 'cancelled' activity line, got %v", m2.activityLines)
	}
	if cmd != nil {
		t.Error("expected no tea.Cmd after cancelling with 'N' (nothing launched)")
	}

	fields := readSpoolFrontMatterAtPath(t, spoolPath)
	if got := fields["decision"]; got != "" {
		t.Errorf("decision on disk = %q, want blank (nothing recorded on cancel)", got)
	}
}

// TestDecidePost_EscCancels is the Esc counterpart, proving the confirm
// gate intercepts Esc before any other Esc-handling block (overlay
// dismissal, planning-tab focus return, TabTypeReviewContent window-close)
// — mirroring issue #102's ordering guarantee, now for decide post.
func TestDecidePost_EscCancels(t *testing.T) {
	tmp := t.TempDir()
	spoolPath := newPostSpoolFixture(t, tmp, "owner-repo-55.md")
	m := driveToDecidePostAwaitingConfirmation(t, spoolPath)

	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m2, ok := updated.(model)
	if !ok {
		t.Fatalf("Update did not return a model")
	}

	if m2.decidePostConfirmState != decidePostConfirmIdle {
		t.Fatalf("expected decidePostConfirmIdle after Esc, got %v", m2.decidePostConfirmState)
	}
	if !activityContains(m2, "cancelled") {
		t.Errorf("expected a 'cancelled' activity line, got %v", m2.activityLines)
	}
	if cmd != nil {
		t.Error("expected no tea.Cmd after cancelling with Esc (nothing launched)")
	}

	fields := readSpoolFrontMatterAtPath(t, spoolPath)
	if got := fields["decision"]; got != "" {
		t.Errorf("decision on disk = %q, want blank (nothing recorded on cancel)", got)
	}
}

// TestDecidePost_YKeyPostsAndArchives drives to confirm state, presses
// "y", drains the returned tea.Cmd for decidePostCompleteMsg (substituting
// postReviewFunc with a fake so no real gh/kiro-cli is ever invoked), and
// asserts the fake was called once with the expected repo/pr and that the
// activity line reports success.
func TestDecidePost_YKeyPostsAndArchives(t *testing.T) {
	tmp := t.TempDir()
	spoolPath := newPostSpoolFixture(t, tmp, "owner-repo-55.md")
	m := driveToDecidePostAwaitingConfirmation(t, spoolPath)

	var calledWith review.Record
	var callCount int
	restore := stubPostReviewFunc(func(ctx context.Context, rec review.Record) (int, error) {
		calledWith = rec
		callCount++
		return 2, nil
	})
	defer restore()

	updated, cmd := m.Update(pressKey('y'))
	m2, ok := updated.(model)
	if !ok {
		t.Fatalf("Update did not return a model")
	}
	if m2.decidePostConfirmState != decidePostConfirmIdle {
		t.Fatalf("expected decidePostConfirmIdle immediately after 'y' (confirm gate resets before the launch runs), got %v", m2.decidePostConfirmState)
	}
	if cmd == nil {
		t.Fatalf("expected a non-nil tea.Cmd for the post launch, got nil")
	}

	completeMsg := drainBatchForType[decidePostCompleteMsg](t, cmd)
	final, finalCmd := m2.Update(completeMsg)
	if finalCmd != nil {
		t.Errorf("expected nil cmd from decidePostCompleteMsg handling, got %v", finalCmd)
	}
	finalModel, ok := final.(model)
	if !ok {
		t.Fatalf("Update did not return a model")
	}

	if callCount != 1 {
		t.Fatalf("expected postReviewFunc to be called exactly once, got %d", callCount)
	}
	if calledWith.Repo != "owner/repo" || calledWith.PR != 55 {
		t.Errorf("expected postReviewFunc called with owner/repo#55, got %+v", calledWith)
	}
	if !anyLineContains(finalModel.activityLines, "Posted 2 inline comments to owner/repo#55, archived to done/.") {
		t.Errorf("expected success activity line, got: %v", finalModel.activityLines)
	}
}

// TestDecidePost_GhFailure_LeavesDecisionWrittenInPending drives to confirm
// state, presses "y" with the fake postReviewFunc returning an error (as
// PostReview itself would after gh fails, per its crash-recovery
// contract), drains for decidePostErrorMsg, and asserts the activity line
// names the retry path. The actual "decision: post already written" part
// of the contract lives inside review.PostReview itself (see
// TestPostReview_GhFails_LeavesInPending_DecisionAlreadyWritten in
// internal/review) — this test only proves the TUI layer surfaces
// PostReview's error correctly and does not attempt to archive on failure.
func TestDecidePost_GhFailure_LeavesDecisionWrittenInPending(t *testing.T) {
	tmp := t.TempDir()
	spoolPath := newPostSpoolFixture(t, tmp, "owner-repo-55.md")
	m := driveToDecidePostAwaitingConfirmation(t, spoolPath)

	restore := stubPostReviewFunc(func(ctx context.Context, rec review.Record) (int, error) {
		return 0, fmt.Errorf("failed to post review for %s#%d: gh api pulls/reviews failed (exit 1): network error", rec.Repo, rec.PR)
	})
	defer restore()

	updated, cmd := m.Update(pressKey('y'))
	m2, ok := updated.(model)
	if !ok {
		t.Fatalf("Update did not return a model")
	}
	if cmd == nil {
		t.Fatalf("expected a non-nil tea.Cmd for the post launch, got nil")
	}

	errMsg := drainBatchForType[decidePostErrorMsg](t, cmd)
	final, finalCmd := m2.Update(errMsg)
	if finalCmd != nil {
		t.Errorf("expected nil cmd from decidePostErrorMsg handling, got %v", finalCmd)
	}
	finalModel, ok := final.(model)
	if !ok {
		t.Fatalf("Update did not return a model")
	}

	found := false
	for _, line := range finalModel.activityLines {
		if strings.Contains(line, "Failed to post review for owner/repo#55:") && strings.Contains(line, "retry with decide post") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error activity line naming the retry path, got: %v", finalModel.activityLines)
	}

	// The spool file itself is untouched by this test's fake (PostReview's
	// own front-matter write happens inside the real function, exercised by
	// internal/review's own tests) — assert the file still exists in
	// pending/ since the TUI layer must not have archived it on error.
	if _, err := os.Stat(spoolPath); err != nil {
		t.Errorf("expected spool file to still exist in pending/ after a post failure, got: %v", err)
	}
}

// TestDecideRevise_ImmediateNoConfirm drives `decide revise` and asserts
// the returned tea.Cmd is non-nil and, once drained, produces
// decideReviseCompleteMsg WITHOUT any intervening confirm keypress — there
// is no revise-confirm state field to even inspect, which is itself the
// proof that no such gate exists.
func TestDecideRevise_ImmediateNoConfirm(t *testing.T) {
	m := newDecideTestModel()
	addReviewsTabWithRecord(m, review.Record{Repo: "owner/repo", PR: 61, SpoolPath: "/tmp/does-not-matter.md"})

	restore := stubReviseReviewFunc(func(ctx context.Context, rec review.Record) (string, error) {
		return "COMMENT", nil
	})
	defer restore()

	result, cmd := m.handleDecide([]string{"revise"})
	if cmd == nil {
		t.Fatalf("expected a non-nil tea.Cmd for the revise launch (no confirm gate), got nil")
	}

	msg := drainBatchForType[decideReviseCompleteMsg](t, cmd)
	if msg.rec.PR != 61 || msg.newVerdict != "COMMENT" {
		t.Errorf("unexpected decideReviseCompleteMsg: %+v", msg)
	}
	if result.decidePostConfirmState != decidePostConfirmIdle {
		t.Errorf("revise must never touch the post confirm-gate state, got %v", result.decidePostConfirmState)
	}
}

// TestDecideRereview_ImmediateNoConfirm mirrors
// TestDecideRevise_ImmediateNoConfirm for the "rereview" action, including
// a degraded-to-revise sub-case.
func TestDecideRereview_ImmediateNoConfirm(t *testing.T) {
	t.Run("full lens fan-out", func(t *testing.T) {
		m := newDecideTestModel()
		addReviewsTabWithRecord(m, review.Record{Repo: "owner/repo", PR: 62, SpoolPath: "/tmp/does-not-matter.md"})

		restore := stubRereviewReviewFunc(func(ctx context.Context, rec review.Record) (string, bool, error) {
			return "REQUEST_CHANGES", false, nil
		})
		defer restore()

		result, cmd := m.handleDecide([]string{"rereview"})
		if cmd == nil {
			t.Fatalf("expected a non-nil tea.Cmd for the rereview launch (no confirm gate), got nil")
		}

		msg := drainBatchForType[decideRereviewCompleteMsg](t, cmd)
		if msg.rec.PR != 62 || msg.newVerdict != "REQUEST_CHANGES" || msg.degradedToRevise {
			t.Errorf("unexpected decideRereviewCompleteMsg: %+v", msg)
		}
		if result.decidePostConfirmState != decidePostConfirmIdle {
			t.Errorf("rereview must never touch the post confirm-gate state, got %v", result.decidePostConfirmState)
		}
	})

	t.Run("degraded to revise when diff file is gone", func(t *testing.T) {
		m := newDecideTestModel()
		addReviewsTabWithRecord(m, review.Record{Repo: "owner/repo", PR: 63, SpoolPath: "/tmp/does-not-matter.md"})

		restore := stubRereviewReviewFunc(func(ctx context.Context, rec review.Record) (string, bool, error) {
			return "APPROVE", true, nil
		})
		defer restore()

		result, cmd := m.handleDecide([]string{"rereview"})
		if cmd == nil {
			t.Fatalf("expected a non-nil tea.Cmd for the rereview launch, got nil")
		}

		msg := drainBatchForType[decideRereviewCompleteMsg](t, cmd)
		if !msg.degradedToRevise {
			t.Fatalf("expected degradedToRevise=true, got %+v", msg)
		}

		final, finalCmd := result.Update(msg)
		if finalCmd != nil {
			t.Errorf("expected nil cmd from decideRereviewCompleteMsg handling, got %v", finalCmd)
		}
		finalModel, ok := final.(model)
		if !ok {
			t.Fatalf("Update did not return a model")
		}
		if !anyLineContains(finalModel.activityLines, "degraded to revise") {
			t.Errorf("expected the activity line to mention the degrade, got: %v", finalModel.activityLines)
		}
	})
}

// TestDecideDiscard_ImmediateNoConfirm_ArchivesRightAway drives `decide
// discard` and asserts the file is archived to done/ with no confirm
// keypress needed.
func TestDecideDiscard_ImmediateNoConfirm_ArchivesRightAway(t *testing.T) {
	repoRoot := findRepoRootForTest(t)
	t.Chdir(repoRoot)
	tmp := t.TempDir()
	spoolPath := newPostSpoolFixture(t, tmp, "owner-repo-64.md")

	m := newDecideTestModel()
	addReviewsTabWithRecord(m, review.Record{Repo: "owner/repo", PR: 55, SpoolPath: spoolPath})

	result, cmd := m.handleDecide([]string{"discard"})
	if cmd == nil {
		t.Fatalf("expected a non-nil tea.Cmd for the discard launch (no confirm gate), got nil")
	}

	msg := drainBatchForType[decideDiscardCompleteMsg](t, cmd)
	final, finalCmd := result.Update(msg)
	if finalCmd != nil {
		t.Errorf("expected nil cmd from decideDiscardCompleteMsg handling, got %v", finalCmd)
	}
	finalModel, ok := final.(model)
	if !ok {
		t.Fatalf("Update did not return a model")
	}

	if !anyLineContains(finalModel.activityLines, "Discarded review for owner/repo#55 — archived to done/.") {
		t.Errorf("expected success activity line, got: %v", finalModel.activityLines)
	}
	donePath := filepath.Join(tmp, "done", "owner-repo-64.md")
	if _, err := os.Stat(donePath); err != nil {
		t.Errorf("expected spool file to be archived to %s: %v", donePath, err)
	}
	if _, err := os.Stat(spoolPath); !os.IsNotExist(err) {
		t.Errorf("expected spool file to no longer exist at pending/ path %s", spoolPath)
	}
}

// TestHandleDecide_InvalidVocabulary_StillRejectsBeforeDispatch is a
// regression check: the pre-existing invalid-vocabulary rejection
// (commands_decide_test.go's TestHandleDecide_InvalidVocabulary) needs no
// modification post-rewrite since validation happens before any dispatch —
// this test drives the same path via handleDecide directly to confirm the
// dispatch layer (Task 6/7) never runs for an invalid decision string.
func TestHandleDecide_InvalidVocabulary_StillRejectsBeforeDispatch(t *testing.T) {
	m := newDecideTestModel()
	addReviewsTabWithRecord(m, review.Record{Repo: "owner/repo", PR: 65, SpoolPath: "/tmp/spool.md"})

	result, cmd := m.handleDecide([]string{"bogus"})
	if cmd != nil {
		t.Errorf("expected nil cmd (rejected before dispatch), got %v", cmd)
	}
	if !anyLineContains(result.activityLines, "Invalid decision: bogus (must be post, revise, rereview, or discard)") {
		t.Errorf("expected invalid-vocabulary error line, got: %v", result.activityLines)
	}
	if result.decidePostConfirmState != decidePostConfirmIdle {
		t.Errorf("expected the post confirm-gate to be untouched by an invalid decision, got %v", result.decidePostConfirmState)
	}
}

// TestReviewsTabKeys_MatchReplDecideSemantics proves AC7 by direct
// behavioral comparison (not just "they call the same function," which
// dispatchDecideAction already guarantees structurally): driving the
// Reviews-tab "p" key must reach the exact same decidePostConfirmAwaiting
// state `decide post` (REPL) reaches.
func TestReviewsTabKeys_MatchReplDecideSemantics(t *testing.T) {
	// Entry point 1: REPL command.
	mViaRepl := newDecideTestModel()
	addReviewsTabWithRecord(mViaRepl, review.Record{Repo: "owner/repo", PR: 70, SpoolPath: "/tmp/spool.md"})
	replResult, replCmd := mViaRepl.handleDecide([]string{"post"})
	if replCmd != nil {
		t.Errorf("expected nil cmd from the REPL path, got %v", replCmd)
	}

	// Entry point 2: Reviews-tab "p" key, via decideRequestMsg round trip.
	mViaKey := newDecideRoutingTestModel()
	addReviewsTabWithRecord(mViaKey, review.Record{Repo: "owner/repo", PR: 70, SpoolPath: "/tmp/spool.md"})
	msgResult, keyCmd := mViaKey.Update(decideRequestMsg{
		repo:      "owner/repo",
		pr:        70,
		spoolPath: "/tmp/spool.md",
		decision:  "post",
	})
	if keyCmd != nil {
		t.Errorf("expected nil cmd from the key-driven path, got %v", keyCmd)
	}
	keyResult, ok := msgResult.(model)
	if !ok {
		t.Fatalf("Update did not return a model")
	}

	if replResult.decidePostConfirmState != decidePostConfirmAwaiting {
		t.Fatalf("REPL path: expected decidePostConfirmAwaiting, got %v", replResult.decidePostConfirmState)
	}
	if keyResult.decidePostConfirmState != decidePostConfirmAwaiting {
		t.Fatalf("key path: expected decidePostConfirmAwaiting, got %v", keyResult.decidePostConfirmState)
	}
	if replResult.decidePostPending.PR != keyResult.decidePostPending.PR {
		t.Errorf("expected both paths to record the same pending PR, got repl=%d key=%d", replResult.decidePostPending.PR, keyResult.decidePostPending.PR)
	}

	// Activity-line text should also match — same prompt, same values.
	replLine := replResult.activityLines[len(replResult.activityLines)-1]
	keyLine := keyResult.activityLines[len(keyResult.activityLines)-1]
	if replLine != keyLine {
		t.Errorf("expected identical confirm-prompt text from both entry points, got repl=%q key=%q", replLine, keyLine)
	}
}

// readSpoolFrontMatterAtPath reads and parses the front-matter of the
// spool file at path.
func readSpoolFrontMatterAtPath(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read spool file back: %v", err)
	}
	return review.ParseSpoolFrontMatter(data)
}

// stubPostReviewFunc substitutes the package-level postReviewFunc seam
// (decideactions.go) with fn for the duration of a test.
func stubPostReviewFunc(fn func(ctx context.Context, rec review.Record) (int, error)) func() {
	original := postReviewFunc
	postReviewFunc = fn
	return func() { postReviewFunc = original }
}

// stubRereviewReviewFunc substitutes the package-level rereviewReviewFunc
// seam (decideactions.go) with fn for the duration of a test.
func stubRereviewReviewFunc(fn func(ctx context.Context, rec review.Record) (string, bool, error)) func() {
	original := rereviewReviewFunc
	rereviewReviewFunc = fn
	return func() { rereviewReviewFunc = original }
}

// contains is already defined in commands_review_test.go (same package);
// reused here rather than redeclared.
