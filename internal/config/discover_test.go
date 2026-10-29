package config

import "testing"

func TestLoad_DiscoverRequestedDefaultOff(t *testing.T) {
	cfg, err := loadConfigFrom(t, "repo: test/repo")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Review.DiscoverRequested {
		t.Error("DiscoverRequested should default to false")
	}
}

func TestLoad_DiscoverRequestedEnabled(t *testing.T) {
	content := `repo: test/repo
review:
  discover_requested: true
  auto_post:
    mode: own-repos`
	cfg, err := loadConfigFrom(t, content)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.Review.DiscoverRequested {
		t.Error("DiscoverRequested = false, want true")
	}
	if cfg.Review.AutoPost.Mode != "own-repos" {
		t.Errorf("AutoPost.Mode = %q, want own-repos (sibling key must still parse)", cfg.Review.AutoPost.Mode)
	}
}
