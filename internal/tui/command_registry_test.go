package tui

import (
	"testing"

	"github.com/matthiashowellyopp/howmux/internal/agent"
	"github.com/matthiashowellyopp/howmux/internal/config"
)

func TestCommandRegistry(t *testing.T) {
	cfg := &config.Config{}
	manager := agent.NewManager(cfg)
	registry := NewCommandRegistry(manager)

	// Test command filtering
	matches := registry.FilterCommands("w")
	if len(matches) != 1 || matches[0].Name != "watch" {
		t.Errorf("Expected 1 match for 'w', got %d", len(matches))
	}

	// Test subcommand filtering
	subcommands := registry.GetSubcommands("watch")
	if len(subcommands) != 2 {
		t.Errorf("Expected 2 subcommands for 'watch', got %d", len(subcommands))
	}

	// Test best match
	match := registry.GetBestMatch("wat")
	if match != "watch" {
		t.Errorf("Expected 'watch' for 'wat', got '%s'", match)
	}

	// Test valid command
	if !registry.IsValidCommand("watch start") {
		t.Error("Expected 'watch start' to be valid")
	}

	if registry.IsValidCommand("invalid command") {
		t.Error("Expected 'invalid command' to be invalid")
	}
}

func TestPlanClassicAutocomplete(t *testing.T) {
	cfg := &config.Config{}
	manager := agent.NewManager(cfg)
	registry := NewCommandRegistry(manager)

	// Test plan classic subcommand exists
	subcommands := registry.GetSubcommands("plan")
	if len(subcommands) != 1 || subcommands[0] != "classic" {
		t.Errorf("Expected plan to have 'classic' subcommand, got %v", subcommands)
	}

	// Test plan classic in flattened matches
	matches := registry.GetFlattenedMatches("plan")
	foundPlan := false
	foundPlanClassic := false
	for _, match := range matches {
		if match == "plan" {
			foundPlan = true
		}
		if match == "plan classic" {
			foundPlanClassic = true
		}
	}
	if !foundPlan {
		t.Error("Expected 'plan' in flattened matches")
	}
	if !foundPlanClassic {
		t.Error("Expected 'plan classic' in flattened matches")
	}

	// Test plan classic validation
	if !registry.IsValidCommand("plan classic") {
		t.Error("Expected 'plan classic' to be valid")
	}

	if !registry.IsValidCommand("plan classic some description") {
		t.Error("Expected 'plan classic some description' to be valid")
	}

	// Test backward compatibility for base plan command with description
	if !registry.IsValidCommand("plan some description") {
		t.Error("Expected 'plan some description' to be valid")
	}

	if !registry.IsValidCommand("plan implement feature X") {
		t.Error("Expected 'plan implement feature X' to be valid")
	}

	// Test prefix filtering for plan classic
	matches = registry.GetFlattenedMatches("plan c")
	foundPlanClassic = false
	for _, match := range matches {
		if match == "plan classic" {
			foundPlanClassic = true
			break
		}
	}
	if !foundPlanClassic {
		t.Error("Expected 'plan classic' in matches for 'plan c'")
	}

	// Test partial subcommand filtering
	subcommands = registry.GetSubcommands("plan c")
	if len(subcommands) != 1 || subcommands[0] != "classic" {
		t.Errorf("Expected 'plan c' to filter to 'classic' subcommand, got %v", subcommands)
	}

	// Test best match for plan classic
	match := registry.GetBestMatch("plan cl")
	if match != "plan classic" {
		t.Errorf("Expected 'plan classic' for 'plan cl', got '%s'", match)
	}
}

// TestDecideCommandRegistration verifies the "decide" command is registered
// with exactly the four expected subcommands, in registration order, and
// that IsValidCommand/GetSubcommands/GetFlattenedMatches all behave
// consistently with the existing generic registry logic (no special-casing
// needed for "decide").
func TestDecideCommandRegistration(t *testing.T) {
	cfg := &config.Config{}
	manager := agent.NewManager(cfg)
	registry := NewCommandRegistry(manager)

	cmd, exists := registry.GetCommand("decide")
	if !exists {
		t.Fatal("expected 'decide' command to be registered")
	}

	wantSubs := []string{"post", "revise", "rereview", "discard"}
	if len(cmd.Subcommands) != len(wantSubs) {
		t.Fatalf("expected %d subcommands, got %d: %v", len(wantSubs), len(cmd.Subcommands), cmd.Subcommands)
	}
	for i, want := range wantSubs {
		if cmd.Subcommands[i] != want {
			t.Errorf("Subcommands[%d] = %q, want %q (order: %v)", i, cmd.Subcommands[i], want, cmd.Subcommands)
		}
	}

	for _, sub := range wantSubs {
		input := "decide " + sub
		if !registry.IsValidCommand(input) {
			t.Errorf("expected IsValidCommand(%q) to be true", input)
		}
	}

	// Bare "decide" (no subcommand) is valid at the registry level, matching
	// the existing len==1 branch behavior verified against "watch" bare
	// (IsValidCommand("watch") == true) — the registry only checks
	// vocabulary when a subcommand is actually present.
	if !registry.IsValidCommand("decide") {
		t.Error("expected bare 'decide' to be valid at the registry level")
	}

	if registry.IsValidCommand("decide bogus") {
		t.Error("expected 'decide bogus' to be invalid")
	}

	gotSubs := registry.GetSubcommands("decide")
	if len(gotSubs) != len(wantSubs) {
		t.Fatalf("GetSubcommands(\"decide\") = %v, want %v", gotSubs, wantSubs)
	}
	for i, want := range wantSubs {
		if gotSubs[i] != want {
			t.Errorf("GetSubcommands(\"decide\")[%d] = %q, want %q", i, gotSubs[i], want)
		}
	}

	matches := registry.GetFlattenedMatches("dec")
	for _, sub := range wantSubs {
		want := "decide " + sub
		found := false
		for _, m := range matches {
			if m == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected %q in GetFlattenedMatches(\"dec\"), got %v", want, matches)
		}
	}
}

// TestFinalizeCommandRegistration verifies "finalize" is registered with no
// subcommands and HasArgs: false (matching "status"'s zero-arg
// registration — see command_registry.go), and appears in autocomplete
// matches for its own prefix.
func TestFinalizeCommandRegistration(t *testing.T) {
	cfg := &config.Config{}
	manager := agent.NewManager(cfg)
	registry := NewCommandRegistry(manager)

	cmd, exists := registry.GetCommand("finalize")
	if !exists {
		t.Fatal("expected 'finalize' command to be registered")
	}
	if cmd.HasArgs {
		t.Error("expected 'finalize' to have HasArgs: false (bare form only)")
	}
	if len(cmd.Subcommands) != 0 {
		t.Errorf("expected 'finalize' to have no subcommands, got %v", cmd.Subcommands)
	}

	if !registry.IsValidCommand("finalize") {
		t.Error("expected bare 'finalize' to be a valid command")
	}

	matches := registry.GetFlattenedMatches("fin")
	found := false
	for _, m := range matches {
		if m == "finalize" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'finalize' in GetFlattenedMatches(\"fin\"), got %v", matches)
	}
}

// TestNotesAndBodyEditKeys_NotRegisteredAsCommands confirms "n" (notes
// editing, issue #86 Task 2) and "e" (body editing, issue #86 Task 4) stay
// key-binding-only features, never accidentally registered as typed REPL
// commands — the same vocabulary/behavior split "decide" already
// establishes (row-shortcut key vs. footer-typed command are two entry
// points to the SAME action for p/r/R/d, but n/e have no footer/REPL
// counterpart at all).
func TestNotesAndBodyEditKeys_NotRegisteredAsCommands(t *testing.T) {
	cfg := &config.Config{}
	manager := agent.NewManager(cfg)
	registry := NewCommandRegistry(manager)

	if _, exists := registry.GetCommand("n"); exists {
		t.Error("expected 'n' to NOT be a registered command — notes editing is key-binding-only")
	}
	if _, exists := registry.GetCommand("e"); exists {
		t.Error("expected 'e' to NOT be a registered command — body editing is key-binding-only")
	}

	for _, cmd := range registry.GetAllCommands() {
		if cmd.Name == "n" || cmd.Name == "e" {
			t.Errorf("expected no registered command named %q", cmd.Name)
		}
		for _, sub := range cmd.Subcommands {
			if sub == "n" || sub == "e" {
				t.Errorf("expected no registered subcommand named %q under %q", sub, cmd.Name)
			}
		}
	}
}
