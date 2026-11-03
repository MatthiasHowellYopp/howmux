package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/matthiashowellyopp/howmux/internal/templates"
)

// Runs extraction against the real embedded templates (what `howmux init`
// ships) and checks the files land under .howmux/, not ./howmux/.
func TestEmbeddedTemplatesExtractToDotHowmux(t *testing.T) {
	templates.SetTemplates(Templates)
	dest := t.TempDir()

	if err := templates.Extract("templates", dest, false); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{
		".howmux/config.yaml",
		".howmux/scripts/worktree-create.sh",
		".howmux/scripts/planning-worktree-create.sh",
	} {
		if _, err := os.Stat(filepath.Join(dest, rel)); err != nil {
			t.Errorf("want %s: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dest, "howmux")); err == nil {
		t.Error("init created ./howmux/ instead of .howmux/")
	}
}
