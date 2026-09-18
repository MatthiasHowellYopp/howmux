package tui

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/matthiashowellyopp/howmux/internal/agent"
)

// withFakeFinalizeCommand swaps finalizeCommandFunc and finalizeScriptPathFunc
// for the duration of the test, restoring both in a cleanup hook — mirroring
// the withFakeRunner pattern used elsewhere in this codebase for injectable
// I/O seams. scriptPath/scriptErr control finalizeScriptPathFunc's return;
// cmdFunc controls finalizeCommandFunc's return (a fake *exec.Cmd built via
// exec.Command against a real, harmless binary like "true"/"false"/"sh").
func withFakeFinalizeCommand(t *testing.T, scriptPath string, scriptErr error, cmdFunc func(ctx context.Context, name string, args ...string) *exec.Cmd) {
	t.Helper()
	origCmdFunc := finalizeCommandFunc
	origScriptFunc := finalizeScriptPathFunc
	finalizeScriptPathFunc = func() (string, error) { return scriptPath, scriptErr }
	if cmdFunc != nil {
		finalizeCommandFunc = cmdFunc
	}
	t.Cleanup(func() {
		finalizeCommandFunc = origCmdFunc
		finalizeScriptPathFunc = origScriptFunc
	})
}

// TestRunFinalizeCmd_DryRunSuccess verifies a dry-run invocation that exits
// 0 returns finalizeDryRunMsg carrying the captured output.
func TestRunFinalizeCmd_DryRunSuccess(t *testing.T) {
	withFakeFinalizeCommand(t, "/fake/finalize-reviews.sh", nil, func(ctx context.Context, name string, args ...string) *exec.Cmd {
		// A real, harmless subprocess: print a line to stdout, then exit 0.
		return exec.CommandContext(ctx, "sh", "-c", "echo dry-run-output-line")
	})

	capture := agent.NewOutputCapture(100)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	msg := runFinalizeCmd(ctx, true, capture, cancel)()

	dryRunMsg, ok := msg.(finalizeDryRunMsg)
	if !ok {
		t.Fatalf("expected finalizeDryRunMsg, got %T: %+v", msg, msg)
	}
	found := false
	for _, l := range dryRunMsg.lines {
		if strings.Contains(l, "dry-run-output-line") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected captured lines to contain the subprocess output, got %v", dryRunMsg.lines)
	}
	if dryRunMsg.cancel == nil {
		t.Error("expected cancel to be carried through on finalizeDryRunMsg")
	}
}

// TestRunFinalizeCmd_LiveSuccess verifies a live (non-dry-run) invocation
// that exits 0 returns finalizeCompleteMsg.
func TestRunFinalizeCmd_LiveSuccess(t *testing.T) {
	withFakeFinalizeCommand(t, "/fake/finalize-reviews.sh", nil, func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "echo live-output-line")
	})

	capture := agent.NewOutputCapture(100)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	msg := runFinalizeCmd(ctx, false, capture, cancel)()

	completeMsg, ok := msg.(finalizeCompleteMsg)
	if !ok {
		t.Fatalf("expected finalizeCompleteMsg, got %T: %+v", msg, msg)
	}
	found := false
	for _, l := range completeMsg.lines {
		if strings.Contains(l, "live-output-line") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected captured lines to contain the subprocess output, got %v", completeMsg.lines)
	}
}

// TestRunFinalizeCmd_ScriptNotFound verifies that when finalizeScriptPathFunc
// returns an error, no subprocess is spawned and finalizeErrorMsg is
// returned immediately.
func TestRunFinalizeCmd_ScriptNotFound(t *testing.T) {
	spawned := false
	withFakeFinalizeCommand(t, "", &finalizeScriptNotFoundError{}, func(ctx context.Context, name string, args ...string) *exec.Cmd {
		spawned = true
		return exec.CommandContext(ctx, "true")
	})

	capture := agent.NewOutputCapture(100)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	msg := runFinalizeCmd(ctx, true, capture, cancel)()

	errMsg, ok := msg.(finalizeErrorMsg)
	if !ok {
		t.Fatalf("expected finalizeErrorMsg, got %T: %+v", msg, msg)
	}
	if !errMsg.dryRun {
		t.Error("expected dryRun to be true")
	}
	if errMsg.err == nil {
		t.Error("expected a non-nil error")
	}
	if spawned {
		t.Error("expected no subprocess to be spawned when script path resolution fails")
	}
}

// TestRunFinalizeCmd_NonZeroExit verifies a non-zero exit produces
// finalizeErrorMsg with whatever partial output was captured before
// failure attached.
func TestRunFinalizeCmd_NonZeroExit(t *testing.T) {
	withFakeFinalizeCommand(t, "/fake/finalize-reviews.sh", nil, func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "echo partial-output-before-failure; exit 1")
	})

	capture := agent.NewOutputCapture(100)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	msg := runFinalizeCmd(ctx, false, capture, cancel)()

	errMsg, ok := msg.(finalizeErrorMsg)
	if !ok {
		t.Fatalf("expected finalizeErrorMsg, got %T: %+v", msg, msg)
	}
	if errMsg.dryRun {
		t.Error("expected dryRun to be false for a live-run failure")
	}
	if errMsg.err == nil {
		t.Error("expected a non-nil error for a non-zero exit")
	}
	found := false
	for _, l := range errMsg.lines {
		if strings.Contains(l, "partial-output-before-failure") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected partial output to be captured, got %v", errMsg.lines)
	}
}

// TestPollFinalizeOutputCmd_ProducesFinalizeTickMsg verifies the tea.Cmd
// returned by pollFinalizeOutputCmd resolves to a finalizeTickMsg after the
// poll interval elapses.
func TestPollFinalizeOutputCmd_ProducesFinalizeTickMsg(t *testing.T) {
	cmd := pollFinalizeOutputCmd()
	if cmd == nil {
		t.Fatal("expected a non-nil tea.Cmd")
	}

	done := make(chan struct{})
	var msg any
	go func() {
		msg = cmd()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for pollFinalizeOutputCmd's tea.Cmd to resolve")
	}

	if _, ok := msg.(finalizeTickMsg); !ok {
		t.Fatalf("expected finalizeTickMsg, got %T: %+v", msg, msg)
	}
}

// TestFinalizeStateEnum_HasFourDistinctValues is a basic sanity check that
// the four finalizeState values are distinct, so a stray comparison bug
// (e.g. two states accidentally sharing a value) would be caught here
// rather than surfacing as a confusing state-machine bug elsewhere.
func TestFinalizeStateEnum_HasFourDistinctValues(t *testing.T) {
	states := []finalizeState{finalizeIdle, finalizeDryRunRunning, finalizeAwaitingConfirmation, finalizeLiveRunning}
	seen := make(map[finalizeState]bool)
	for _, s := range states {
		if seen[s] {
			t.Fatalf("finalizeState value %v is not distinct", s)
		}
		seen[s] = true
	}
	if len(seen) != 4 {
		t.Fatalf("expected 4 distinct finalizeState values, got %d", len(seen))
	}
}

// TestOutputCapture_ConcurrentWriterAndReader is the required concurrent
// test (see issue #87 design spec, "Concurrency Analysis") exercising
// OutputCapture.AddLine concurrently with GetLines()/Generation() from
// separate goroutines simulating the writer (subprocess capture, via a fake
// finalizeCommandFunc writing a burst of lines) and reader (poll tick) sides
// of the exact access pattern runFinalizeCmd's goroutine and the poll
// loop's finalizeTickMsg handler use in production. Run under `go test
// -race` to prove no data race exists in this access pattern.
func TestOutputCapture_ConcurrentWriterAndReader(t *testing.T) {
	capture := agent.NewOutputCapture(1000)

	// Writer side: a fake finalizeCommandFunc that emits a burst of lines,
	// exercising the same CaptureWriter path runFinalizeCmd uses in
	// production.
	lineCount := 200
	script := "for i in $(seq 1 200); do echo line-$i; done"
	withFakeFinalizeCommand(t, "/fake/finalize-reviews.sh", nil, func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", script)
	})

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Reader goroutines: continuously call GetLines() and Generation()
	// concurrently with the writer goroutine below, matching the poll
	// loop's finalizeTickMsg access pattern.
	wg.Add(2)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = capture.GetLines()
			}
		}
	}()
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = capture.Generation()
			}
		}
	}()

	// Writer side: run the subprocess capture (the same runFinalizeCmd
	// entry point production code uses) while readers are hammering the
	// same OutputCapture concurrently.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	msg := runFinalizeCmd(ctx, true, capture, cancel)()

	close(stop)
	wg.Wait()

	dryRunMsg, ok := msg.(finalizeDryRunMsg)
	if !ok {
		t.Fatalf("expected finalizeDryRunMsg, got %T: %+v", msg, msg)
	}
	if len(dryRunMsg.lines) != lineCount {
		t.Errorf("expected %d captured lines, got %d", lineCount, len(dryRunMsg.lines))
	}
	for i, l := range dryRunMsg.lines {
		want := fmt.Sprintf("line-%d", i+1)
		if l != want {
			t.Errorf("line %d: got %q, want %q", i, l, want)
			break
		}
	}
}
