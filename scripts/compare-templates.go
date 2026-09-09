package main

import (
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type ComparisonReport struct {
	MissingInTemplates []string          `json:"missing_in_templates"`
	ContentDifferences []ContentDiff     `json:"content_differences"`
	MissingInLive      []string          `json:"missing_in_live"`
	Summary            ComparisonSummary `json:"summary"`
}

type ContentDiff struct {
	Path         string `json:"path"`
	TemplatePath string `json:"template_path"`
	LivePath     string `json:"live_path"`
	Reason       string `json:"reason"`
}

type ComparisonSummary struct {
	TotalTemplateFiles int    `json:"total_template_files"`
	TotalLiveFiles     int    `json:"total_live_files"`
	SyncNeeded         int    `json:"sync_needed"`
	Status             string `json:"status"`
}

func main() {
	report := ComparisonReport{
		MissingInTemplates: []string{},
		ContentDifferences: []ContentDiff{},
		MissingInLive:      []string{},
	}

	templateBase := "cmd/howmux/templates"

	// --agents-only compares just the agent configs (used by `task sync:check`,
	// which relies on this tool's JSON-aware, local-only-allowlist comparison
	// instead of a raw `diff` so intentionally local-only entries like the
	// creds-agent MCP server don't register as drift). Default mode compares
	// everything (used by the template-sync summary helper).
	agentsOnly := len(os.Args) > 1 && os.Args[1] == "--agents-only"

	if agentsOnly {
		compareDirectory(templateBase+"/kiro/agents", ".kiro/agents", &report)
		report.Summary.TotalTemplateFiles = countFiles(templateBase + "/kiro/agents")
		report.Summary.TotalLiveFiles = countFiles(".kiro/agents")
	} else {
		// Compare howmux directory
		compareDirectory(templateBase+"/howmux", ".howmux", &report)
		// Compare kiro directory
		compareDirectory(templateBase+"/kiro", ".kiro", &report)
		report.Summary.TotalTemplateFiles = countFiles(templateBase)
		report.Summary.TotalLiveFiles = countFiles(".howmux") + countFiles(".kiro")
	}

	// Calculate summary
	report.Summary.SyncNeeded = len(report.MissingInTemplates) + len(report.ContentDifferences)

	if report.Summary.SyncNeeded == 0 {
		report.Summary.Status = "synchronized"
	} else {
		report.Summary.Status = "sync_required"
	}

	// Output structured report
	output, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error generating report: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(string(output))

	if report.Summary.SyncNeeded > 0 {
		os.Exit(1)
	}
}

func compareDirectory(templateDir, liveDir string, report *ComparisonReport) {
	// Scan live directory and compare with templates
	err := filepath.Walk(liveDir, func(livePath string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip inaccessible files
		}

		if info.IsDir() {
			return nil
		}

		// Skip generated/runtime files
		if shouldSkip(livePath) {
			return nil
		}

		// Calculate corresponding template path
		relPath, _ := filepath.Rel(liveDir, livePath)
		templatePath := filepath.Join(templateDir, relPath)

		// Check if template exists
		if _, err := os.Stat(templatePath); os.IsNotExist(err) {
			report.MissingInTemplates = append(report.MissingInTemplates, relPath)
			return nil
		}

		// Compare content
		if different, reason := compareFiles(templatePath, livePath); different {
			report.ContentDifferences = append(report.ContentDifferences, ContentDiff{
				Path:         relPath,
				TemplatePath: templatePath,
				LivePath:     livePath,
				Reason:       reason,
			})
		}

		return nil
	})

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error walking live directory %s: %v\n", liveDir, err)
	}

	// Check for template files missing in live
	err = filepath.Walk(templateDir, func(templatePath string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}

		relPath, _ := filepath.Rel(templateDir, templatePath)
		livePath := filepath.Join(liveDir, relPath)

		if _, err := os.Stat(livePath); os.IsNotExist(err) {
			report.MissingInLive = append(report.MissingInLive, relPath)
		}

		return nil
	})

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error walking template directory %s: %v\n", templateDir, err)
	}
}

func shouldSkip(path string) bool {
	skipPaths := []string{
		"specs/", "artifacts/", "retries/", "results/", "skills/",
		"config.yaml", "validation-results.md", "build-instructions.md",
	}

	for _, skip := range skipPaths {
		if strings.Contains(path, skip) {
			return true
		}
	}
	return false
}

func compareFiles(templatePath, livePath string) (bool, string) {
	// Agent (and other) JSON files are compared structurally, not byte-for-byte:
	//   - formatting/whitespace differences are ignored (the files are
	//     canonicalized before hashing), and
	//   - a named allowlist of local-only entries is stripped from BOTH sides
	//     before comparison, so personal runtime config that intentionally lives
	//     only in the live agents does not register as template drift.
	//
	// This prevents the recurring leak where a local-only addition (e.g. the
	// creds-agent MCP server) shows up as "sync required" and then gets copied
	// into the shipped //go:embed templates to make the check pass.
	if strings.HasSuffix(templatePath, ".json") && strings.HasSuffix(livePath, ".json") {
		return compareJSONFiles(templatePath, livePath)
	}

	templateHash, err1 := fileHash(templatePath)
	liveHash, err2 := fileHash(livePath)

	if err1 != nil {
		return true, fmt.Sprintf("template read error: %v", err1)
	}
	if err2 != nil {
		return true, fmt.Sprintf("live read error: %v", err2)
	}

	if templateHash != liveHash {
		return true, "content differs"
	}

	return false, ""
}

// localOnlyMCPServers is the named allowlist of MCP servers that are permitted
// to exist only in the live agent configs and NOT in the shipped templates.
// Adding a name here is a deliberate, reviewable declaration that the server is
// local-only (e.g. it vends credentials from the developer's machine) and must
// not be propagated into the embedded templates that ship to end users.
var localOnlyMCPServers = map[string]bool{
	"creds-agent": true,
}

// localOnlyTools is the named allowlist of tool grants tied to the local-only
// MCP servers above (an "@<server>" tool reference). Stripped from both sides
// before comparison for the same reason.
var localOnlyTools = map[string]bool{
	"@creds-agent": true,
}

// compareJSONFiles compares two JSON files after canonicalizing them and
// stripping the named local-only entries from both sides.
func compareJSONFiles(templatePath, livePath string) (bool, string) {
	tmplNorm, err := normalizedJSON(templatePath)
	if err != nil {
		return true, fmt.Sprintf("template read error: %v", err)
	}
	liveNorm, err := normalizedJSON(livePath)
	if err != nil {
		return true, fmt.Sprintf("live read error: %v", err)
	}

	if tmplNorm != liveNorm {
		return true, "content differs (after normalizing and stripping local-only entries)"
	}
	return false, ""
}

// normalizedJSON reads a JSON file, strips the named local-only entries, and
// returns a canonical (sorted-key) serialization for comparison.
func normalizedJSON(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	var v interface{}
	if err := json.Unmarshal(data, &v); err != nil {
		// Not valid JSON — fall back to raw content so we still detect drift.
		return string(data), nil
	}

	stripLocalOnly(v)

	// json.Marshal sorts map keys, giving a canonical form independent of the
	// original spacing/key order/trailing newline.
	out, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// stripLocalOnly removes the named local-only MCP servers and their associated
// tool grants from a decoded agent JSON document, in place.
func stripLocalOnly(v interface{}) {
	obj, ok := v.(map[string]interface{})
	if !ok {
		return
	}

	// Remove named local-only servers from mcpServers; drop mcpServers entirely
	// if it becomes empty so its mere presence doesn't count as drift.
	if servers, ok := obj["mcpServers"].(map[string]interface{}); ok {
		for name := range localOnlyMCPServers {
			delete(servers, name)
		}
		if len(servers) == 0 {
			delete(obj, "mcpServers")
		}
	}

	// Remove the associated "@<server>" tool grants from tools/allowedTools.
	for _, key := range []string{"tools", "allowedTools"} {
		if arr, ok := obj[key].([]interface{}); ok {
			filtered := make([]interface{}, 0, len(arr))
			for _, item := range arr {
				if s, ok := item.(string); ok && localOnlyTools[s] {
					continue
				}
				filtered = append(filtered, item)
			}
			obj[key] = filtered
		}
	}
}

func fileHash(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := md5.New()
	_, err = io.Copy(hash, file)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func countFiles(dir string) int {
	count := 0
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && !shouldSkip(path) {
			count++
		}
		return nil
	})
	return count
}
