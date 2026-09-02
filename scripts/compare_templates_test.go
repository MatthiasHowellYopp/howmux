package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// writeTemp writes content to a temp .json file and returns its path.
func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return p
}

// TestCompareJSONFiles exercises the contract that stands between a developer's
// local-only credential config and the shipped templates. A future edit that
// silently weakens any of these must break a test.
func TestCompareJSONFiles(t *testing.T) {
	const templateArchitect = `{
  "name": "architect",
  "tools": ["read", "write", "shell"],
  "allowedTools": ["read", "write", "shell"],
  "model": "claude-sonnet-4.5"
}`

	// Live agent = template + local-only creds-agent (mcpServers block and the
	// @creds-agent tool grant), plus different formatting/key order.
	const liveWithCreds = `{
  "allowedTools" : [ "read", "write", "shell", "@creds-agent" ],
  "model" : "claude-sonnet-4.5",
  "name" : "architect",
  "tools" : [ "read", "write", "shell", "@creds-agent" ],
  "mcpServers" : {
    "creds-agent" : { "command" : "aim", "args" : [ "mcp", "start-server", "local-creds-agent-mcp" ] }
  }
}`

	// Same as template but a real, non-local-only difference (model changed).
	const liveRealDrift = `{
  "name": "architect",
  "tools": ["read", "write", "shell"],
  "allowedTools": ["read", "write", "shell"],
  "model": "claude-sonnet-9-fake"
}`

	tests := []struct {
		name       string
		template   string
		live       string
		wantDiffer bool
	}{
		{
			name:       "local-only creds-agent + reformatting is NOT drift",
			template:   templateArchitect,
			live:       liveWithCreds,
			wantDiffer: false,
		},
		{
			name:       "real model change IS drift",
			template:   templateArchitect,
			live:       liveRealDrift,
			wantDiffer: true,
		},
		{
			name:       "identical files are not drift",
			template:   templateArchitect,
			live:       templateArchitect,
			wantDiffer: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tp := writeTemp(t, "template.json", tt.template)
			lp := writeTemp(t, "live.json", tt.live)
			differ, reason := compareJSONFiles(tp, lp)
			if differ != tt.wantDiffer {
				t.Errorf("compareJSONFiles differ=%v (%q), want %v", differ, reason, tt.wantDiffer)
			}
		})
	}
}

// TestStripLocalOnly asserts the stripping details the reviewer called out: the
// named server is removed, an empty mcpServers map is dropped entirely (so its
// bare presence is not drift), and the @creds-agent tool grant is removed while
// other tools are preserved.
func TestStripLocalOnly(t *testing.T) {
	raw := `{
  "tools": ["read", "@creds-agent", "shell"],
  "allowedTools": ["@creds-agent"],
  "mcpServers": { "creds-agent": { "command": "aim" } }
}`
	var v interface{}
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	stripLocalOnly(v)
	obj := v.(map[string]interface{})

	// mcpServers had only creds-agent -> the whole key must be gone.
	if _, ok := obj["mcpServers"]; ok {
		t.Errorf("mcpServers should be dropped when it becomes empty, got %v", obj["mcpServers"])
	}

	// @creds-agent removed from tools, others preserved.
	tools := obj["tools"].([]interface{})
	if len(tools) != 2 || tools[0] != "read" || tools[1] != "shell" {
		t.Errorf("tools = %v, want [read shell]", tools)
	}

	// allowedTools had only @creds-agent -> becomes empty slice (not absent).
	allowed := obj["allowedTools"].([]interface{})
	if len(allowed) != 0 {
		t.Errorf("allowedTools = %v, want empty", allowed)
	}
}

// TestStripLocalOnlyPreservesOtherMCPServers ensures a non-allowlisted MCP
// server is NOT stripped (so a genuine template-worthy server would still be
// compared and could still be flagged as drift).
func TestStripLocalOnlyPreservesOtherMCPServers(t *testing.T) {
	raw := `{ "mcpServers": { "creds-agent": {}, "some-shared-server": { "command": "x" } } }`
	var v interface{}
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	stripLocalOnly(v)
	obj := v.(map[string]interface{})

	servers, ok := obj["mcpServers"].(map[string]interface{})
	if !ok {
		t.Fatalf("mcpServers should remain (a non-local-only server is present), got %v", obj["mcpServers"])
	}
	if _, ok := servers["creds-agent"]; ok {
		t.Error("creds-agent should have been stripped")
	}
	if _, ok := servers["some-shared-server"]; !ok {
		t.Error("some-shared-server must be preserved so real drift is still caught")
	}
}
