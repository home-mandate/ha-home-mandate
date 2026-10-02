// SPDX-License-Identifier: AGPL-3.0-or-later

// Package mcp is the MCP server for agents and the Policy Enforcement Point. Every tool
// call that touches a device goes token → availability → PDP → rate limit → (ask is
// refused until approval requests exist) → execution → audit log. Administrative
// functions do not exist here.
package mcp

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"time"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mandate-spec/mandate-spec/evaluator"

	"github.com/home-mandate/home-mandate/internal/agent"
	"github.com/home-mandate/home-mandate/internal/audit"
	"github.com/home-mandate/home-mandate/internal/catalog"
	"github.com/home-mandate/home-mandate/internal/ha"
	"github.com/home-mandate/home-mandate/internal/pdp"
)

// Path is where the MCP endpoint is served.
const Path = "/mcp"

const (
	maxRequestBytes    = 64 << 10
	defaultCallTimeout = 10 * time.Second
)

// Error codes returned to agents: short, without internal details (ARCHITECTURE §5).
const (
	codeNotFound         = "not_found"
	codeDenied           = "denied"
	codeApprovalRequired = "approval_required"
	codeRateLimited      = "rate_limited"
	codeUnavailable      = "unavailable"
	codeInvalidParams    = "invalid_params"
	codeNotSupported     = "not_supported"
	codeFailed           = "failed"
)

var (
	// Entity IDs and actions are checked before anything else; they also bound what the
	// audit log stores.
	entityIDPattern = regexp.MustCompile(`^[a-z0-9_]{1,64}\.[a-z0-9_]{1,190}$`)
	actionPattern   = regexp.MustCompile(`^[a-z_]{1,64}$`)
	// hiddenAttributes are never returned to agents (entity_picture carries an access token).
	hiddenAttributes = []string{"entity_picture", "access_token"}
)

// Interfaces to the rest of the gateway.
type (
	Authenticator interface {
		Authenticate(ctx context.Context, token string) (agent.Agent, error)
	}
	Decider interface {
		Decide(ctx context.Context, clientID, entityID, action string) (pdp.Decision, error)
	}
	Catalog interface {
		Lookup(entityID string) (catalog.Device, bool)
		All() []catalog.Device
		Ready() bool
	}
	Executor interface {
		CallService(ctx context.Context, call ha.ServiceCall) error
		Connected() bool
	}
	Limiter interface {
		Allow(clientID string, perHour int) bool
	}
	Auditor interface {
		Append(ctx context.Context, e audit.Entry) (int64, error)
	}
)

// Config wires the gateway.
type Config struct {
	Agents      Authenticator
	PDP         Decider
	Catalog     Catalog
	HA          Executor
	Limiter     Limiter
	Audit       Auditor
	Logger      *slog.Logger
	Version     string
	Now         func() time.Time
	CallTimeout time.Duration
}

// Gateway serves the MCP tools.
type Gateway struct {
	cfg    Config
	server *sdk.Server
}

// New registers the tools.
func New(cfg Config) *Gateway {
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.CallTimeout <= 0 {
		cfg.CallTimeout = defaultCallTimeout
	}
	g := &Gateway{cfg: cfg, server: sdk.NewServer(&sdk.Implementation{Name: "home-mandate", Version: cfg.Version}, nil)}
	sdk.AddTool(g.server, &sdk.Tool{Name: "list_devices",
		Description: "Lists the devices you may read, with category, area and state."}, g.listDevices)
	sdk.AddTool(g.server, &sdk.Tool{Name: "get_state",
		Description: "Returns the state of one device."}, g.getState)
	sdk.AddTool(g.server, &sdk.Tool{Name: "perform_action",
		Description: "Performs an action on one device, e.g. turn_on or unlock. Some actions need a human to confirm."}, g.performAction)
	sdk.AddTool(g.server, &sdk.Tool{Name: "list_my_permissions",
		Description: "Lists what you may do on which device: allow (immediately) or ask (a human confirms)."}, g.listPermissions)
	return g
}

// Handler serves the endpoint at Path, for bearer tokens of active agents only.
func (g *Gateway) Handler() http.Handler {
	h := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return g.server }, &sdk.StreamableHTTPOptions{
		Stateless: true, JSONResponse: true, MaxRequestBodyBytes: maxRequestBytes, Logger: g.cfg.Logger,
	})
	protected := sdkauth.RequireBearerToken(g.verify, &sdkauth.RequireBearerTokenOptions{AllowMissingExpiration: true})(h)
	mux := http.NewServeMux()
	mux.Handle(Path, protected)
	return mux
}

func (g *Gateway) verify(ctx context.Context, token string, _ *http.Request) (*sdkauth.TokenInfo, error) {
	a, err := g.cfg.Agents.Authenticate(ctx, token)
	if err != nil {
		if !errors.Is(err, agent.ErrUnauthorized) {
			g.cfg.Logger.Error("token check failed", "error", err)
		}
		return nil, sdkauth.ErrInvalidToken
	}
	return &sdkauth.TokenInfo{UserID: a.ClientID, Extra: map[string]any{"agent": a}}, nil
}

func agentOf(req *sdk.CallToolRequest) (agent.Agent, error) {
	if req == nil || req.Extra == nil || req.Extra.TokenInfo == nil {
		return agent.Agent{}, errors.New(codeDenied)
	}
	a, ok := req.Extra.TokenInfo.Extra["agent"].(agent.Agent)
	if !ok {
		return agent.Agent{}, errors.New(codeDenied)
	}
	return a, nil
}

// available reports whether requests can be decided and executed now.
func (g *Gateway) available() bool {
	return g.cfg.HA.Connected() && g.cfg.Catalog.Ready()
}

// Tool inputs and outputs.
type (
	noInput     struct{}
	entityInput struct {
		EntityID string `json:"entity_id" jsonschema:"entity ID, e.g. light.kitchen"`
	}
	actionInput struct {
		EntityID string         `json:"entity_id" jsonschema:"entity ID, e.g. lock.front_door"`
		Action   string         `json:"action" jsonschema:"action from the device category, e.g. turn_on"`
		Params   map[string]any `json:"params,omitempty" jsonschema:"parameters of the action, e.g. brightness_pct"`
	}
	deviceOut struct {
		EntityID string `json:"entity_id"`
		Name     string `json:"name,omitempty"`
		Category string `json:"category"`
		Area     string `json:"area,omitempty"`
		State    string `json:"state"`
	}
	devicesOut struct {
		Devices []deviceOut `json:"devices"`
	}
	stateOut struct {
		EntityID   string         `json:"entity_id"`
		State      string         `json:"state"`
		Attributes map[string]any `json:"attributes,omitempty"`
	}
	actionOut struct {
		Status string `json:"status"`
	}
	permissionOut struct {
		EntityID string            `json:"entity_id"`
		Category string            `json:"category"`
		Area     string            `json:"area,omitempty"`
		Actions  map[string]string `json:"actions"`
	}
	permissionsOut struct {
		Devices []permissionOut `json:"devices"`
	}
)

func (g *Gateway) listDevices(ctx context.Context, req *sdk.CallToolRequest, _ noInput) (*sdk.CallToolResult, devicesOut, error) {
	out := devicesOut{Devices: []deviceOut{}}
	a, err := agentOf(req)
	if err != nil {
		return nil, out, err
	}
	if !g.available() {
		return nil, out, errors.New(codeUnavailable)
	}
	for _, dev := range g.cfg.Catalog.All() {
		d, err := g.cfg.PDP.Decide(ctx, a.ClientID, dev.EntityID, "read")
		if err != nil || d.Result.Decision == evaluator.Deny {
			continue
		}
		name, _ := dev.Attributes["friendly_name"].(string)
		out.Devices = append(out.Devices, deviceOut{EntityID: dev.EntityID, Name: name, Category: dev.Category, Area: dev.Area, State: dev.State})
	}
	return nil, out, nil
}

func (g *Gateway) listPermissions(ctx context.Context, req *sdk.CallToolRequest, _ noInput) (*sdk.CallToolResult, permissionsOut, error) {
	out := permissionsOut{Devices: []permissionOut{}}
	a, err := agentOf(req)
	if err != nil {
		return nil, out, err
	}
	if !g.available() {
		return nil, out, errors.New(codeUnavailable)
	}
	for _, dev := range g.cfg.Catalog.All() {
		p := permissionOut{EntityID: dev.EntityID, Category: dev.Category, Area: dev.Area, Actions: map[string]string{}}
		for _, action := range actionsOf(dev.Category) {
			d, err := g.cfg.PDP.Decide(ctx, a.ClientID, dev.EntityID, action)
			if err == nil && d.Result.Decision != evaluator.Deny {
				p.Actions[action] = string(d.Result.Decision)
			}
		}
		if len(p.Actions) > 0 {
			out.Devices = append(out.Devices, p)
		}
	}
	return nil, out, nil
}

func (g *Gateway) getState(ctx context.Context, req *sdk.CallToolRequest, in entityInput) (*sdk.CallToolResult, stateOut, error) {
	a, err := agentOf(req)
	if err != nil {
		return nil, stateOut{}, err
	}
	if !entityIDPattern.MatchString(in.EntityID) {
		return nil, stateOut{}, errors.New(codeInvalidParams)
	}
	d, err := g.enforce(ctx, a, in.EntityID, "read")
	if err != nil {
		return nil, stateOut{}, err
	}
	dev, ok := g.cfg.Catalog.Lookup(in.EntityID)
	if !ok { // removed between decision and answer
		return nil, stateOut{}, errors.New(codeNotFound)
	}
	g.record(ctx, a, d, true, audit.Result{Status: audit.StatusExecuted})
	for _, k := range hiddenAttributes {
		delete(dev.Attributes, k)
	}
	return nil, stateOut{EntityID: dev.EntityID, State: dev.State, Attributes: dev.Attributes}, nil
}

func (g *Gateway) performAction(ctx context.Context, req *sdk.CallToolRequest, in actionInput) (*sdk.CallToolResult, actionOut, error) {
	a, err := agentOf(req)
	if err != nil {
		return nil, actionOut{}, err
	}
	if !entityIDPattern.MatchString(in.EntityID) || !actionPattern.MatchString(in.Action) || in.Action == "read" {
		return nil, actionOut{}, errors.New(codeInvalidParams)
	}
	d, err := g.enforce(ctx, a, in.EntityID, in.Action)
	if err != nil {
		return nil, actionOut{}, err
	}
	call, err := buildCall(in.EntityID, d.Resource.Category, in.Action, in.Params)
	if err != nil {
		code := codeInvalidParams
		if errors.Is(err, errNotSupported) {
			code = codeNotSupported
		}
		g.record(ctx, a, d, true, audit.Result{Status: audit.StatusFailed, Error: code})
		if code == codeInvalidParams {
			return nil, actionOut{}, err // names the parameter, nothing internal
		}
		return nil, actionOut{}, errors.New(code)
	}
	start := g.cfg.Now()
	cctx, cancel := context.WithTimeout(ctx, g.cfg.CallTimeout)
	err = g.cfg.HA.CallService(cctx, call)
	cancel()
	took := max(g.cfg.Now().Sub(start).Milliseconds(), 0)
	if err != nil {
		code := "ha_error"
		if errors.Is(err, ha.ErrDisconnected) || errors.Is(err, context.DeadlineExceeded) {
			code = "ha_unavailable"
		}
		g.cfg.Logger.Warn("service call failed", "entity_id", in.EntityID, "action", in.Action, "error", err)
		g.record(ctx, a, d, true, audit.Result{Status: audit.StatusFailed, Error: code, DurationMs: took})
		return nil, actionOut{}, errors.New(codeFailed)
	}
	g.record(ctx, a, d, true, audit.Result{Status: audit.StatusExecuted, DurationMs: took})
	return nil, actionOut{Status: "executed"}, nil
}

// enforce runs availability, PDP, rate limit and the decision for one request and logs
// every refusal. It returns the decision only if the action is allowed.
func (g *Gateway) enforce(ctx context.Context, a agent.Agent, entityID, action string) (pdp.Decision, error) {
	if !g.available() {
		d := pdp.Decision{Time: g.cfg.Now(), Resource: evaluator.Resource{EntityID: entityID}, Action: action}
		g.record(ctx, a, d, false, audit.Result{Status: audit.StatusFailed, Error: "ha_unavailable"})
		return pdp.Decision{}, errors.New(codeUnavailable)
	}
	d, err := g.cfg.PDP.Decide(ctx, a.ClientID, entityID, action)
	if err != nil {
		g.cfg.Logger.Error("decision failed", "error", err)
	}
	if d.TimeZone == "" && err == nil {
		g.record(ctx, a, d, false, audit.Result{Status: audit.StatusFailed, Error: "timezone_unknown"})
		return pdp.Decision{}, errors.New(codeUnavailable)
	}
	if d.Result.Reason != evaluator.ReasonInvalidMandate && !g.cfg.Limiter.Allow(a.ClientID, d.MaxActionsPerHour) {
		g.record(ctx, a, d, false, audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByRateLimit})
		return pdp.Decision{}, errors.New(codeRateLimited)
	}
	switch d.Result.Decision {
	case evaluator.Allow:
		return d, nil
	case evaluator.Ask:
		g.record(ctx, a, d, true, audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByApproval})
		return pdp.Decision{}, errors.New(codeApprovalRequired + ": confirmation by a human is not available yet")
	}
	g.record(ctx, a, d, true, audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByMandate})
	if action == "read" || !g.readable(ctx, a, entityID) {
		return pdp.Decision{}, errors.New(codeNotFound) // same answer as for an entity that does not exist
	}
	return pdp.Decision{}, errors.New(codeDenied + ": " + string(d.Result.Reason))
}

// readable reports whether the agent may read the entity; otherwise its existence is
// not revealed.
func (g *Gateway) readable(ctx context.Context, a agent.Agent, entityID string) bool {
	d, err := g.cfg.PDP.Decide(ctx, a.ClientID, entityID, "read")
	return err == nil && d.Result.Decision != evaluator.Deny
}

// record writes a decision entry. A failure is logged; the request result stands.
func (g *Gateway) record(ctx context.Context, a agent.Agent, d pdp.Decision, withEvaluation bool, result audit.Result) {
	e := audit.Entry{
		Event: audit.EventDecision,
		Agent: &audit.Agent{ClientID: a.ClientID, DisplayName: a.DisplayName},
		Request: &audit.Request{Time: d.Time, Timezone: d.TimeZone, Revoked: d.Status == evaluator.StatusRevoked,
			Resource: audit.Resource{EntityID: d.Resource.EntityID, Category: d.Resource.Category, Area: d.Resource.Area},
			Action:   d.Action},
		Result: &result,
	}
	if withEvaluation {
		r := d.Result
		e.Evaluation = &audit.Evaluation{Decision: string(r.Decision), Reason: string(r.Reason)}
		if r.RuleID != "" {
			e.Evaluation.RuleID = &r.RuleID
		}
		if r.Decision == evaluator.Ask && r.Approval != nil {
			e.Evaluation.ApprovalTimeout = r.Approval.Timeout
		}
		if r.MandateDigest != "" {
			e.Mandate = &audit.Mandate{ID: d.MandateID, Digest: r.MandateDigest}
		}
	}
	if _, err := g.cfg.Audit.Append(context.WithoutCancel(ctx), e); err != nil {
		g.cfg.Logger.Error("audit log write failed", "error", err)
	}
}
