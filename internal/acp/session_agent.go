package acp

import (
	"context"
	"errors"
	"fmt"

	"github.com/coder/acp-go-sdk"
	"github.com/matthiashowellyopp/howmux/internal/logging"
)

// modeConfigID is the session config option kiro-cli's v3 engine uses to
// select the agent for a session. Verified on kiro-cli 2.33.1: session/new
// returns a select option {id:"mode", category:"mode", currentValue:"vibe"}
// whose values include every resolvable workspace and global agent, and
// session/set_config_option {configId:"mode", value:<agent>} switches to it.
const modeConfigID acp.SessionConfigId = "mode"

// ErrAgentNotOffered is returned when the v3 engine's session does not list
// the requested agent among its selectable modes. Selecting it anyway would be
// accepted silently and run the default agent instead (verified on 2.33.1), so
// we refuse up front.
var ErrAgentNotOffered = errors.New("agent not offered by the kiro-cli v3 session")

// createSession opens a new ACP session for this client's cwd and, on the v3
// engine, selects the configured agent on it. It returns the session id.
//
// Callers hold c.mu; this method only reads c.config and c.engine, which are
// fixed once Connect succeeds.
func (c *KiroACPClient) createSession(ctx context.Context, conn *acp.ClientSideConnection) (string, error) {
	resp, err := conn.NewSession(ctx, acp.NewSessionRequest{
		Cwd:        c.config.Cwd,
		McpServers: []acp.McpServer{},
	})
	if err != nil {
		return "", err
	}
	sessionID := resp.SessionId

	if c.engine == AgentEngineV3 {
		if err := selectSessionAgent(ctx, conn, sessionID, c.config.Agent, resp.ConfigOptions); err != nil {
			return "", err
		}
	}
	return string(sessionID), nil
}

// selectSessionAgent switches a v3 session to the named agent and verifies the
// switch took. It fails loud when:
//   - the session exposes no "mode" option (the engine's agent-selection
//     contract changed, so we cannot pick the agent at all);
//   - the agent is not among the offered modes (it would silently fall back);
//   - set_config_option errors, or its response does not report the agent as
//     the current mode.
func selectSessionAgent(ctx context.Context, conn *acp.ClientSideConnection, sessionID acp.SessionId, agent string, offered []acp.SessionConfigOption) error {
	mode := findSelectOption(offered, modeConfigID)
	if mode == nil {
		return fmt.Errorf("kiro-cli v3 session did not expose a %q config option; cannot select agent %q", modeConfigID, agent)
	}
	if !selectOffers(mode, agent) {
		return fmt.Errorf("%w: %q is not a selectable mode (check <cwd>/.kiro/agents and ~/.kiro/agents, and that the config is in a format the v3 engine loads)", ErrAgentNotOffered, agent)
	}

	if mode.CurrentValue == acp.SessionConfigValueId(agent) {
		logging.Debug("v3 session already on requested agent", "agent", agent)
		return nil
	}

	setResp, err := conn.SetSessionConfigOption(ctx, acp.SetSessionConfigOptionRequest{
		ValueId: &acp.SetSessionConfigOptionValueId{
			SessionId: sessionID,
			ConfigId:  modeConfigID,
			Value:     acp.SessionConfigValueId(agent),
		},
	})
	if err != nil {
		return fmt.Errorf("selecting agent %q on v3 session: %w", agent, err)
	}

	// The response carries the full, updated option set. If it includes the
	// mode option it must now report our agent; anything else means the switch
	// did not take.
	if after := findSelectOption(setResp.ConfigOptions, modeConfigID); after != nil && after.CurrentValue != acp.SessionConfigValueId(agent) {
		return fmt.Errorf("selecting agent %q on v3 session: engine reports current mode %q", agent, after.CurrentValue)
	}

	logging.Info("selected agent on v3 session", "agent", agent, "session_id", sessionID)
	return nil
}

// findSelectOption returns the select-type config option with the given id, or
// nil if absent.
func findSelectOption(options []acp.SessionConfigOption, id acp.SessionConfigId) *acp.SessionConfigOptionSelect {
	for _, o := range options {
		if o.Select != nil && o.Select.Id == id {
			return o.Select
		}
	}
	return nil
}

// selectOffers reports whether a select option lists value among its choices,
// in either the flat or grouped option shape.
func selectOffers(opt *acp.SessionConfigOptionSelect, value string) bool {
	want := acp.SessionConfigValueId(value)
	if opt.Options.Ungrouped != nil {
		for _, o := range *opt.Options.Ungrouped {
			if o.Value == want {
				return true
			}
		}
	}
	if opt.Options.Grouped != nil {
		for _, g := range *opt.Options.Grouped {
			for _, o := range g.Options {
				if o.Value == want {
					return true
				}
			}
		}
	}
	return false
}

// ErrAbnormalStop is returned (wrapped) when an ACP turn ends with a stop
// reason other than end_turn.
var ErrAbnormalStop = errors.New("agent turn did not complete normally")

// stopReasonError maps an ACP stop reason to an error. end_turn is success.
// An empty reason is also treated as success: the field is required by the
// protocol, so an empty value means an agent that doesn't set it rather than
// a failed turn. Every other reason means the turn did not finish its work
// and must not be reported as success.
func stopReasonError(reason acp.StopReason) error {
	switch reason {
	case acp.StopReasonEndTurn, "":
		return nil
	case acp.StopReasonMaxTokens:
		return fmt.Errorf("%w: hit the model's max token limit (output is truncated)", ErrAbnormalStop)
	case acp.StopReasonMaxTurnRequests:
		return fmt.Errorf("%w: hit the max turn request limit", ErrAbnormalStop)
	case acp.StopReasonRefusal:
		return fmt.Errorf("%w: the agent refused the request", ErrAbnormalStop)
	case acp.StopReasonCancelled:
		return fmt.Errorf("%w: the turn was cancelled", ErrAbnormalStop)
	default:
		return fmt.Errorf("%w: stop reason %q", ErrAbnormalStop, reason)
	}
}
