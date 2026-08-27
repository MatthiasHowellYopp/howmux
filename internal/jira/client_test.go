package jira

import "testing"

func TestParseSearchOutput(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []Issue
	}{
		{
			name: "empty result message",
			in:   "No issues found\n",
			want: nil,
		},
		{
			name: "header only",
			in:   "KEY | STATUS | TYPE | PTS | ASSIGNEE | SUMMARY\n",
			want: nil,
		},
		{
			name: "single row with header",
			in: "KEY | STATUS | TYPE | PTS | ASSIGNEE | SUMMARY\n" +
				"AEA-629 | On Hold | Task | - | Matthias Howell | Revamp cookbook\n",
			want: []Issue{
				{Key: "AEA-629", Status: "On Hold", Type: "Task", Points: "-", Assignee: "Matthias Howell", Summary: "Revamp cookbook"},
			},
		},
		{
			name: "multiple rows",
			in: "KEY | STATUS | TYPE | PTS | ASSIGNEE | SUMMARY\n" +
				"AEA-629 | On Hold | Task | - | Matthias Howell | Revamp Unstructured + Valkey cookbook\n" +
				"AEA-628 | To Do | Bug | 3 | Jane Doe | Fix flaky test\n",
			want: []Issue{
				{Key: "AEA-629", Status: "On Hold", Type: "Task", Points: "-", Assignee: "Matthias Howell", Summary: "Revamp Unstructured + Valkey cookbook"},
				{Key: "AEA-628", Status: "To Do", Type: "Bug", Points: "3", Assignee: "Jane Doe", Summary: "Fix flaky test"},
			},
		},
		{
			name: "summary containing the delimiter is preserved",
			in: "KEY | STATUS | TYPE | PTS | ASSIGNEE | SUMMARY\n" +
				"AEA-1 | To Do | Task | - | Me | do A | then B | finally C\n",
			want: []Issue{
				{Key: "AEA-1", Status: "To Do", Type: "Task", Points: "-", Assignee: "Me", Summary: "do A | then B | finally C"},
			},
		},
		{
			name: "pagination trailer is ignored",
			in: "KEY | STATUS | TYPE | PTS | ASSIGNEE | SUMMARY\n" +
				"AEA-629 | On Hold | Task | - | Matthias Howell | Revamp cookbook\n" +
				"More results available (next: ChUjU3RyaW5nJlFVVk=)\n",
			want: []Issue{
				{Key: "AEA-629", Status: "On Hold", Type: "Task", Points: "-", Assignee: "Matthias Howell", Summary: "Revamp cookbook"},
			},
		},
		{
			name: "blank lines and whitespace are tolerated",
			in: "\nKEY | STATUS | TYPE | PTS | ASSIGNEE | SUMMARY\n\n" +
				"   AEA-2 | Done | Task | 5 | Me | Ship it   \n\n",
			want: []Issue{
				{Key: "AEA-2", Status: "Done", Type: "Task", Points: "5", Assignee: "Me", Summary: "Ship it"},
			},
		},
		{
			name: "malformed row with too few columns is skipped",
			in: "KEY | STATUS | TYPE | PTS | ASSIGNEE | SUMMARY\n" +
				"AEA-3 | To Do | Task\n" +
				"AEA-4 | To Do | Task | - | Me | Valid row\n",
			want: []Issue{
				{Key: "AEA-4", Status: "To Do", Type: "Task", Points: "-", Assignee: "Me", Summary: "Valid row"},
			},
		},
		{
			name: "completely empty output",
			in:   "",
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseSearchOutput(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d issues, want %d\n got: %+v", len(got), len(tt.want), got)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("issue[%d] = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestListTodoIssues_EmptyJQL(t *testing.T) {
	if _, err := ListTodoIssues("  ", 10); err == nil {
		t.Error("expected error for empty JQL, got nil")
	}
}

func TestGetIssue_EmptyKey(t *testing.T) {
	if _, err := GetIssue(""); err == nil {
		t.Error("expected error for empty key, got nil")
	}
}
