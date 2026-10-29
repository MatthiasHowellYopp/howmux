package github

import (
	"reflect"
	"testing"
)

func TestReviewRequestedArgs(t *testing.T) {
	got, err := reviewRequestedArgs("Bit-Quill/flowdra", "someone")
	if err != nil {
		t.Fatalf("reviewRequestedArgs() error = %v", err)
	}
	want := []string{
		"pr", "list",
		"--repo", "Bit-Quill/flowdra",
		"--state", "open",
		// user-review-requested: matches direct requests only; review-requested:
		// would also match every PR assigned to a team the user is on.
		"--search", "user-review-requested:someone",
		"--json", "number,url,isDraft",
		"--limit", "100",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("reviewRequestedArgs() =\n  %q\nwant\n  %q", got, want)
	}
}

func TestValidateReviewerLogin(t *testing.T) {
	tests := []struct {
		reviewer string
		wantErr  bool
	}{
		{"MatthiasHowellYopp", false},
		{"some-user", false},
		{"", true},
		{"   ", true},
		{"Bit-Quill/reviewers", true}, // team: 422 from the search API
		{"two words", true},
		{"tab\there", true},
	}
	for _, tt := range tests {
		err := ValidateReviewerLogin(tt.reviewer)
		if (err != nil) != tt.wantErr {
			t.Errorf("ValidateReviewerLogin(%q) error = %v, wantErr %v", tt.reviewer, err, tt.wantErr)
		}
		// The argv builder must reject the same inputs, so ListReviewRequested
		// never reaches exec for them.
		if _, aerr := reviewRequestedArgs("o/r", tt.reviewer); (aerr != nil) != tt.wantErr {
			t.Errorf("reviewRequestedArgs(%q) error = %v, wantErr %v", tt.reviewer, aerr, tt.wantErr)
		}
	}
}

func TestParsePRRefs(t *testing.T) {
	data := []byte(`[
		{"isDraft":false,"number":1,"url":"https://github.com/o/r/pull/1"},
		{"isDraft":true,"number":2,"url":"https://github.com/o/r/pull/2"}
	]`)
	got, err := parsePRRefs(data)
	if err != nil {
		t.Fatalf("parsePRRefs() error = %v", err)
	}
	want := []PRRef{
		{Number: 1, URL: "https://github.com/o/r/pull/1", IsDraft: false},
		{Number: 2, URL: "https://github.com/o/r/pull/2", IsDraft: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parsePRRefs() = %+v, want %+v", got, want)
	}
}

func TestParsePRRefs_Empty(t *testing.T) {
	got, err := parsePRRefs([]byte(`[]`))
	if err != nil {
		t.Fatalf("parsePRRefs() error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("parsePRRefs([]) = %+v, want empty", got)
	}
}

func TestParsePRRefs_Malformed(t *testing.T) {
	if _, err := parsePRRefs([]byte(`{not json`)); err == nil {
		t.Error("parsePRRefs() expected error on malformed JSON")
	}
}
