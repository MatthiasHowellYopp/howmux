package github

import (
	"fmt"
	"testing"
)

func TestGetCurrentUser(t *testing.T) {
	tests := []struct {
		name      string
		fakeExec  func() ([]byte, error)
		wantLogin string
		wantErr   bool
	}{
		{
			name:      "successful login resolution",
			fakeExec:  func() ([]byte, error) { return []byte("octocat\n"), nil },
			wantLogin: "octocat",
			wantErr:   false,
		},
		{
			name:      "trims surrounding whitespace/newline",
			fakeExec:  func() ([]byte, error) { return []byte("  octocat\n\n"), nil },
			wantLogin: "octocat",
			wantErr:   false,
		},
		{
			name:      "gh command failure surfaces as error",
			fakeExec:  func() ([]byte, error) { return nil, fmt.Errorf("gh: not authenticated") },
			wantLogin: "",
			wantErr:   true,
		},
		{
			name:      "empty output is an error, not a valid empty login",
			fakeExec:  func() ([]byte, error) { return []byte(""), nil },
			wantLogin: "",
			wantErr:   true,
		},
		{
			name:      "whitespace-only output is an error",
			fakeExec:  func() ([]byte, error) { return []byte("   \n"), nil },
			wantLogin: "",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orig := execCurrentUserFunc
			execCurrentUserFunc = tt.fakeExec
			defer func() { execCurrentUserFunc = orig }()

			got, err := GetCurrentUser()
			if (err != nil) != tt.wantErr {
				t.Fatalf("GetCurrentUser() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.wantLogin {
				t.Errorf("GetCurrentUser() = %q, want %q", got, tt.wantLogin)
			}
		})
	}
}
