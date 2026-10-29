package kirocli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// ListModels returns the model ids kiro-cli accepts for --model, from
// `kiro-cli chat --list-models --format json`. The list is whatever the
// signed-in account can use right now, so it is the ground truth for
// validating a configured model id.
func ListModels(ctx context.Context) ([]string, error) {
	cmd := exec.CommandContext(ctx, cliPath, "chat", "--list-models", "--format", "json")
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("kiro-cli --list-models exited %d: %s", exitErr.ExitCode(), tail(stderr.String()))
		}
		return nil, fmt.Errorf("kiro-cli --list-models: %w", err)
	}
	return parseModelList(stdout.String())
}

// parseModelList extracts model ids from the --list-models JSON document:
// {"models":[{"model_id":"auto",...},...]}.
func parseModelList(out string) ([]string, error) {
	var doc struct {
		Models []struct {
			ModelID string `json:"model_id"`
		} `json:"models"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &doc); err != nil {
		return nil, fmt.Errorf("parse kiro-cli --list-models output: %w", err)
	}
	ids := make([]string, 0, len(doc.Models))
	for _, m := range doc.Models {
		if id := strings.TrimSpace(m.ModelID); id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil, errors.New("kiro-cli --list-models returned no models")
	}
	return ids, nil
}
