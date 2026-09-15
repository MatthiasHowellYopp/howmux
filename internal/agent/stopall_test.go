package agent

import (
	"testing"
	"time"

	"github.com/matthiashowellyopp/howmux/internal/config"
)

// TestStopAllReturnsPromptly verifies StopAll does not block: it must return
// well within stopAllTimeout even with multiple running agents. This guards the
// exit-hang regression where StopAll closed agent ACP clients sequentially and
// unbounded, making exit (and Ctrl+C, which routes through the same cleanup)
// hang ~3s per agent — or indefinitely on a wedged process.
func TestStopAllReturnsPromptly(t *testing.T) {
	cfg := &config.Config{}
	manager := NewManager(cfg)

	// Register several running agents with no ACP client and no process; this
	// exercises the concurrent close loop and the bounded wait without spawning
	// real kiro-cli processes.
	for i := 0; i < 5; i++ {
		id := "agent-" + itoa(i)
		manager.RegisterAgent(id, i+1)
		manager.agents[id].Status = StatusRunning
	}

	done := make(chan struct{})
	start := time.Now()
	go func() {
		manager.StopAll()
		close(done)
	}()

	select {
	case <-done:
		if elapsed := time.Since(start); elapsed >= stopAllTimeout {
			t.Fatalf("StopAll took %s, expected well under the %s deadline", elapsed, stopAllTimeout)
		}
	case <-time.After(stopAllTimeout + 2*time.Second):
		t.Fatalf("StopAll did not return within %s + margin — it is blocking", stopAllTimeout)
	}
}

// TestStopAllNoAgentsReturnsImmediately verifies the no-running-agents fast path.
func TestStopAllNoAgentsReturnsImmediately(t *testing.T) {
	cfg := &config.Config{}
	manager := NewManager(cfg)

	done := make(chan struct{})
	go func() {
		manager.StopAll()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("StopAll with no running agents did not return immediately")
	}
}

// itoa is a tiny helper to avoid importing strconv for a single conversion.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}
