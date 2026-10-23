// Package kirocli runs one-shot kiro-cli agent turns and parses their
// `--output-format stream-json` output.
//
// It is the Go port of ai-resources' workflows/kiro_oneshot.py
// (kiro_oneshot_meta + _parse_acp_events) and must stay in lockstep with it:
// same argv shape, same event handling on both agent engines, same error
// conditions. The parser fixtures in oneshot_test.go mirror
// workflows/tests/test_oneshot_parse.py case for case.
//
// Stream-json across agent engines (verified on kiro-cli 2.33.1):
//
//   - Both engines: JSON Lines of ACP events. `runStarted.data.engine` names
//     the engine ("v2" / "v3"); the answer arrives as `agent_message_chunk`
//     session updates and, complete, as `runFinished.data.finalText`, along
//     with `stopReason` and `status`.
//   - V2: per-turn metadata comes in `metadata` events (`turnDurationMs`,
//     `contextUsagePercentage`); the last value wins.
//   - V3: no `metadata` event. The same signals arrive as
//     `session_info_update` updates under `_meta.kiro`, with
//     `kind: turn_completion` (`elapsedTime`, ms) and `kind: context_usage`
//     (`usagePercentage`).
//
// Neither engine reports billed tokens or cost, so there is nothing here for
// them; audit records keep those fields as explicit null.
//
// The engine is whatever kiro-cli's `chat.agentEngine` setting selects; one-
// shot calls do not pin it (matching kiro_oneshot.py), and the engine that
// actually ran is reported in Result.Engine.
package kirocli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"unicode/utf8"
)

// ansiRe matches CSI and OSC escape sequences (kiro_oneshot.py's _ANSI_RE).
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)

// StripANSI removes ANSI escape sequences and carriage returns.
func StripANSI(text string) string {
	return strings.ReplaceAll(ansiRe.ReplaceAllString(text, ""), "\r", "")
}

// Meta is the per-turn metadata kiro-cli exposes in stream-json. Pointer
// fields are nil when the stream did not carry the value.
type Meta struct {
	TurnDurationMs  *float64 // V2 metadata.turnDurationMs / V3 turn_completion.elapsedTime
	ContextUsagePct *float64 // V2 metadata.contextUsagePercentage / V3 context_usage.usagePercentage
	StopReason      string   // runFinished.stopReason ("" if absent)
	Status          string   // runFinished.status ("" if absent)
	SessionID       string
	Engine          string // runStarted.engine: "v2" | "v3" ("" if absent)

	// RunError is the last runError event's "<stage>: <message>" ("" if
	// none). In stream-json mode both engines report failures (e.g. an
	// unknown agent) this way on stdout; stderr is empty on v2 and engine
	// logging on v3, so this is the useful error text.
	RunError string
}

// Result is one turn's answer plus its metadata.
type Result struct {
	Text string
	Meta
}

type event struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

type eventData struct {
	SessionID string `json:"sessionId"`

	// runStarted
	Engine string `json:"engine"`

	// metadata (V2)
	TurnDurationMs         *float64 `json:"turnDurationMs"`
	ContextUsagePercentage *float64 `json:"contextUsagePercentage"`

	// runError
	Stage   string `json:"stage"`
	Message string `json:"message"`

	// runFinished
	Status             string `json:"status"`
	StopReason         string `json:"stopReason"`
	FinalText          string `json:"finalText"`
	FinalTextTruncated bool   `json:"finalTextTruncated"`

	// sessionUpdate
	Update *struct {
		SessionUpdate string `json:"sessionUpdate"`
		Content       *struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Meta *struct {
			Kiro *struct {
				Kind            string   `json:"kind"`
				ElapsedTime     *float64 `json:"elapsedTime"`
				UsagePercentage *float64 `json:"usagePercentage"`
			} `json:"kiro"`
		} `json:"_meta"`
	} `json:"update"`
}

// ParseStreamJSON parses `--output-format stream-json` stdout and returns the
// answer text (trimmed) and metadata, handling both agent engines. Malformed
// or non-object lines are skipped: a partial parse beats failing the call
// over one bad line. runFinished.finalText is preferred when present and not
// truncated; otherwise the agent_message_chunk text is used.
func ParseStreamJSON(stdout string) (string, Meta) {
	var (
		meta        Meta
		chunks      strings.Builder
		finished    *eventData
		lastSession string
	)

	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue // malformed, or valid JSON that isn't an object
		}
		var d eventData
		if len(ev.Data) > 0 && !bytes.Equal(ev.Data, []byte("null")) {
			if err := json.Unmarshal(ev.Data, &d); err != nil {
				continue
			}
		}

		switch ev.Type {
		case "runStarted":
			if d.Engine != "" {
				meta.Engine = d.Engine
			}
		case "metadata": // V2
			if d.TurnDurationMs != nil {
				meta.TurnDurationMs = d.TurnDurationMs
			}
			if d.ContextUsagePercentage != nil {
				meta.ContextUsagePct = d.ContextUsagePercentage
			}
			if d.SessionID != "" {
				lastSession = d.SessionID
			}
		case "runFinished":
			dd := d
			finished = &dd
		case "runError":
			if d.Message != "" {
				meta.RunError = d.Message
				if d.Stage != "" {
					meta.RunError = d.Stage + ": " + d.Message
				}
			}
		case "sessionUpdate":
			if u := d.Update; u != nil {
				switch u.SessionUpdate {
				case "agent_message_chunk":
					if u.Content != nil && u.Content.Type == "text" {
						chunks.WriteString(u.Content.Text)
					}
				case "session_info_update": // V3
					if u.Meta != nil && u.Meta.Kiro != nil {
						k := u.Meta.Kiro
						switch {
						case k.Kind == "turn_completion" && k.ElapsedTime != nil:
							meta.TurnDurationMs = k.ElapsedTime
						case k.Kind == "context_usage" && k.UsagePercentage != nil:
							meta.ContextUsagePct = k.UsagePercentage
						}
					}
				}
			}
			if d.SessionID != "" {
				lastSession = d.SessionID
			}
		}
	}

	text := chunks.String()
	meta.SessionID = lastSession
	if finished != nil {
		if finished.FinalText != "" && !finished.FinalTextTruncated {
			text = finished.FinalText
		}
		meta.StopReason = finished.StopReason
		meta.Status = finished.Status
		if finished.SessionID != "" {
			meta.SessionID = finished.SessionID
		}
	}
	return strings.TrimSpace(text), meta
}

// Request describes one one-shot turn.
type Request struct {
	Agent string // --agent; "" runs kiro-cli's default agent
	Model string // --model; "" passes none
	// Prompt is passed as the final argv element, or on stdin when
	// PromptOnStdin is set (both work on v2 and v3).
	Prompt        string
	PromptOnStdin bool
	// TrustAllTools passes --trust-all-tools (kiro_oneshot.py always does).
	// Off by default so a caller that never needed tools (the eval LLM
	// judge) doesn't silently gain them.
	TrustAllTools bool
}

// Args returns the kiro-cli argv for req (excluding the binary).
func Args(req Request) []string {
	args := []string{"chat", "--no-interactive"}
	if req.TrustAllTools {
		args = append(args, "--trust-all-tools")
	}
	if req.Agent != "" {
		args = append(args, "--agent", req.Agent)
	}
	args = append(args, "--output-format", "stream-json")
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	if !req.PromptOnStdin {
		args = append(args, req.Prompt)
	}
	return args
}

// ErrNoAnswer is returned (wrapped) when kiro-cli exits 0 but the stream has
// no answer text.
var ErrNoAnswer = errors.New("kiro-cli produced no answer")

// tailLen is how much of stderr/stdout is kept in error messages. The v3
// engine logs heavily to stderr, so a tail keeps errors readable while still
// showing the final failure line (matches kiro_oneshot.py's [-500:]).
const tailLen = 500

func tail(s string) string {
	s = strings.TrimSpace(StripANSI(s))
	if len(s) > tailLen {
		i := len(s) - tailLen
		for i < len(s) && !utf8.RuneStart(s[i]) {
			i++ // don't cut a multi-byte rune in half
		}
		s = "…" + s[i:]
	}
	return s
}

func agentLabel(agent string) string {
	if agent == "" {
		return "default agent"
	}
	return fmt.Sprintf("agent %q", agent)
}

// cliPath is the kiro-cli binary. Package var so tests can point it at a fake.
var cliPath = "kiro-cli"

// Run executes one non-interactive kiro-cli turn and returns the parsed
// answer and metadata. It errors on a non-zero exit or an empty answer, the
// two KiroOneshotError conditions in kiro_oneshot.py. The error carries the
// stream's runError message when there is one, else a stderr (exit) or
// stdout (empty answer) tail. A non-end_turn stop reason is NOT an error here,
// matching the Python reference: it is reported in Result.StopReason for the
// caller to act on.
//
// Stdin is never the controlling tty: it is the prompt when PromptOnStdin is
// set and empty otherwise (kiro_oneshot.py's stdin=DEVNULL), so terminal-
// control queries kiro-cli emits get no stray replies.
func Run(ctx context.Context, req Request) (Result, error) {
	cmd := exec.CommandContext(ctx, cliPath, Args(req)...)
	if req.PromptOnStdin {
		cmd.Stdin = strings.NewReader(req.Prompt)
	}
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	text, meta := ParseStreamJSON(stdout.String())

	if runErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(runErr, &exitErr) {
			return Result{Meta: meta}, fmt.Errorf("kiro-cli failed for %s: %w", agentLabel(req.Agent), runErr)
		}
		detail := "stderr tail: " + tail(stderr.String())
		if meta.RunError != "" {
			detail = meta.RunError
		}
		return Result{Meta: meta}, fmt.Errorf("kiro-cli exited %d for %s: %s: %w",
			exitErr.ExitCode(), agentLabel(req.Agent), detail, runErr)
	}

	if text == "" {
		detail := "stdout tail: " + tail(stdout.String())
		if meta.RunError != "" {
			detail = meta.RunError
		}
		return Result{Meta: meta}, fmt.Errorf("%w for %s: %s", ErrNoAnswer, agentLabel(req.Agent), detail)
	}
	return Result{Text: text, Meta: meta}, nil
}
