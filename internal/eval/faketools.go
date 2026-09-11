package eval

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// How to Add a New Fake Tool
//
// Adding support for a new external tool requires three small, uniform steps:
//
// 1. Define a fake shim constant: Add a `const fakeXyzShim` string below containing
//    a bash script that logs calls to HOWMUX_EVAL_CALL_LOG and returns canned responses
//    for common commands (follow the pattern of fakeGHShim, fakeJtkShim, etc.)
//
// 2. Write the fake script in SetupFakeTools: Add a call to os.WriteFile that writes
//    the fake shim to filepath.Join(tempDir, "xyz") with permissions 0755
//
// 3. That's it: The PATH injection and call logging are already service-agnostic,
//    so no other changes are needed
//
// Example:
//   const fakeXyzShim = `#!/bin/bash
//   if [ -n "$HOWMUX_EVAL_CALL_LOG" ]; then
//     echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) xyz $*" >> "$HOWMUX_EVAL_CALL_LOG"
//   fi
//   echo '{"status": "success"}'
//   exit 0`
//
//   Then in SetupFakeTools:
//     xyzPath := filepath.Join(tempDir, "xyz")
//     if err := os.WriteFile(xyzPath, []byte(fakeXyzShim), 0755); err != nil { ... }

// fakeGHShim is the embedded fake gh CLI script that logs calls and returns canned responses
const fakeGHShim = `#!/bin/bash
# Mock GitHub CLI for eval testing
# Logs operations and returns canned responses

# Log each call with timestamp to the path specified by HOWMUX_EVAL_CALL_LOG
if [ -n "$HOWMUX_EVAL_CALL_LOG" ]; then
  echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) gh $*" >> "$HOWMUX_EVAL_CALL_LOG"
fi

case "$1" in
  auth)
    echo "github.com"
    echo "  ✓ Logged in to github.com account sandbox-user (mocked)"
    ;;
  issue)
    case "$2" in
      create)
        echo '{"number": 12345, "url": "https://github.com/test/test/issues/12345"}'
        ;;
      list)
        echo '[]'
        ;;
      view)
        echo '{"number": 1, "title": "Mock issue", "body": "Mock body", "labels": []}'
        ;;
      edit)
        echo '{"number": 1}'
        ;;
      *)
        echo '{"status": "success"}'
        ;;
    esac
    ;;
  pr)
    case "$2" in
      create)
        echo '{"number": 42, "url": "https://github.com/test/test/pull/42"}'
        ;;
      list)
        echo '[]'
        ;;
      view)
        echo '{"number": 1, "title": "Mock PR", "body": "Mock body"}'
        ;;
      *)
        echo '{"status": "success"}'
        ;;
    esac
    ;;
  release)
    echo '{"tag_name": "v0.0.0", "name": "Mock Release"}'
    ;;
  api)
    echo '[]'
    ;;
  *)
    echo '{"status": "success"}'
    ;;
esac

exit 0
`

// fakeJtkShim is the embedded fake jtk / atlassian-cli script that logs calls and returns canned responses
const fakeJtkShim = `#!/bin/bash
# Mock Jira Ticket CLI (jtk / atlassian-cli) for eval testing
# Logs operations and returns canned responses

# Log each call with timestamp to the path specified by HOWMUX_EVAL_CALL_LOG
if [ -n "$HOWMUX_EVAL_CALL_LOG" ]; then
  echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) jtk $*" >> "$HOWMUX_EVAL_CALL_LOG"
fi

case "$1" in
  issues)
    case "$2" in
      get)
        # jtk issues get <KEY>
        echo 'KEY | STATUS | TYPE | PTS | ASSIGNEE | SUMMARY'
        echo 'AEA-123 | In Progress | Task | - | sandbox-user | Mock Jira issue'
        ;;
      search)
        # jtk issues search --jql "<query>"
        echo 'KEY | STATUS | TYPE | PTS | ASSIGNEE | SUMMARY'
        echo 'AEA-123 | In Progress | Task | - | sandbox-user | Mock search result'
        ;;
      create)
        # jtk issues create ...
        echo 'AEA-12345'
        ;;
      update)
        # jtk issues update <KEY> --field ...
        echo 'KEY | STATUS | TYPE | PTS | ASSIGNEE | SUMMARY'
        echo 'AEA-123 | Updated | Task | - | sandbox-user | Mock updated issue'
        ;;
      *)
        echo 'KEY | STATUS | TYPE | PTS | ASSIGNEE | SUMMARY'
        echo 'AEA-123 | Open | Task | - | sandbox-user | Mock issue'
        ;;
    esac
    ;;
  comments)
    case "$2" in
      list)
        # jtk comments list <KEY>
        echo 'ID | AUTHOR | CREATED | BODY'
        echo '1 | sandbox-user | 2026-01-01T00:00:00Z | Mock comment'
        ;;
      add)
        # jtk comments add <KEY> --body "text"
        echo 'Comment added to AEA-123'
        ;;
      *)
        echo 'ID | AUTHOR | CREATED | BODY'
        ;;
    esac
    ;;
  projects)
    case "$2" in
      list)
        echo 'KEY | NAME | TYPE'
        echo 'AEA | AI Engineering Automations | software'
        ;;
      *)
        echo 'KEY | NAME | TYPE'
        echo 'AEA | AI Engineering Automations | software'
        ;;
    esac
    ;;
  me)
    echo 'USER | EMAIL | ACCOUNT_ID'
    echo 'sandbox-user | sandbox@example.com | mock-account-id'
    ;;
  transitions)
    case "$2" in
      list)
        # jtk transitions list <KEY>
        echo 'ID | NAME'
        echo '1 | To Do'
        echo '2 | In Progress'
        echo '3 | Done'
        ;;
      do)
        # jtk transitions do <KEY> <ID>
        echo 'Transitioned AEA-123 to new status'
        ;;
      *)
        echo 'ID | NAME'
        ;;
    esac
    ;;
  *)
    echo 'Mock jtk response'
    ;;
esac

exit 0
`

// fakeAsanaShim is the embedded fake asana CLI script that logs calls and returns canned responses
// This is a placeholder fake for future Asana CLI integration
const fakeAsanaShim = `#!/bin/bash
# Mock Asana CLI for eval testing
# Logs operations and returns minimal canned responses (placeholder for future use)

# Log each call with timestamp to the path specified by HOWMUX_EVAL_CALL_LOG
if [ -n "$HOWMUX_EVAL_CALL_LOG" ]; then
  echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) asana $*" >> "$HOWMUX_EVAL_CALL_LOG"
fi

# Minimal placeholder responses for common Asana CLI patterns
case "$1" in
  tasks)
    echo '{"data": [{"gid": "12345", "name": "Mock Task"}]}'
    ;;
  projects)
    echo '{"data": [{"gid": "67890", "name": "Mock Project"}]}'
    ;;
  *)
    echo '{"data": []}'
    ;;
esac

exit 0
`

// SetupFakeTools creates a temporary directory with fake CLI tools and returns paths
// for injecting onto PATH. It returns the tools directory path, call log file path,
// a cleanup function, and any error encountered.
//
// The cleanup function removes the temporary directory and should be deferred.
// Each call to SetupFakeTools creates an isolated temp directory to avoid collisions
// in concurrent test runs.
//
// Currently installs fake scripts for: gh, jtk, asana
func SetupFakeTools(baseDir string) (toolsDir string, callLogPath string, cleanup func(), err error) {
	// Create unique temp directory under baseDir
	tempDir, err := os.MkdirTemp(baseDir, "fake-tools-*")
	if err != nil {
		return "", "", nil, fmt.Errorf("creating temp tools directory: %w", err)
	}

	cleanup = func() {
		os.RemoveAll(tempDir)
	}

	// Write the fake gh script
	ghPath := filepath.Join(tempDir, "gh")
	if runtime.GOOS == "windows" {
		ghPath += ".bat"
	}

	if err := os.WriteFile(ghPath, []byte(fakeGHShim), 0755); err != nil {
		cleanup()
		return "", "", nil, fmt.Errorf("writing fake gh script: %w", err)
	}

	// Write the fake jtk script
	jtkPath := filepath.Join(tempDir, "jtk")
	if runtime.GOOS == "windows" {
		jtkPath += ".bat"
	}

	if err := os.WriteFile(jtkPath, []byte(fakeJtkShim), 0755); err != nil {
		cleanup()
		return "", "", nil, fmt.Errorf("writing fake jtk script: %w", err)
	}

	// Write the fake asana script
	asanaPath := filepath.Join(tempDir, "asana")
	if runtime.GOOS == "windows" {
		asanaPath += ".bat"
	}

	if err := os.WriteFile(asanaPath, []byte(fakeAsanaShim), 0755); err != nil {
		cleanup()
		return "", "", nil, fmt.Errorf("writing fake asana script: %w", err)
	}

	// Create call log file
	callLogPath = filepath.Join(tempDir, "call.log")
	if _, err := os.Create(callLogPath); err != nil {
		cleanup()
		return "", "", nil, fmt.Errorf("creating call log file: %w", err)
	}

	return tempDir, callLogPath, cleanup, nil
}

// readCallLog parses the call log file into structured ExternalCall entries.
// Returns an empty slice if the file doesn't exist or is empty.
// Each log line is expected to be in the format:
//
//	<ISO8601-timestamp> <tool> <arg1> <arg2> ...
func readCallLog(path string) ([]ExternalCall, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []ExternalCall{}, nil
		}
		return nil, fmt.Errorf("opening call log: %w", err)
	}
	defer file.Close()

	var calls []ExternalCall
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		// Parse line: timestamp tool arg1 arg2 ...
		parts := strings.Fields(line)
		if len(parts) < 2 {
			// Malformed line, skip
			continue
		}

		timestamp := parts[0]
		tool := parts[1]
		args := parts[2:]

		calls = append(calls, ExternalCall{
			Timestamp: timestamp,
			Tool:      tool,
			Args:      args,
			RawLine:   line,
		})
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading call log: %w", err)
	}

	return calls, nil
}

// prependPathEnv prepends dir to the PATH environment variable in the env slice.
// Handles both Unix (:) and Windows (;) path separators.
// If PATH is not present in env, it adds a new PATH entry.
func prependPathEnv(env []string, dir string) []string {
	pathSep := ":"
	if runtime.GOOS == "windows" {
		pathSep = ";"
	}

	pathPrefix := "PATH="
	pathIndex := -1

	// Find existing PATH entry
	for i, entry := range env {
		if strings.HasPrefix(entry, pathPrefix) {
			pathIndex = i
			break
		}
	}

	if pathIndex >= 0 {
		// PATH exists, prepend to it
		existingPath := env[pathIndex][len(pathPrefix):]
		if existingPath == "" {
			env[pathIndex] = pathPrefix + dir
		} else {
			env[pathIndex] = pathPrefix + dir + pathSep + existingPath
		}
	} else {
		// PATH doesn't exist, add it
		env = append(env, pathPrefix+dir)
	}

	return env
}

// MatchCall returns true if the recorded external call matches the expected call.
// Matching requires:
//   - Tool names match exactly (case-sensitive)
//   - Subcommand appears in the recorded args (if specified)
//   - All required args appear somewhere in args or raw line (if specified)
//
// The subcommand match is flexible: "pr create" matches if both "pr" and "create"
// appear consecutively in recorded.Args. Required args use substring matching to
// avoid brittleness from flag ordering.
func MatchCall(recorded ExternalCall, expected ExpectedCall) bool {
	// Tool must match exactly
	if recorded.Tool != expected.Tool {
		return false
	}

	// If subcommand specified, verify it appears in args
	if expected.Subcommand != "" {
		subParts := strings.Fields(expected.Subcommand)
		if !matchSubcommand(recorded.Args, subParts) {
			return false
		}
	}

	// All required args must appear somewhere in args or raw line
	for _, reqArg := range expected.RequiredArgs {
		if !containsArg(recorded.Args, recorded.RawLine, reqArg) {
			return false
		}
	}

	return true
}

// matchSubcommand checks if subParts appear consecutively at the start of args
func matchSubcommand(args []string, subParts []string) bool {
	if len(subParts) > len(args) {
		return false
	}

	for i, part := range subParts {
		if args[i] != part {
			return false
		}
	}

	return true
}

// containsArg checks if reqArg appears as a substring in any arg or in the raw line
func containsArg(args []string, rawLine string, reqArg string) bool {
	// Check each arg
	for _, arg := range args {
		if strings.Contains(arg, reqArg) {
			return true
		}
	}

	// Fallback: check raw line for substring match
	return strings.Contains(rawLine, reqArg)
}

// AnalyzeCalls compares recorded external calls against expected calls and returns:
//   - satisfied: descriptions of expected calls that were matched
//   - missing: descriptions of expected calls that were not matched
//   - unexpected: descriptions of recorded calls that matched no expectation
//
// Each description is a human-readable string summarizing the call (tool, subcommand, key args).
func AnalyzeCalls(recorded []ExternalCall, expected []ExpectedCall) (satisfied, missing, unexpected []string) {
	// Track which expected calls were satisfied
	satisfiedFlags := make([]bool, len(expected))

	// Track which recorded calls were matched
	matchedRecorded := make([]bool, len(recorded))

	// Check each expected call against all recorded calls
	for i, exp := range expected {
		for j, rec := range recorded {
			if MatchCall(rec, exp) {
				satisfiedFlags[i] = true
				matchedRecorded[j] = true
				break // Only need one match per expected call
			}
		}
	}

	// Build satisfied and missing lists
	for i, exp := range expected {
		desc := formatExpectedCall(exp)
		if satisfiedFlags[i] {
			satisfied = append(satisfied, desc)
		} else {
			missing = append(missing, desc)
		}
	}

	// Build unexpected list from unmatched recorded calls
	for i, rec := range recorded {
		if !matchedRecorded[i] {
			unexpected = append(unexpected, formatRecordedCall(rec))
		}
	}

	return satisfied, missing, unexpected
}

// formatExpectedCall creates a human-readable description of an expected call
func formatExpectedCall(exp ExpectedCall) string {
	parts := []string{exp.Tool}
	if exp.Subcommand != "" {
		parts = append(parts, exp.Subcommand)
	}
	if len(exp.RequiredArgs) > 0 {
		parts = append(parts, "with")
		parts = append(parts, exp.RequiredArgs...)
	}
	return strings.Join(parts, " ")
}

// formatRecordedCall creates a human-readable description of a recorded call
func formatRecordedCall(rec ExternalCall) string {
	if len(rec.Args) == 0 {
		return rec.Tool
	}
	return rec.Tool + " " + strings.Join(rec.Args, " ")
}
