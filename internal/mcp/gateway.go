// SPDX-License-Identifier: AGPL-3.0-or-later

// Package mcp is the MCP server for agents and the Policy Enforcement Point. Every tool
// call that touches a device goes token (bound to this resource) → emergency stop →
// availability → PDP → rate limit → (ask: parameters checked, a human confirms, then
// emergency stop and mandate checked again) → execution → audit log. Administrative
// functions do not exist here.
package mcp

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mandate-spec/mandate-spec/evaluator"

	"github.com/home-mandate/home-mandate/internal/agent"
	"github.com/home-mandate/home-mandate/internal/approval"
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
	// noMandateLimit bounds requests of agents without a usable mandate, which would
	// otherwise fill the audit log with denials.
	noMandateLimit = 60
	// rateLimitLogInterval is the minimum time between two audit entries for rate-limit
	// refusals of one agent.
	rateLimitLogInterval = time.Minute
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
)

// Interfaces to the rest of the gateway.
type (
	Authenticator interface {
		Authenticate(ctx context.Context, token, resource string) (agent.Agent, error)
		EmergencyStopActive(ctx context.Context) (bool, error)
	}
	Decider interface {
		Snapshot(ctx context.Context, clientID string) (*pdp.Snapshot, error)
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
		WithEntry(ctx context.Context, e audit.Entry, action func() error) error
	}
	// Approver asks a human to confirm an action with the decision ask.
	Approver interface {
		Ask(ctx context.Context, req approval.Request) (approval.Result, error)
	}
)

// Config wires the gateway.
type Config struct {
	// Resource is this endpoint's RFC 8707 resource identifier (public URL + Path);
	// access tokens must be bound to it. Empty refuses every token (OAuth off).
	Resource string
	// ResourceMetadataURL is announced in WWW-Authenticate on 401 (RFC 9728).
	ResourceMetadataURL string

	Agents  Authenticator
	PDP     Decider
	Catalog Catalog
	HA      Executor
	Limiter Limiter
	Audit   Auditor
	// Approvals asks humans for ask decisions; nil refuses every ask.
	Approvals   Approver
	Logger      *slog.Logger
	Version     string
	Now         func() time.Time
	CallTimeout time.Duration
}

// Gateway serves the MCP tools.
type Gateway struct {
	cfg    Config
	server *sdk.Server

	mu           sync.Mutex
	rateLimitLog map[string]time.Time // last rate-limit entry per agent
	rejectedLog  time.Time            // last auth.rejected entry for an invalid token
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
	g := &Gateway{cfg: cfg, rateLimitLog: map[string]time.Time{}, server: sdk.NewServer(&sdk.Implementation{Name: "home-mandate", Version: cfg.Version}, nil)}
	sdk.AddTool(g.server, &sdk.Tool{Name: "list_devices",
		Description: "Lists the devices you may read, with category, area and state."}, g.listDevices)
	sdk.AddTool(g.server, &sdk.Tool{Name: "get_state",
		Description: "Returns the state of one device."}, g.getState)
	sdk.AddTool(g.server, &sdk.Tool{Name: "perform_action",
		Description: "Performs an action on one device, e.g. turn_on or unlock. Some actions need a human to confirm; " +
			"then the call waits for the answer and you should give a short reason."}, g.performAction)
	sdk.AddTool(g.server, &sdk.Tool{Name: "list_my_permissions",
		Description: "Lists what you may do on which device: allow (immediately) or ask (a human confirms)."}, g.listPermissions)
	return g
}

// Handler serves the endpoint at Path, for bearer tokens of active agents only.
func (g *Gateway) Handler() http.Handler {
	h := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return g.server }, &sdk.StreamableHTTPOptions{
		Stateless: true, JSONResponse: true, MaxRequestBodyBytes: maxRequestBytes, Logger: g.cfg.Logger,
	})
	protected := sdkauth.RequireBearerToken(g.verify, &sdkauth.RequireBearerTokenOptions{
		ResourceMetadataURL: g.cfg.ResourceMetadataURL, AllowMissingExpiration: true})(h)
	mux := http.NewServeMux()
	mux.Handle(Path, protected)
	return mux
}

func (g *Gateway) verify(ctx context.Context, token string, _ *http.Request) (*sdkauth.TokenInfo, error) {
	a, err := g.cfg.Agents.Authenticate(ctx, token, g.cfg.Resource)
	if err != nil {
		if !errors.Is(err, agent.ErrUnauthorized) {
			g.cfg.Logger.Error("token check failed", "error", err)
		}
		g.recordRejectedToken(ctx)
		return nil, sdkauth.ErrInvalidToken
	}
	return &sdkauth.TokenInfo{UserID: a.ClientID, Extra: map[string]any{"agent": a}}, nil
}

// recordRejectedToken logs at most one auth.rejected entry per interval: an invalid
// token names no agent, and anyone on the network could otherwise fill the audit log.
func (g *Gateway) recordRejectedToken(ctx context.Context) {
	now := g.cfg.Now()
	g.mu.Lock()
	if !g.rejectedLog.IsZero() && now.Sub(g.rejectedLog) < rateLimitLogInterval {
		g.mu.Unlock()
		return
	}
	g.rejectedLog = now
	g.mu.Unlock()
	if _, err := g.cfg.Audit.Append(context.WithoutCancel(ctx), audit.Entry{Event: audit.EventAuthRejected,
		Result: &audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByAuthentication, Error: "invalid_token"}}); err != nil {
		g.cfg.Logger.Error("audit log write failed", "error", err)
	}
}

// stopped reports whether the emergency stop is active; an unreadable stop counts as
// active. Tokens are revoked when the stop is activated, so this catches requests that
// were authenticated a moment before.
func (g *Gateway) stopped(ctx context.Context) bool {
	on, err := g.cfg.Agents.EmergencyStopActive(ctx)
	if err != nil {
		g.cfg.Logger.Error("reading the emergency stop failed", "error", err)
		return true
	}
	return on
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
		Reason   string         `json:"reason,omitempty" jsonschema:"why you want this; shown to the human who confirms, marked as your claim"`
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
	snap, err := g.listSnapshot(ctx, req)
	if err != nil {
		return nil, out, err
	}
	for _, dev := range g.cfg.Catalog.All() {
		// Only devices the agent may read now; ask would need a human first.
		if snap.Decide(dev.EntityID, "read").Result.Decision != evaluator.Allow {
			continue
		}
		name, _ := dev.Attributes["friendly_name"].(string)
		out.Devices = append(out.Devices, deviceOut{EntityID: dev.EntityID, Name: name, Category: dev.Category, Area: dev.Area, State: dev.State})
	}
	return nil, out, nil
}

func (g *Gateway) listPermissions(ctx context.Context, req *sdk.CallToolRequest, _ noInput) (*sdk.CallToolResult, permissionsOut, error) {
	out := permissionsOut{Devices: []permissionOut{}}
	snap, err := g.listSnapshot(ctx, req)
	if err != nil {
		return nil, out, err
	}
	for _, dev := range g.cfg.Catalog.All() {
		p := permissionOut{EntityID: dev.EntityID, Category: dev.Category, Area: dev.Area, Actions: map[string]string{}}
		for _, action := range actionsOf(dev.Category) {
			if d := snap.Decide(dev.EntityID, action); d.Result.Decision != evaluator.Deny {
				p.Actions[action] = string(d.Result.Decision)
			}
		}
		if len(p.Actions) > 0 {
			out.Devices = append(out.Devices, p)
		}
	}
	return nil, out, nil
}

// listSnapshot checks availability and the rate limit for a list request (one token per
// list) and loads the agent's mandate once.
func (g *Gateway) listSnapshot(ctx context.Context, req *sdk.CallToolRequest) (*pdp.Snapshot, error) {
	a, err := agentOf(req)
	if err != nil {
		return nil, err
	}
	if g.stopped(ctx) {
		return nil, errors.New(codeDenied + ": emergency_stop")
	}
	if !g.available() {
		return nil, errors.New(codeUnavailable)
	}
	snap, err := g.cfg.PDP.Snapshot(ctx, a.ClientID)
	if err != nil {
		g.cfg.Logger.Error("loading the mandate failed", "error", err)
	}
	if !g.cfg.Limiter.Allow(a.ClientID, limitOf(snap)) {
		return nil, errors.New(codeRateLimited)
	}
	return snap, nil
}

func limitOf(snap *pdp.Snapshot) int {
	if n := snap.MaxActionsPerHour(); n > 0 {
		return n
	}
	return noMandateLimit
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
		_ = g.record(ctx, a, d, true, audit.Result{Status: audit.StatusFailed, Error: codeNotFound})
		return nil, stateOut{}, errors.New(codeNotFound)
	}
	// The state is returned only if the read is in the audit log.
	if err := g.record(ctx, a, d, true, audit.Result{Status: audit.StatusExecuted}); err != nil {
		return nil, stateOut{}, errors.New(codeUnavailable)
	}
	return nil, stateOut{EntityID: dev.EntityID, State: dev.State, Attributes: sanitize(dev.Attributes)}, nil
}

// sanitize removes attributes that can carry access tokens, such as entity_picture of
// cameras and media players with a ?token= URL.
func sanitize(attrs map[string]any) map[string]any {
	out := make(map[string]any, len(attrs))
	for k, v := range attrs {
		key := strings.ToLower(k)
		if strings.Contains(key, "token") || strings.Contains(key, "picture") {
			continue
		}
		if s, ok := v.(string); ok && strings.Contains(strings.ToLower(s), "token=") {
			continue
		}
		out[k] = v
	}
	return out
}

func (g *Gateway) performAction(ctx context.Context, req *sdk.CallToolRequest, in actionInput) (*sdk.CallToolResult, actionOut, error) {
	a, err := agentOf(req)
	if err != nil {
		return nil, actionOut{}, err
	}
	if !entityIDPattern.MatchString(in.EntityID) || !actionPattern.MatchString(in.Action) || in.Action == "read" ||
		utf8.RuneCountInString(in.Reason) > maxReasonRunes {
		return nil, actionOut{}, errors.New(codeInvalidParams)
	}
	d, err := g.enforce(ctx, a, in.EntityID, in.Action)
	ask := errors.Is(err, errAsk)
	if err != nil && !ask {
		return nil, actionOut{}, err
	}
	call, err := buildCall(in.EntityID, d.Resource.Category, in.Action, in.Params)
	if err != nil {
		code := codeInvalidParams
		if errors.Is(err, errNotSupported) {
			code = codeNotSupported
		}
		_ = g.record(ctx, a, d, true, audit.Result{Status: audit.StatusFailed, Error: code})
		if code == codeInvalidParams {
			return nil, actionOut{}, err // names the parameter, nothing internal
		}
		return nil, actionOut{}, errors.New(code)
	}
	if ask {
		return g.askHuman(ctx, a, d, call, in.Reason)
	}
	return g.execute(ctx, a, d, call, nil)
}

// execute calls Home Assistant only while its "executed" entry is being written in the
// same transaction: no entry, no execution. A failed call rolls the entry back and is
// logged as failed.
func (g *Gateway) execute(ctx context.Context, a agent.Agent, d pdp.Decision, call ha.ServiceCall, appr *audit.Approval) (*sdk.CallToolResult, actionOut, error) {
	start := g.cfg.Now()
	executed := false
	e := g.entry(a, d, true, audit.Result{Status: audit.StatusExecuted})
	e.Approval = appr
	err := g.cfg.Audit.WithEntry(context.WithoutCancel(ctx), e, func() error {
		cctx, cancel := context.WithTimeout(ctx, g.cfg.CallTimeout)
		defer cancel()
		if err := g.cfg.HA.CallService(cctx, call); err != nil {
			return err
		}
		executed = true
		return nil
	})
	var actionErr *audit.ActionError
	switch {
	case err == nil:
		return nil, actionOut{Status: "executed"}, nil
	case executed:
		// Executed, but the entry could not be committed: report the truth, log loudly.
		g.cfg.Logger.Error("action executed but its audit entry was lost", "entity_id", call.EntityID, "error", err)
		return nil, actionOut{Status: "executed"}, nil
	case !errors.As(err, &actionErr):
		g.cfg.Logger.Error("audit log unavailable, action not executed", "error", err)
		return nil, actionOut{}, errors.New(codeUnavailable)
	}
	code := "ha_error"
	if errors.Is(err, ha.ErrDisconnected) || errors.Is(err, context.DeadlineExceeded) {
		code = "ha_unavailable"
	}
	g.cfg.Logger.Warn("service call failed", "entity_id", call.EntityID, "service", call.Service, "error", actionErr.Err)
	took := max(g.cfg.Now().Sub(start).Milliseconds(), 0)
	_ = g.recordApproval(ctx, a, d, audit.Result{Status: audit.StatusFailed, Error: code, DurationMs: took}, appr)
	return nil, actionOut{}, errors.New(codeFailed)
}

// enforce runs availability, PDP, rate limit and the decision for one request and logs
// every refusal. It returns the decision only if the action is allowed. An agent that
// may not read the entity gets not_found for every refusal, as for a missing entity.
func (g *Gateway) enforce(ctx context.Context, a agent.Agent, entityID, action string) (pdp.Decision, error) {
	if g.stopped(ctx) {
		d := pdp.Decision{Time: g.cfg.Now(), Resource: evaluator.Resource{EntityID: entityID}, Action: action}
		_ = g.record(ctx, a, d, false, audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByEmergencyStop})
		return pdp.Decision{}, errors.New(codeDenied + ": emergency_stop")
	}
	if !g.available() {
		d := pdp.Decision{Time: g.cfg.Now(), Resource: evaluator.Resource{EntityID: entityID}, Action: action}
		_ = g.record(ctx, a, d, false, audit.Result{Status: audit.StatusFailed, Error: "ha_unavailable"})
		return pdp.Decision{}, errors.New(codeUnavailable)
	}
	snap, err := g.cfg.PDP.Snapshot(ctx, a.ClientID)
	if err != nil {
		g.cfg.Logger.Error("loading the mandate failed", "error", err)
	}
	d := snap.Decide(entityID, action)
	if d.TimeZone == "" && err == nil {
		_ = g.record(ctx, a, d, false, audit.Result{Status: audit.StatusFailed, Error: "timezone_unknown"})
		return pdp.Decision{}, errors.New(codeUnavailable)
	}
	if !g.cfg.Limiter.Allow(a.ClientID, limitOf(snap)) {
		g.recordRateLimited(ctx, a, d)
		return pdp.Decision{}, errors.New(codeRateLimited)
	}
	if d.Result.Decision == evaluator.Allow {
		return d, nil
	}
	readable := d.Result.Decision != evaluator.Deny
	if action != "read" {
		readable = snap.Decide(entityID, "read").Result.Decision != evaluator.Deny
	}
	if d.Result.Decision == evaluator.Ask {
		if readable && action != "read" && g.cfg.Approvals != nil {
			return d, errAsk // the caller asks a human after checking the parameters
		}
		_ = g.record(ctx, a, d, true, audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByApproval})
		if !readable {
			return pdp.Decision{}, errors.New(codeNotFound)
		}
		return pdp.Decision{}, errors.New(codeApprovalRequired + ": reading this device needs a confirmation, which v0.1 does not ask for")
	}
	_ = g.record(ctx, a, d, true, audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByMandate})
	if !readable {
		return pdp.Decision{}, errors.New(codeNotFound) // same answer as for an entity that does not exist
	}
	return pdp.Decision{}, errors.New(codeDenied + ": " + string(d.Result.Reason))
}

// recordRateLimited logs at most one rate-limit refusal per agent and interval, so an
// agent cannot fill the audit log by exceeding its limit.
func (g *Gateway) recordRateLimited(ctx context.Context, a agent.Agent, d pdp.Decision) {
	now := g.cfg.Now()
	g.mu.Lock()
	last, seen := g.rateLimitLog[a.ClientID]
	if seen && now.Sub(last) < rateLimitLogInterval {
		g.mu.Unlock()
		return
	}
	g.rateLimitLog[a.ClientID] = now
	g.mu.Unlock()
	_ = g.record(ctx, a, d, false, audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByRateLimit})
}

// record writes a decision entry; a failure is logged and returned.
func (g *Gateway) record(ctx context.Context, a agent.Agent, d pdp.Decision, withEvaluation bool, result audit.Result) error {
	return g.append(ctx, g.entry(a, d, withEvaluation, result))
}

// recordApproval writes a decision entry with the outcome of an approval request (nil
// if none was asked).
func (g *Gateway) recordApproval(ctx context.Context, a agent.Agent, d pdp.Decision, result audit.Result, appr *audit.Approval) error {
	e := g.entry(a, d, true, result)
	e.Approval = appr
	return g.append(ctx, e)
}

func (g *Gateway) append(ctx context.Context, e audit.Entry) error {
	if _, err := g.cfg.Audit.Append(context.WithoutCancel(ctx), e); err != nil {
		g.cfg.Logger.Error("audit log write failed", "error", err)
		return err
	}
	return nil
}

func (g *Gateway) entry(a agent.Agent, d pdp.Decision, withEvaluation bool, result audit.Result) audit.Entry {
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
	return e
}
