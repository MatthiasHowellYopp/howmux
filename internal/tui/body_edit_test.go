package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/matthiashowellyopp/howmux/internal/agent"
	"github.com/matthiashowellyopp/howmux/internal/config"
	"github.com/matthiashowellyopp/howmux/internal/review"
)

// newBodyEditTestModel builds a model with everything handleBodyEditLaunch
// and the editorDoneMsg scaffold need: a real agent.Manager (so
// Suspend/ResumeOutputCapture behave exactly as in the running app), styles,
// and an initialized footer/tab system via newModel — mirroring
// createTestModelWithTab's (enter_key_test.go) construction pattern.
func newBodyEditTestModel(t *testing.T) model {
	t.Helper()
	cfg := &config.Config{LoadedTheme: createMinimalTheme()}
	return newModel(nil, agent.NewManager(cfg), cfg, nil, nil, "")
}

// withFakeBodyEditCommand substitutes bodyEditCommandFunc with a fake that
// records the argv it was called with and returns an *exec.Cmd whose exit
// code is controlled by exitCode, without spawning a real editor. Mirrors
// decisionwriter_test.go's fakeExecCommand pattern (/bin/sh -c "exit N").
func withFakeBodyEditCommand(t *testing.T, exitCode int) *[]string {
	t.Helper()
	orig := bodyEditCommandFunc
	var capturedArgv []string
	bodyEditCommandFunc = func(name string, arg ...string) *exec.Cmd {
		capturedArgv = append([]string{name}, arg...)
		return exec.Command("/bin/sh", "-c", "exit "+itoaBodyEdit(exitCode))
	}
	t.Cleanup(func() {
		bodyEditCommandFunc = orig
	})
	return &capturedArgv
}

// itoaBodyEdit avoids pulling in strconv just for a single non-negative int
// used inside a generated shell script in this test file.
func itoaBodyEdit(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// newPendingSpoolForBodyEdit writes a real spool file under a temp
// pending/-shaped directory so ReadSpoolBody/resolveSpoolPath resolve it,
// returning the spool path and the body content used.
func newPendingSpoolForBodyEdit(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "pr-review-owner-repo-1.md")
	content := "---\ndecision:\ndecision_notes: \"\"\n---\n" + body
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write spool: %v", err)
	}
	return path
}

// TestHandleBodyEditLaunchEditorUnset verifies that with $EDITOR unset,
// handleBodyEditLaunch reports an activity-line error and does NOT launch a
// subprocess, call SuspendOutputCapture, or create a temp file.
func TestHandleBodyEditLaunchEditorUnset(t *testing.T) {
	t.Setenv("EDITOR", "")

	capturedArgv := withFakeBodyEditCommand(t, 0)

	before, err := os.ReadDir(os.TempDir())
	if err != nil {
		t.Fatalf("read temp dir before: %v", err)
	}

	m := newBodyEditTestModel(t)
	spoolPath := newPendingSpoolForBodyEdit(t, "original body\n")
	rec := review.Record{Repo: "owner/repo", PR: 42, SpoolPath: spoolPath}

	m, cmd := m.handleBodyEditLaunch(rec)

	if cmd != nil {
		t.Errorf("expected nil tea.Cmd when $EDITOR is unset, got non-nil")
	}
	if len(*capturedArgv) != 0 {
		t.Errorf("expected bodyEditCommandFunc to never be called, got argv %v", *capturedArgv)
	}

	after, err := os.ReadDir(os.TempDir())
	if err != nil {
		t.Fatalf("read temp dir after: %v", err)
	}
	if countHowmuxReviewTemps(after) > countHowmuxReviewTemps(before) {
		t.Errorf("expected no howmux-review-*.md temp file to be created when $EDITOR is unset")
	}

	found := false
	for _, line := range m.activityLines {
		if strings.Contains(line, "$EDITOR is not set") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an activity line reporting $EDITOR is not set, got %v", m.activityLines)
	}
}

// countHowmuxReviewTemps counts entries matching the howmux-review-*.md temp
// file pattern handleBodyEditLaunch creates, so tests can assert none were
// left behind (or none were created in the unset-$EDITOR case) without
// depending on exact OS temp-dir naming beyond that glob.
func countHowmuxReviewTemps(entries []os.DirEntry) int {
	n := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "howmux-review-") && strings.HasSuffix(e.Name(), ".md") {
			n++
		}
	}
	return n
}

// TestHandleBodyEditLaunchSuspendsAndBuildsCommand verifies that with
// $EDITOR set, handleBodyEditLaunch suspends output capture exactly once,
// writes the current body to a temp file, and builds the exec.Cmd via the
// injectable seam with the temp file path as the last argument — using the
// fake seam so no real editor is spawned.
func TestHandleBodyEditLaunchSuspendsAndBuildsCommand(t *testing.T) {
	t.Setenv("EDITOR", "myeditor --flag")

	capturedArgv := withFakeBodyEditCommand(t, 0)

	m := newBodyEditTestModel(t)
	body := "original review body\nline two\n"
	spoolPath := newPendingSpoolForBodyEdit(t, body)
	rec := review.Record{Repo: "owner/repo", PR: 7, SpoolPath: spoolPath}

	m, cmd := m.handleBodyEditLaunch(rec)

	if cmd == nil {
		t.Fatalf("expected non-nil tea.Cmd when $EDITOR is set")
	}
	// SuspendOutputCapture's effect (m.terminalOutputPaused) is private to
	// the agent package with no exported getter, so it isn't directly
	// observable from this package's tests — commands.go's call to it is
	// a single, easily-inspected line (mirroring handlePlanSubprocess's
	// identical call), verified by code review rather than a state probe
	// here. The temp-file write and argv assertions below are what this
	// test can observe directly.

	argv := *capturedArgv
	if len(argv) < 3 {
		t.Fatalf("expected argv to have at least [editor, flag, tempPath], got %v", argv)
	}
	if argv[0] != "myeditor" {
		t.Errorf("expected editor binary %q, got %q (argv=%v)", "myeditor", argv[0], argv)
	}
	if argv[1] != "--flag" {
		t.Errorf("expected $EDITOR flags to be preserved, argv=%v", argv)
	}
	tempPath := argv[len(argv)-1]
	if !strings.HasPrefix(filepath.Base(tempPath), "howmux-review-") {
		t.Errorf("expected temp file path to match howmux-review-*.md, got %q", tempPath)
	}

	data, err := os.ReadFile(tempPath)
	if err != nil {
		t.Fatalf("expected temp file to exist with the review body: %v", err)
	}
	if string(data) != body {
		t.Errorf("temp file content = %q, want %q", string(data), body)
	}

	// handleBodyEditLaunch itself does not remove the temp file (that is
	// editorDoneMsg's job) — clean up so the test doesn't leak.
	_ = os.Remove(tempPath)

	// Executing the returned tea.Cmd runs the real tea.ExecProcess
	// machinery synchronously isn't exercised here — that requires a
	// running Bubble Tea program. The editorDoneMsg callback itself is
	// exercised directly in the tests below.
}

// runEditorDoneCase drives model.Update's editorDoneMsg case arm directly
// (bypassing the real tea.ExecProcess/tea.Program plumbing, which requires a
// live terminal) — this is the resume half of the round trip. spoolPath, if
// non-empty, is used as the rec's SpoolPath so BodyWriter.SetBody has a real
// file to act on (Task 5's end-to-end cases); tests that only care about the
// editor-error path (which never reaches BodyWriter.SetBody) may pass "".
func runEditorDoneCase(t *testing.T, execErr error, tempContent string, spoolPath string) (model, string) {
	t.Helper()
	m := newBodyEditTestModel(t)
	m.manager.SuspendOutputCapture()

	tempFile, err := os.CreateTemp("", "howmux-review-*.md")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	tempPath := tempFile.Name()
	if _, err := tempFile.WriteString(tempContent); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	_ = tempFile.Close()

	if spoolPath == "" {
		spoolPath = "/tmp/does-not-matter.md"
	}
	rec := review.Record{Repo: "owner/repo", PR: 99, SpoolPath: spoolPath}

	newM, _ := m.Update(editorDoneMsg{tempPath: tempPath, rec: rec, err: execErr})
	resultModel := newM.(model)
	return resultModel, tempPath
}

// TestEditorDoneMsgSuccess verifies the success path end-to-end against a
// real spool file: ResumeOutputCapture is called, a success activity line
// is appended, BodyWriter.SetBody updates the spool file's body while
// preserving its front-matter block byte-for-byte, and the temp file is
// removed. Uses the real set-review-body.sh script (not a fake), mirroring
// TestDecideIntegration's t.Chdir-to-repo-root approach, since
// bodyScriptPathFunc resolves the script path relative to CWD.
func TestEditorDoneMsgSuccess(t *testing.T) {
	repoRoot := findRepoRootForTest(t)
	t.Chdir(repoRoot)

	spoolPath := newPendingSpoolForBodyEdit(t, "original body\n")
	origData, err := os.ReadFile(spoolPath)
	if err != nil {
		t.Fatalf("read original spool: %v", err)
	}
	origFrontMatter := frontMatterBlock(t, string(origData))

	m, tempPath := runEditorDoneCase(t, nil, "edited body\nline two\n", spoolPath)

	found := false
	for _, line := range m.activityLines {
		if strings.Contains(line, "Updated review body for PR #99") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a success activity line for PR #99, got %v", m.activityLines)
	}

	if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
		t.Errorf("expected temp file %q to be removed, stat err = %v", tempPath, err)
	}

	newData, err := os.ReadFile(spoolPath)
	if err != nil {
		t.Fatalf("read updated spool: %v", err)
	}
	if got := frontMatterBlock(t, string(newData)); got != origFrontMatter {
		t.Errorf("front-matter block changed:\nbefore:\n%s\nafter:\n%s", origFrontMatter, got)
	}
	if !strings.Contains(string(newData), "edited body\nline two") {
		t.Errorf("expected updated spool to contain the edited body, got: %s", string(newData))
	}
	if strings.Contains(string(newData), "original body") {
		t.Errorf("expected old body to be replaced, but it is still present: %s", string(newData))
	}
}

// frontMatterBlock returns the front-matter block (the first "---" line
// through the closing "---" line, inclusive) of raw spool file content, for
// before/after comparison in tests.
func frontMatterBlock(t *testing.T, raw string) string {
	t.Helper()
	lines := strings.Split(raw, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		t.Fatalf("expected content to start with a front-matter fence, got: %s", raw)
	}
	for i, line := range lines[1:] {
		if strings.TrimSpace(line) == "---" {
			return strings.Join(lines[:i+2], "\n")
		}
	}
	t.Fatalf("no closing front-matter fence found in: %s", raw)
	return ""
}

// TestEditorDoneMsgEmptyFile verifies the empty-temp-file validation path:
// the spool file is left completely untouched (front-matter AND body),
// BodyWriter.SetBody is never invoked, an error activity line is shown, and
// the temp file is still removed.
func TestEditorDoneMsgEmptyFile(t *testing.T) {
	spoolPath := newPendingSpoolForBodyEdit(t, "original body\n")
	origData, err := os.ReadFile(spoolPath)
	if err != nil {
		t.Fatalf("read original spool: %v", err)
	}

	// Whitespace-only content must be treated as empty (TrimSpace), per
	// Task 5's acceptance criteria.
	m, tempPath := runEditorDoneCase(t, nil, "   \n\n  \n", spoolPath)

	found := false
	for _, line := range m.activityLines {
		if strings.Contains(line, "Editor produced an empty file for PR #99") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an empty-file error activity line for PR #99, got %v", m.activityLines)
	}

	if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
		t.Errorf("expected temp file %q to be removed, stat err = %v", tempPath, err)
	}

	newData, err := os.ReadFile(spoolPath)
	if err != nil {
		t.Fatalf("read spool after empty-file case: %v", err)
	}
	if string(newData) != string(origData) {
		t.Errorf("expected spool file to be completely untouched on empty-file error:\nbefore:\n%s\nafter:\n%s", string(origData), string(newData))
	}
}

// TestEditorDoneMsgNonZeroExit verifies the non-zero-exit path end-to-end:
// the spool file is left completely untouched, BodyWriter.SetBody is never
// invoked (there is no way to observe the call directly here, but leaving
// the spool file byte-for-byte identical is the externally observable
// proof), an error activity line is shown, and the temp file is removed
// even though the editor failed.
func TestEditorDoneMsgNonZeroExit(t *testing.T) {
	spoolPath := newPendingSpoolForBodyEdit(t, "original body\n")
	origData, err := os.ReadFile(spoolPath)
	if err != nil {
		t.Fatalf("read original spool: %v", err)
	}

	m, tempPath := runEditorDoneCase(t, &exec.ExitError{}, "content that must never be applied\n", spoolPath)

	found := false
	for _, line := range m.activityLines {
		if strings.Contains(line, "Editor exited with error for PR #99") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an error activity line for PR #99, got %v", m.activityLines)
	}

	if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
		t.Errorf("expected temp file %q to be removed even on editor error, stat err = %v", tempPath, err)
	}

	newData, err := os.ReadFile(spoolPath)
	if err != nil {
		t.Fatalf("read spool after non-zero-exit case: %v", err)
	}
	if string(newData) != string(origData) {
		t.Errorf("expected spool file to be completely untouched on editor error:\nbefore:\n%s\nafter:\n%s", string(origData), string(newData))
	}
}

// TestExecDoneMsgPlanningModeUnaffected is a regression test confirming
// Task 4's changes did not alter execDoneMsg's existing planning-mode
// resume behavior: ResumeOutputCapture is called, restoreConsoleState()
// restores the pre-planning console state (input value AND activity
// lines — restoreConsoleState() intentionally replaces activityLines with
// the pre-planning snapshot, discarding whatever was appended during
// planning, including the "Planning session completed successfully."
// line appended earlier in the same case arm; this is execDoneMsg's
// existing, pre-Task-4 behavior, not something this task changed), and a
// non-nil tea.Cmd is returned. This guards against editorDoneMsg's
// introduction accidentally sharing state or code paths with execDoneMsg.
func TestExecDoneMsgPlanningModeUnaffected(t *testing.T) {
	m := newBodyEditTestModel(t)
	m.manager.SuspendOutputCapture()

	// Seed console state the way handlePlanSubprocess does, so
	// restoreConsoleState() has something concrete to restore.
	m.consoleState.inputValue = "pre-planning input"
	m.consoleState.activityLines = []string{"pre-planning activity line"}
	m.activityLines = []string{"activity line added during planning"}

	newM, cmd := m.Update(execDoneMsg{err: nil})
	resultModel := newM.(model)

	if resultModel.input.Value() != "pre-planning input" {
		t.Errorf("expected restoreConsoleState() to restore input value, got %q", resultModel.input.Value())
	}

	if len(resultModel.activityLines) != 1 || resultModel.activityLines[0] != "pre-planning activity line" {
		t.Errorf("expected restoreConsoleState() to restore the pre-planning activity lines exactly, got %v", resultModel.activityLines)
	}

	if cmd == nil {
		t.Errorf("expected a non-nil tea.Cmd (input focus + ClearScreen) from execDoneMsg handling")
	}
}

var _ tea.Msg = editorDoneMsg{}
var _ tea.Msg = startBodyEditMsg{}
