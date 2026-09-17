package tui

import (
	"fmt"
	"testing"

	"github.com/matthiashowellyopp/howmux/internal/config"
)

func TestResolveReviewer(t *testing.T) {
	tests := []struct {
		name         string
		cfg          *config.Config
		fakeGetUser  func() (string, error)
		wantReviewer string
		wantErr      bool
	}{
		{
			name:         "explicit override wins over gh login",
			cfg:          &config.Config{Reviewer: "override-user"},
			fakeGetUser:  func() (string, error) { return "gh-login-user", nil },
			wantReviewer: "override-user",
			wantErr:      false,
		},
		{
			name:         "empty override falls back to resolved gh login",
			cfg:          &config.Config{Reviewer: ""},
			fakeGetUser:  func() (string, error) { return "gh-login-user", nil },
			wantReviewer: "gh-login-user",
			wantErr:      false,
		},
		{
			name:         "empty override and gh resolution failure returns error",
			cfg:          &config.Config{Reviewer: ""},
			fakeGetUser:  func() (string, error) { return "", fmt.Errorf("gh: not authenticated") },
			wantReviewer: "",
			wantErr:      true,
		},
		{
			name:         "override is used even when gh resolution would also fail",
			cfg:          &config.Config{Reviewer: "override-user"},
			fakeGetUser:  func() (string, error) { return "", fmt.Errorf("gh: not authenticated") },
			wantReviewer: "override-user",
			wantErr:      false,
		},
		{
			name:         "override with different case is preserved verbatim (no normalization)",
			cfg:          &config.Config{Reviewer: "Some-User"},
			fakeGetUser:  func() (string, error) { return "gh-login-user", nil },
			wantReviewer: "Some-User",
			wantErr:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orig := getCurrentUserFunc
			getCurrentUserFunc = tt.fakeGetUser
			defer func() { getCurrentUserFunc = orig }()

			got, err := ResolveReviewer(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ResolveReviewer() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.wantReviewer {
				t.Errorf("ResolveReviewer() = %q, want %q", got, tt.wantReviewer)
			}
		})
	}
}
