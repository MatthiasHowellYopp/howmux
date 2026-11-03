package templates

import (
	"embed"
	"os"
	"path/filepath"
	"testing"
)

//go:embed testdata/templates
var testTemplates embed.FS

func useTestTemplates(t *testing.T) {
	t.Helper()
	orig := Templates
	SetTemplates(testTemplates)
	t.Cleanup(func() { SetTemplates(orig) })
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// Template dirs are stored without a leading dot and must be extracted to
// .howmux/ and .kiro/. A regression here (the kiro-krew → howmux rename left
// the old mapping in place) made `howmux init` write ./howmux/ instead.
func TestExtract_RestoresDotDirs(t *testing.T) {
	useTestTemplates(t)
	dest := t.TempDir()

	if err := Extract("testdata/templates", dest, false); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{".howmux/config.yaml", ".howmux/scripts/a.sh", ".kiro/agents/x.json"} {
		if _, err := os.Stat(filepath.Join(dest, rel)); err != nil {
			t.Errorf("want %s extracted: %v", rel, err)
		}
	}
	for _, rel := range []string{"howmux", "kiro"} {
		if _, err := os.Stat(filepath.Join(dest, rel)); err == nil {
			t.Errorf("%s/ should not be created without its leading dot", rel)
		}
	}
}

// force (howmux update) overwrites other files but never .howmux/config.yaml.
func TestExtract_ForceKeepsConfig(t *testing.T) {
	useTestTemplates(t)
	dest := t.TempDir()
	cfg := filepath.Join(dest, ".howmux", "config.yaml")
	script := filepath.Join(dest, ".howmux", "scripts", "a.sh")
	for path, body := range map[string]string{cfg: "repo: mine\n", script: "local edit\n"} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := Extract("testdata/templates", dest, true); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, cfg); got != "repo: mine\n" {
		t.Errorf("config.yaml was overwritten: %q", got)
	}
	if got := mustRead(t, script); got != "#!/bin/bash\n" {
		t.Errorf("force should overwrite other template files, got %q", got)
	}
}

// Without force (howmux init), existing files are left alone.
func TestExtract_NoForceSkipsExisting(t *testing.T) {
	useTestTemplates(t)
	dest := t.TempDir()
	script := filepath.Join(dest, ".howmux", "scripts", "a.sh")
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("local edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Extract("testdata/templates", dest, false); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, script); got != "local edit\n" {
		t.Errorf("init overwrote an existing file: %q", got)
	}
}
