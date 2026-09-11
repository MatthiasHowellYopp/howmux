package eval

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

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

// SetupFakeTools creates a temporary directory with fake CLI tools and returns paths
// for injecting onto PATH. It returns the tools directory path, call log file path,
// a cleanup function, and any error encountered.
//
// The cleanup function removes the temporary directory and should be deferred.
// Each call to SetupFakeTools creates an isolated temp directory to avoid collisions
// in concurrent test runs.
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
