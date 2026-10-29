package github

import (
	"reflect"
	"testing"
)

func TestReviewRequestedArgs(t *testing.T) {
	got := reviewRequestedArgs("Bit-Quill/flowdra", "someone")
	want := []string{
		"pr", "list",
		"--repo", "Bit-Quill/flowdra",
		"--state", "open",
		"--search", "review-requested:someone",
		"--json", "number,url,isDraft",
		"--limit", "100",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("reviewRequestedArgs() =\n  %q\nwant\n  %q", got, want)
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

func TestListReviewRequested_EmptyReviewer(t *testing.T) {
	// Must fail before shelling out: an empty qualifier is a meaningless search.
	if _, err := ListReviewRequested("o/r", "  "); err == nil {
		t.Error("ListReviewRequested() expected error for empty reviewer")
	}
}
