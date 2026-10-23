package acp

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/acp-go-sdk"
)

// --- engine resolution and launch argv --------------------------------------

func TestResolveAgentEngine(t *testing.T) {
	tests := []struct {
		name       string
		configured string
		env        string
		want       string
		wantErr    bool
	}{
		{name: "default when unset", want: AgentEngineV3},
		{name: "env selects v3", env: "v3", want: AgentEngineV3},
		{name: "env is case/space tolerant", env: "  V3 ", want: AgentEngineV3},
		{name: "config wins over env", configured: "v2", env: "v3", want: AgentEngineV2},
		{name: "config v3", configured: "v3", want: AgentEngineV3},
		{name: "invalid config rejected", configured: "v4", wantErr: true},
		{name: "invalid env rejected", env: "classic", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(AgentEngineEnvVar, tt.env)
			got, err := ResolveAgentEngine(tt.configured)
			if tt.wantErr {
				if !errors.Is(err, ErrInvalidAgentEngine) {
					t.Fatalf("err = %v, want ErrInvalidAgentEngine", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestACPLaunchArgs(t *testing.T) {
	// v2 keeps the launch-time --agent and pins the engine explicitly.
	if got, want := acpLaunchArgs(AgentEngineV2, "krew-lead"), []string{"acp", "--agent-engine", "v2", "--agent", "krew-lead"}; !reflect.DeepEqual(got, want) {
		t.Errorf("v2 args = %v, want %v", got, want)
	}
	// v3 must NOT pass --agent: the v3 engine rejects it and exits before
	// initialize ("not supported with --agent-engine=v3").
	got := acpLaunchArgs(AgentEngineV3, "krew-lead")
	if want := []string{"acp", "--agent-engine", "v3"}; !reflect.DeepEqual(got, want) {
		t.Errorf("v3 args = %v, want %v", got, want)
	}
	for _, a := range got {
		if a == "--agent" {
			t.Errorf("v3 args must not contain --agent: %v", got)
		}
	}
}

func TestDescribeLaunch(t *testing.T) {
	t.Setenv(AgentEngineEnvVar, "")
	tests := []struct {
		name, path, engine, want string
	}{
		{"v3 default", "", "", "kiro-cli acp --agent-engine v3 (session mode: architect)"},
		{"v2 custom path", "/opt/kiro-cli", "v2", "/opt/kiro-cli acp --agent-engine v2 --agent architect"},
		{"v3 notes session mode", "kiro-cli", "v3", "kiro-cli acp --agent-engine v3 (session mode: architect)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DescribeLaunch(tt.path, tt.engine, "architect"); got != tt.want {
				t.Errorf("DescribeLaunch = %q, want %q", got, tt.want)
			}
		})
	}
	if got := DescribeLaunch("kiro-cli", "v9", "architect"); !strings.Contains(got, "v9") {
		t.Errorf("invalid engine should be reported inline, got %q", got)
	}
}

func TestValidateConnectionConfig_AgentEngine(t *testing.T) {
	base := ConnectionConfig{KiroCLIPath: "kiro-cli", Agent: "planner", Cwd: "/tmp", ConnectionTimeout: time.Second, RequestTimeout: time.Second}

	ok := base
	ok.AgentEngine = "v3"
	if err := ValidateConnectionConfig(&ok); err != nil {
		t.Errorf("v3 should validate, got %v", err)
	}

	bad := base
	bad.AgentEngine = "v9"
	if err := ValidateConnectionConfig(&bad); !errors.Is(err, ErrInvalidAgentEngine) {
		t.Errorf("v9 err = %v, want ErrInvalidAgentEngine", err)
	}
}

// --- permission selection ---------------------------------------------------

func perm(id, kind string) acp.PermissionOption {
	return acp.PermissionOption{OptionId: acp.PermissionOptionId(id), Name: id, Kind: acp.PermissionOptionKind(kind)}
}

func TestSelectPermissionOption(t *testing.T) {
	tests := []struct {
		name      string
		options   []acp.PermissionOption
		wantID    string
		wantAllow bool
	}{
		{
			// Exact order kiro-cli's v3 engine sent on 2.33.1.
			name:      "v3 observed order picks allow_once",
			options:   []acp.PermissionOption{perm("accept", "allow_once"), perm("always-accept", "allow_always"), perm("reject", "reject_once"), perm("always-reject", "reject_always")},
			wantID:    "accept",
			wantAllow: true,
		},
		{
			// The regression the old "allow" match hid: reject listed first
			// must NOT be chosen.
			name:      "reject first still approves",
			options:   []acp.PermissionOption{perm("reject", "reject_once"), perm("always-reject", "reject_always"), perm("accept", "allow_once")},
			wantID:    "accept",
			wantAllow: true,
		},
		{
			name:      "only allow_always available",
			options:   []acp.PermissionOption{perm("reject", "reject_once"), perm("always-accept", "allow_always")},
			wantID:    "always-accept",
			wantAllow: true,
		},
		{
			name:      "allow_once preferred over allow_always regardless of order",
			options:   []acp.PermissionOption{perm("always-accept", "allow_always"), perm("accept", "allow_once")},
			wantID:    "accept",
			wantAllow: true,
		},
		{
			name:      "no allow kind falls back to first",
			options:   []acp.PermissionOption{perm("reject", "reject_once"), perm("always-reject", "reject_always")},
			wantID:    "reject",
			wantAllow: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, isAllow := selectPermissionOption(tt.options)
			if string(got.OptionId) != tt.wantID || isAllow != tt.wantAllow {
				t.Errorf("got (%s, %v), want (%s, %v)", got.OptionId, isAllow, tt.wantID, tt.wantAllow)
			}
		})
	}
}

func TestRequestPermission_ApprovesByKind(t *testing.T) {
	k := &KiroClient{autoApprove: true}
	resp, err := k.RequestPermission(context.Background(), acp.RequestPermissionRequest{
		Options: []acp.PermissionOption{perm("reject", "reject_once"), perm("accept", "allow_once")},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Outcome.Selected == nil || resp.Outcome.Selected.OptionId != "accept" {
		t.Errorf("selected %+v, want optionId accept", resp.Outcome.Selected)
	}
}

// --- v3 session agent selection against a fake ACP agent --------------------

// fakeV3Agent is an in-process ACP agent speaking real JSON-RPC over pipes via
// the SDK, so these tests exercise the actual session/new and
// session/set_config_option wire calls without a kiro-cli binary.
type fakeV3Agent struct {
	newSessionResp acp.NewSessionResponse
	setResp        acp.SetSessionConfigOptionResponse
	setErr         error
	stopReason     acp.StopReason // Prompt's stop reason; empty means end_turn

	mu       sync.Mutex
	setCalls []acp.SetSessionConfigOptionValueId
}

func (f *fakeV3Agent) Authenticate(context.Context, acp.AuthenticateRequest) (acp.AuthenticateResponse, error) {
	return acp.AuthenticateResponse{}, nil
}
func (f *fakeV3Agent) Initialize(context.Context, acp.InitializeRequest) (acp.InitializeResponse, error) {
	return acp.InitializeResponse{ProtocolVersion: acp.ProtocolVersionNumber}, nil
}
func (f *fakeV3Agent) Logout(context.Context, acp.LogoutRequest) (acp.LogoutResponse, error) {
	return acp.LogoutResponse{}, nil
}
func (f *fakeV3Agent) Cancel(context.Context, acp.CancelNotification) error { return nil }
func (f *fakeV3Agent) CloseSession(context.Context, acp.CloseSessionRequest) (acp.CloseSessionResponse, error) {
	return acp.CloseSessionResponse{}, nil
}
func (f *fakeV3Agent) ListSessions(context.Context, acp.ListSessionsRequest) (acp.ListSessionsResponse, error) {
	return acp.ListSessionsResponse{}, nil
}
func (f *fakeV3Agent) NewSession(context.Context, acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	return f.newSessionResp, nil
}
func (f *fakeV3Agent) Prompt(context.Context, acp.PromptRequest) (acp.PromptResponse, error) {
	if f.stopReason != "" {
		return acp.PromptResponse{StopReason: f.stopReason}, nil
	}
	return acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
}
func (f *fakeV3Agent) ResumeSession(context.Context, acp.ResumeSessionRequest) (acp.ResumeSessionResponse, error) {
	return acp.ResumeSessionResponse{}, nil
}
func (f *fakeV3Agent) SetSessionConfigOption(_ context.Context, p acp.SetSessionConfigOptionRequest) (acp.SetSessionConfigOptionResponse, error) {
	f.mu.Lock()
	if p.ValueId != nil {
		f.setCalls = append(f.setCalls, *p.ValueId)
	}
	f.mu.Unlock()
	return f.setResp, f.setErr
}
func (f *fakeV3Agent) SetSessionMode(context.Context, acp.SetSessionModeRequest) (acp.SetSessionModeResponse, error) {
	return acp.SetSessionModeResponse{}, nil
}

func (f *fakeV3Agent) calls() []acp.SetSessionConfigOptionValueId {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]acp.SetSessionConfigOptionValueId(nil), f.setCalls...)
}

var _ acp.Agent = (*fakeV3Agent)(nil)

// connectFake wires a real SDK client connection to the fake agent over pipes.
func connectFake(t *testing.T, agent acp.Agent) *acp.ClientSideConnection {
	t.Helper()
	return connectFakeWith(t, agent, &KiroClient{autoApprove: true})
}

// connectFakeWith is connectFake with a caller-supplied client handler, for
// tests that need the KiroACPClient and the connection to share it.
func connectFakeWith(t *testing.T, agent acp.Agent, client *KiroClient) *acp.ClientSideConnection {
	t.Helper()
	c2aR, c2aW := io.Pipe() // client -> agent
	a2cR, a2cW := io.Pipe() // agent -> client
	_ = acp.NewAgentSideConnection(agent, a2cW, c2aR)
	conn := acp.NewClientSideConnection(client, c2aW, a2cR)
	t.Cleanup(func() {
		c2aW.Close()
		a2cW.Close()
	})
	return conn
}

// modeOption builds a v3-style "mode" select option with the given current
// value and choices.
func modeOption(current string, choices ...string) acp.SessionConfigOption {
	opts := make(acp.SessionConfigSelectOptionsUngrouped, 0, len(choices))
	for _, c := range choices {
		opts = append(opts, acp.SessionConfigSelectOption{Name: c, Value: acp.SessionConfigValueId(c)})
	}
	return acp.SessionConfigOption{Select: &acp.SessionConfigOptionSelect{
		Id:           modeConfigID,
		Name:         "Mode",
		Type:         "select",
		CurrentValue: acp.SessionConfigValueId(current),
		Options:      acp.SessionConfigSelectOptions{Ungrouped: &opts},
	}}
}

func v3Client(agent string) *KiroACPClient {
	return &KiroACPClient{
		config: &ConnectionConfig{Agent: agent, Cwd: "/tmp"},
		engine: AgentEngineV3,
	}
}

func ctxT(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestCreateSession_V3SelectsAgent(t *testing.T) {
	fake := &fakeV3Agent{
		newSessionResp: acp.NewSessionResponse{
			SessionId:     "sess-1",
			ConfigOptions: []acp.SessionConfigOption{modeOption("vibe", "vibe", "planner", "krew-lead")},
		},
		setResp: acp.SetSessionConfigOptionResponse{
			ConfigOptions: []acp.SessionConfigOption{modeOption("planner", "vibe", "planner", "krew-lead")},
		},
	}
	conn := connectFake(t, fake)

	id, err := v3Client("planner").createSession(ctxT(t), conn)
	if err != nil {
		t.Fatalf("createSession: %v", err)
	}
	if id != "sess-1" {
		t.Errorf("session id = %q, want sess-1", id)
	}
	calls := fake.calls()
	if len(calls) != 1 {
		t.Fatalf("set_config_option calls = %d, want 1", len(calls))
	}
	if c := calls[0]; c.SessionId != "sess-1" || c.ConfigId != modeConfigID || c.Value != "planner" {
		t.Errorf("set call = %+v, want session sess-1 mode=planner", c)
	}
}

func TestCreateSession_V3AgentNotOfferedFailsLoud(t *testing.T) {
	fake := &fakeV3Agent{
		newSessionResp: acp.NewSessionResponse{
			SessionId:     "sess-1",
			ConfigOptions: []acp.SessionConfigOption{modeOption("vibe", "vibe", "builder")},
		},
	}
	conn := connectFake(t, fake)

	_, err := v3Client("planner").createSession(ctxT(t), conn)
	if !errors.Is(err, ErrAgentNotOffered) {
		t.Fatalf("err = %v, want ErrAgentNotOffered", err)
	}
	// Must refuse BEFORE asking the engine to switch — the engine would accept
	// an unknown mode silently and run the default agent.
	if n := len(fake.calls()); n != 0 {
		t.Errorf("set_config_option called %d times for an unoffered agent; want 0", n)
	}
}

func TestCreateSession_V3NoModeOptionFailsLoud(t *testing.T) {
	fake := &fakeV3Agent{newSessionResp: acp.NewSessionResponse{SessionId: "sess-1"}}
	conn := connectFake(t, fake)

	_, err := v3Client("planner").createSession(ctxT(t), conn)
	if err == nil || !strings.Contains(err.Error(), `"mode" config option`) {
		t.Fatalf("err = %v, want missing mode option error", err)
	}
}

func TestCreateSession_V3SwitchNotConfirmedFailsLoud(t *testing.T) {
	fake := &fakeV3Agent{
		newSessionResp: acp.NewSessionResponse{
			SessionId:     "sess-1",
			ConfigOptions: []acp.SessionConfigOption{modeOption("vibe", "vibe", "planner")},
		},
		// Engine acknowledges but reports it is still on the default.
		setResp: acp.SetSessionConfigOptionResponse{
			ConfigOptions: []acp.SessionConfigOption{modeOption("vibe", "vibe", "planner")},
		},
	}
	conn := connectFake(t, fake)

	_, err := v3Client("planner").createSession(ctxT(t), conn)
	if err == nil || !strings.Contains(err.Error(), `current mode "vibe"`) {
		t.Fatalf("err = %v, want unconfirmed-switch error", err)
	}
}

func TestCreateSession_V3SetErrorPropagates(t *testing.T) {
	fake := &fakeV3Agent{
		newSessionResp: acp.NewSessionResponse{
			SessionId:     "sess-1",
			ConfigOptions: []acp.SessionConfigOption{modeOption("vibe", "vibe", "planner")},
		},
		setErr: errors.New("boom"),
	}
	conn := connectFake(t, fake)

	_, err := v3Client("planner").createSession(ctxT(t), conn)
	if err == nil || !strings.Contains(err.Error(), `selecting agent "planner"`) {
		t.Fatalf("err = %v, want wrapped set_config_option error", err)
	}
}

func TestCreateSession_V3AlreadyOnAgentSkipsSwitch(t *testing.T) {
	fake := &fakeV3Agent{
		newSessionResp: acp.NewSessionResponse{
			SessionId:     "sess-1",
			ConfigOptions: []acp.SessionConfigOption{modeOption("planner", "vibe", "planner")},
		},
	}
	conn := connectFake(t, fake)

	if _, err := v3Client("planner").createSession(ctxT(t), conn); err != nil {
		t.Fatalf("createSession: %v", err)
	}
	if n := len(fake.calls()); n != 0 {
		t.Errorf("set_config_option called %d times when already on agent; want 0", n)
	}
}

func TestCreateSession_V2NeverSwitchesMode(t *testing.T) {
	// The v2 engine selects the agent at launch (--agent); even if a v2 session
	// advertised a mode option, createSession must not touch it.
	fake := &fakeV3Agent{
		newSessionResp: acp.NewSessionResponse{
			SessionId:     "sess-2",
			ConfigOptions: []acp.SessionConfigOption{modeOption("vibe", "vibe", "planner")},
		},
	}
	conn := connectFake(t, fake)
	c := v3Client("planner")
	c.engine = AgentEngineV2

	id, err := c.createSession(ctxT(t), conn)
	if err != nil || id != "sess-2" {
		t.Fatalf("createSession = (%q, %v), want (sess-2, nil)", id, err)
	}
	if n := len(fake.calls()); n != 0 {
		t.Errorf("v2 path called set_config_option %d times; want 0", n)
	}
}

func TestSelectOffers_Grouped(t *testing.T) {
	groups := acp.SessionConfigSelectOptionsGrouped{
		{Group: "workspace", Name: "Workspace", Options: []acp.SessionConfigSelectOption{{Name: "planner", Value: "planner"}}},
		{Group: "global", Name: "Global", Options: []acp.SessionConfigSelectOption{{Name: "humanizer", Value: "humanizer"}}},
	}
	opt := &acp.SessionConfigOptionSelect{Id: modeConfigID, Options: acp.SessionConfigSelectOptions{Grouped: &groups}}
	if !selectOffers(opt, "humanizer") {
		t.Error("grouped option humanizer should be offered")
	}
	if selectOffers(opt, "missing") {
		t.Error("missing should not be offered")
	}
}

// streamWithStopReason runs a full StreamMessage turn against the fake agent
// returning the given stop reason, and returns every event received.
func streamWithStopReason(t *testing.T, reason acp.StopReason) []*StreamingResponse {
	t.Helper()
	kc := &KiroClient{autoApprove: true}
	conn := connectFakeWith(t, &fakeV3Agent{stopReason: reason}, kc)
	c := &KiroACPClient{
		config:    &ConnectionConfig{Agent: "planner", Cwd: "/tmp"},
		engine:    AgentEngineV2,
		client:    kc,
		conn:      conn,
		connected: true,
		sessionID: "sess-1",
	}
	ch, err := c.StreamMessage(ctxT(t), &MessageRequest{Message: "hi", Streaming: true})
	if err != nil {
		t.Fatalf("StreamMessage: %v", err)
	}
	var events []*StreamingResponse
	for ev := range ch {
		events = append(events, ev)
	}
	return events
}

func terminalEvents(events []*StreamingResponse) []*StreamingResponse {
	var out []*StreamingResponse
	for _, ev := range events {
		if ev.Type == "done" || ev.Type == "error" {
			out = append(out, ev)
		}
	}
	return out
}

func TestStreamMessage_EndTurnIsDone(t *testing.T) {
	term := terminalEvents(streamWithStopReason(t, acp.StopReasonEndTurn))
	if len(term) != 1 || term[0].Type != "done" {
		t.Fatalf("terminal events = %+v, want exactly one done", term)
	}
	if term[0].StopReason != "end_turn" {
		t.Errorf("StopReason = %q, want end_turn", term[0].StopReason)
	}
}

// TestStreamMessage_AbnormalStopIsError: refusal, truncation, turn limits and
// cancellation must end the stream with a single "error" (never "done"), so
// consumers such as the manager (which sets exit 0 on "done") cannot report
// an unfinished turn as success.
func TestStreamMessage_AbnormalStopIsError(t *testing.T) {
	for _, reason := range []acp.StopReason{
		acp.StopReasonRefusal, acp.StopReasonMaxTokens, acp.StopReasonMaxTurnRequests, acp.StopReasonCancelled, "something_new",
	} {
		t.Run(string(reason), func(t *testing.T) {
			term := terminalEvents(streamWithStopReason(t, reason))
			if len(term) != 1 || term[0].Type != "error" {
				t.Fatalf("terminal events = %+v, want exactly one error", term)
			}
			if term[0].StopReason != string(reason) {
				t.Errorf("StopReason = %q, want %q", term[0].StopReason, reason)
			}
			if !strings.Contains(term[0].Error, ErrAbnormalStop.Error()) {
				t.Errorf("Error = %q, want it to mention %q", term[0].Error, ErrAbnormalStop)
			}
		})
	}
}

func TestStopReasonError(t *testing.T) {
	for _, ok := range []acp.StopReason{acp.StopReasonEndTurn, ""} {
		if err := stopReasonError(ok); err != nil {
			t.Errorf("stopReasonError(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []acp.StopReason{acp.StopReasonRefusal, acp.StopReasonMaxTokens, acp.StopReasonMaxTurnRequests, acp.StopReasonCancelled, "x"} {
		if err := stopReasonError(bad); !errors.Is(err, ErrAbnormalStop) {
			t.Errorf("stopReasonError(%q) = %v, want ErrAbnormalStop", bad, err)
		}
	}
}
