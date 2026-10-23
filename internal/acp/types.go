package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Common errors
var (
	ErrNotConnected          = errors.New("not connected to ACP server")
	ErrAlreadyConnected      = errors.New("already connected to ACP server")
	ErrConnectionFailed      = errors.New("failed to connect to ACP server")
	ErrInvalidRequest        = errors.New("invalid request")                  // Use for MessageRequest validation
	ErrInvalidConfig         = errors.New("invalid connection configuration") // Use for ConnectionConfig validation
	ErrMissingConfigAgent    = errors.New("agent name is required in connection configuration")
	ErrMissingAgent          = errors.New("agent name is required")
	ErrMissingMessage        = errors.New("message is required")
	ErrInvalidResponseFormat = errors.New("response format must be 'json' or 'text'")
	ErrRequestTimeout        = errors.New("request timed out")
	ErrStreamingFailed       = errors.New("streaming failed")
	ErrAuthenticationFailed  = errors.New("authentication failed")
)

// MessageRequest represents a request to send a message to an ACP agent
type MessageRequest struct {
	// Agent is the name of the agent to communicate with
	Agent string `json:"agent"`

	// Message is the text message to send to the agent
	Message string `json:"message"`

	// Context provides additional context for the message
	Context map[string]interface{} `json:"context,omitempty"`

	// Streaming indicates if the response should be streamed
	Streaming bool `json:"streaming,omitempty"`

	// ResponseFormat specifies the expected response format ("json" or "text")
	ResponseFormat string `json:"response_format,omitempty"`

	// Timeout for the request
	Timeout time.Duration `json:"timeout,omitempty"`
}

// MessageResponse represents a response from an ACP agent
type MessageResponse struct {
	// Success indicates if the request was successful
	Success bool `json:"success"`

	// Message contains the response text from the agent
	Message string `json:"message,omitempty"`

	// Data contains structured response data when ResponseFormat is "json"
	Data json.RawMessage `json:"data,omitempty"`

	// Error contains error information if the request failed
	Error string `json:"error,omitempty"`

	// Metadata contains additional response metadata
	Metadata map[string]interface{} `json:"metadata,omitempty"`

	// Timestamp when the response was received
	Timestamp time.Time `json:"timestamp"`
}

// StreamingResponse represents a streaming response from an ACP agent
type StreamingResponse struct {
	// Type indicates the type of streaming data ("text", "json", "error", "done")
	Type string `json:"type"`

	// Content contains the streaming content
	Content string `json:"content,omitempty"`

	// Data contains structured data for "json" type responses
	Data json.RawMessage `json:"data,omitempty"`

	// Error contains error information for "error" type responses
	Error string `json:"error,omitempty"`

	// Timestamp when the streaming data was received
	Timestamp time.Time `json:"timestamp"`
}

// ConnectionConfig represents ACP connection configuration
type ConnectionConfig struct {
	// KiroCLIPath is the path to the Kiro CLI executable
	KiroCLIPath string `json:"kiro_cli_path,omitempty"`

	// Agent is the name of the agent to connect to
	Agent string `json:"agent,omitempty"`

	// Cwd is the working directory for the ACP session (must be absolute path)
	Cwd string `json:"cwd,omitempty"`

	// MaxRetries is the maximum number of retry attempts
	MaxRetries int `json:"max_retries,omitempty"`

	// RetryDelay is the delay between retry attempts
	RetryDelay time.Duration `json:"retry_delay,omitempty"`

	// ConnectionTimeout is the timeout for establishing connections
	ConnectionTimeout time.Duration `json:"connection_timeout,omitempty"`

	// RequestTimeout is the default timeout for requests
	RequestTimeout time.Duration `json:"request_timeout,omitempty"`

	// AgentEngine selects the kiro-cli agent engine ("v2" or "v3"). Empty means
	// "resolve at Connect time" via ResolveAgentEngine: the
	// HOWMUX_KIRO_AGENT_ENGINE env var, else DefaultAgentEngine. howmux always
	// passes the resolved engine explicitly to `kiro-cli acp`, so kiro-cli's own
	// default (which flips from v2 to v3 with CLI 3.0) never changes behavior
	// underneath us.
	AgentEngine string `json:"agent_engine,omitempty"`
}

// Supported kiro-cli agent engines.
const (
	AgentEngineV2 = "v2"
	AgentEngineV3 = "v3"

	// DefaultAgentEngine is the engine used when neither ConnectionConfig nor
	// the environment selects one. It stays on v2 (the behavior howmux was
	// built and verified against) until the v3 path has been exercised on real
	// workloads; opt in with HOWMUX_KIRO_AGENT_ENGINE=v3.
	DefaultAgentEngine = AgentEngineV2

	// AgentEngineEnvVar overrides the engine for every ACP connection that does
	// not set ConnectionConfig.AgentEngine explicitly.
	AgentEngineEnvVar = "HOWMUX_KIRO_AGENT_ENGINE"
)

// ErrInvalidAgentEngine is returned for an engine value other than v2/v3.
var ErrInvalidAgentEngine = errors.New(`agent engine must be "v2" or "v3"`)

// ResolveAgentEngine returns the engine to launch: configured (if non-empty),
// else the HOWMUX_KIRO_AGENT_ENGINE env var (if non-empty), else
// DefaultAgentEngine. Unknown values are rejected rather than passed through,
// so a typo fails loud instead of handing kiro-cli an engine it may interpret
// differently.
func ResolveAgentEngine(configured string) (string, error) {
	engine := strings.ToLower(strings.TrimSpace(configured))
	if engine == "" {
		engine = strings.ToLower(strings.TrimSpace(os.Getenv(AgentEngineEnvVar)))
	}
	if engine == "" {
		engine = DefaultAgentEngine
	}
	switch engine {
	case AgentEngineV2, AgentEngineV3:
		return engine, nil
	default:
		return "", fmt.Errorf("%w (got %q)", ErrInvalidAgentEngine, engine)
	}
}

// acpLaunchArgs returns the `kiro-cli` argv for starting the ACP server.
//
//   - v2: `acp --agent-engine v2 --agent <agent>` — the agent is chosen at
//     launch, as before.
//   - v3: `acp --agent-engine v3` — the v3 engine rejects --agent ("not
//     supported with --agent-engine=v3") and exits before initialize, so the
//     agent is selected per session instead (see selectSessionAgent).
func acpLaunchArgs(engine, agent string) []string {
	if engine == AgentEngineV3 {
		return []string{"acp", "--agent-engine", AgentEngineV3}
	}
	return []string{"acp", "--agent-engine", AgentEngineV2, "--agent", agent}
}

// DescribeLaunch returns a human-readable command line for the ACP server
// howmux would start for agent, for error reports. It resolves the engine the
// same way Connect does. On v3, where the agent is selected per session rather
// than on the command line, the agent is noted after the command. An invalid
// engine is reported inline rather than as an error, since this is for
// diagnostics only (Connect rejects it).
func DescribeLaunch(kiroCLIPath, configuredEngine, agent string) string {
	if kiroCLIPath == "" {
		kiroCLIPath = "kiro-cli"
	}
	engine, err := ResolveAgentEngine(configuredEngine)
	if err != nil {
		return fmt.Sprintf("%s acp --agent %s (%v)", kiroCLIPath, agent, err)
	}
	cmd := kiroCLIPath + " " + strings.Join(acpLaunchArgs(engine, agent), " ")
	if engine == AgentEngineV3 {
		cmd += fmt.Sprintf(" (session mode: %s)", agent)
	}
	return cmd
}

// Client defines the interface for ACP client operations
type Client interface {
	// Connect establishes a connection to the ACP server
	Connect(ctx context.Context) error

	// Disconnect closes the connection to the ACP server
	Disconnect() error

	// IsConnected returns true if connected to the ACP server
	IsConnected() bool

	// SendMessage sends a message to an agent and returns the response
	SendMessage(ctx context.Context, req *MessageRequest) (*MessageResponse, error)

	// StreamMessage sends a message to an agent and returns a streaming response
	StreamMessage(ctx context.Context, req *MessageRequest) (<-chan *StreamingResponse, error)

	// Close closes the client and cleans up resources
	Close() error
}

// StreamReader wraps an io.Reader to provide streaming response parsing
type StreamReader interface {
	io.Reader

	// ReadResponse reads the next streaming response
	ReadResponse() (*StreamingResponse, error)

	// IsDone returns true if the stream is complete
	IsDone() bool
}

// AuthProvider defines the interface for ACP authentication
type AuthProvider interface {
	// GetCredentials returns the authentication credentials
	GetCredentials(ctx context.Context) (map[string]string, error)

	// RefreshCredentials refreshes the authentication credentials
	RefreshCredentials(ctx context.Context) error
}

// DefaultConnectionConfig returns default connection configuration
func DefaultConnectionConfig() *ConnectionConfig {
	cwd, err := os.Getwd()
	if err != nil {
		// Use absolute fallback when Getwd fails (e.g., dir deleted, permission issues)
		cwd = os.TempDir()
	}

	// Ensure absolute path unconditionally
	if !filepath.IsAbs(cwd) {
		absCwd, err := filepath.Abs(cwd)
		if err != nil {
			// Final fallback to temp dir if Abs fails
			cwd = os.TempDir()
		} else {
			cwd = absCwd
		}
	}

	return &ConnectionConfig{
		KiroCLIPath:       "kiro-cli",
		Agent:             "", // Must be set explicitly by caller
		Cwd:               cwd,
		MaxRetries:        3,
		RetryDelay:        1 * time.Second,
		ConnectionTimeout: 30 * time.Second,
		RequestTimeout:    60 * time.Second,
	}
}

// ValidateConnectionConfig validates a ConnectionConfig
func ValidateConnectionConfig(config *ConnectionConfig) error {
	if config == nil {
		return ErrInvalidConfig
	}

	if config.Agent == "" {
		return ErrMissingConfigAgent
	}

	if config.KiroCLIPath == "" {
		return errors.New("kiro-cli path is required")
	}

	if config.Cwd == "" {
		return errors.New("working directory (cwd) is required")
	}

	if !filepath.IsAbs(config.Cwd) {
		return errors.New("working directory (cwd) must be an absolute path")
	}

	if config.ConnectionTimeout <= 0 || config.RequestTimeout <= 0 {
		return errors.New("timeouts must be positive")
	}

	if config.AgentEngine != "" {
		if _, err := ResolveAgentEngine(config.AgentEngine); err != nil {
			return err
		}
	}

	return nil
}

// ValidateAgentResolvable verifies that the named agent actually exists as a
// config under the working directory's .kiro/agents before a session is
// started. This is the pre-flight guard for the silent-fallback problem: over
// ACP, kiro-cli resolves an unknown agent by silently falling back to a default
// client — Connect and the session both succeed, and no "no agent with name"
// signal is surfaced over the protocol or on stderr (verified against
// kiro-cli 2.21.4, and again on 2.33.1 for both engines: v2 with an unknown
// --agent, and v3 accepting session/set_config_option mode=<unknown>). So
// detection after the fact is impossible; we instead fail loud up front by
// checking the same file kiro-cli resolves the agent from
// (<cwd>/.kiro/agents/<agent>.json). On the v3 engine, selectSessionAgent adds
// a second, protocol-level check against the agents the session offers. This
// restores the trustworthiness guarantee from issue #37 in a way that does not
// depend on kiro-cli's ACP error reporting.
func ValidateAgentResolvable(cwd, agent string) error {
	if agent == "" {
		return ErrMissingConfigAgent
	}
	agentFile := filepath.Join(cwd, ".kiro", "agents", agent+".json")
	if _, err := os.Stat(agentFile); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("agent %q could not be resolved: no config at %s (kiro-cli would silently fall back to a default client)", agent, agentFile)
		}
		return fmt.Errorf("agent %q could not be resolved: %w", agent, err)
	}
	return nil
}

// ValidateMessageRequest validates a MessageRequest
func ValidateMessageRequest(req *MessageRequest) error {
	if req == nil {
		return ErrInvalidRequest
	}

	if req.Message == "" {
		return ErrMissingMessage
	}

	if req.ResponseFormat != "" && req.ResponseFormat != "json" && req.ResponseFormat != "text" {
		return ErrInvalidResponseFormat
	}

	return nil
}
