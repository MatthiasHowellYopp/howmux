package watcher

import (
	"sync"
	"testing"
	"time"

	"github.com/jbrinkman/kiro-krew/internal/agent"
	"github.com/jbrinkman/kiro-krew/internal/config"
)

// TestRunning_ConcurrentAccess is a regression test for the race condition
// fixed in Task 1. It exercises Running() concurrently with Start()/Stop()
// to ensure the accessor properly uses RLock protection.
//
// Without the RLock fix, running `go test -race` would detect a data race
// between the read in Running() and the writes in Start()/Stop().
func TestRunning_ConcurrentAccess(t *testing.T) {
	cfg := &config.Config{
		Repo:         "test/repo",
		Label:        "test-label",
		PollInterval: time.Minute, // Long interval to avoid pollLoop interference
		MaxRetries:   3,
	}
	mgr := agent.NewManager(cfg)
	w := New(cfg, mgr)

	// Channel to signal test completion
	done := make(chan struct{})
	var wg sync.WaitGroup

	// Goroutine 1: Repeatedly call Running() to read w.started
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
				_ = w.Running() // Read access
			}
		}
	}()

	// Goroutine 2: Flip the watcher state via Start()/Stop()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			select {
			case <-done:
				return
			default:
				w.Start() // Write access
				time.Sleep(1 * time.Millisecond)
				w.Stop() // Write access
				time.Sleep(1 * time.Millisecond)
			}
		}
	}()

	// Let the concurrent access run for a short duration
	time.Sleep(50 * time.Millisecond)

	// Signal completion and wait for goroutines
	close(done)
	wg.Wait()

	// Ensure we end in a clean state
	if w.Running() {
		w.Stop()
	}
}
