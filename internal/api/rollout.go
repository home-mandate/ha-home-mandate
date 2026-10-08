// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/admission"
	"github.com/home-mandate/ha-home-mandate/internal/agent"
	"github.com/home-mandate/ha-home-mandate/internal/mandate"
)

// maxRolloutTargets bounds the mandates one request applies a template to: far more than
// a household has agents, few enough for one request's time.
const maxRolloutTargets = 100

// Results of applying a template to one of several mandates.
const (
	rolloutUpdated   = "updated"
	rolloutUnchanged = "unchanged"
	// rolloutConflict: the mandate changed since the human saw it.
	rolloutConflict = "conflict"
	// rolloutRevoked: the mandate or its agent is revoked.
	rolloutRevoked  = "revoked"
	rolloutNotFound = "not_found"
	// rolloutFailed: anything else; the server log says why.
	rolloutFailed = "failed"
	// rolloutSkipped: not attempted, the request ran out of time; nothing was stored.
	rolloutSkipped = "skipped"
)

// rolloutReserve is the time left before the request's deadline below which no further
// mandate is started: the answer must still reach the client, so that it learns which
// mandates changed.
const rolloutReserve = 3 * time.Second

// rolloutNow is the clock the deadline is compared with; tests move it.
var rolloutNow = time.Now

// nearDeadline tells whether no further mandate may be started.
func nearDeadline(ctx context.Context) bool {
	if ctx.Err() != nil {
		return true
	}
	deadline, ok := ctx.Deadline()
	return ok && deadline.Sub(rolloutNow()) < rolloutReserve
}

// wireTemplateUsage lists the active mandates whose rules were last taken from a template.
type wireTemplateUsage struct {
	Name     string             `json:"name"`
	Digest   string             `json:"digest"` // of the template now
	Mandates []wireTemplateUser `json:"mandates"`
}

type wireTemplateUser struct {
	MandateID        string `json:"mandate_id"`
	MandateName      string `json:"mandate_name"`
	ClientID         string `json:"client_id"`
	AgentDisplayName string `json:"agent_display_name"`
	// Digest of the mandate's current version: the base of a change.
	Digest string `json:"digest"`
	// TakenAt is when the rules were last taken from the template, TemplateDigest the
	// template's digest then.
	TakenAt        string `json:"taken_at"`
	TemplateDigest string `json:"template_digest"`
	// EditedSince: a later version came from an edit; taking the template over replaces it.
	EditedSince bool `json:"edited_since"`
	// UpToDate: taking the template over as it is now would change nothing (the same
	// rules, approval settings and limits), whatever the digests say.
	UpToDate bool `json:"up_to_date"`
}

// getTemplateUsage answers GET api/templates/{name}/usage.
func (s *Server) getTemplateUsage(r *request) (any, error) {
	name, err := templateName(r)
	if err != nil {
		return nil, err
	}
	_, t, err := s.cfg.Admission.TemplateDocument(r.Context(), name)
	if errors.Is(err, admission.ErrTemplateNotFound) {
		return nil, fail(codeNotFound)
	}
	if err != nil {
		return nil, err
	}
	uses, err := s.cfg.Mandates.TemplateUses(r.Context())
	if err != nil {
		return nil, err
	}
	list, err := s.cfg.Mandates.List(r.Context())
	if err != nil {
		return nil, err
	}
	// The template as it would be taken over; without it (nobody to approve, a hidden base
	// template) a mandate counts as up to date only with the template's current digest.
	resolved, _, resolveErr := s.cfg.Admission.Resolved(r.Context(), name, s.actor(r))
	out := wireTemplateUsage{Name: name, Digest: t.Digest, Mandates: []wireTemplateUser{}}
	for _, m := range list {
		use, ok := uses[m.ID]
		if !ok || use.Template != name || m.Status != mandate.StatusActive {
			continue
		}
		a, err := s.cfg.Agents.Get(r.Context(), m.ClientID)
		if err != nil {
			return nil, err
		}
		if a.Status != agent.StatusActive {
			continue
		}
		upToDate := !use.EditedSince && use.TemplateDigest == t.Digest
		if resolveErr == nil {
			upToDate = s.wouldNotChange(r, m.ID, resolved)
		}
		out.Mandates = append(out.Mandates, wireTemplateUser{MandateID: m.ID, MandateName: nameOf(m), ClientID: m.ClientID,
			AgentDisplayName: a.DisplayName, Digest: m.Digest, TakenAt: *formatTime(use.At), TemplateDigest: use.TemplateDigest,
			EditedSince: use.EditedSince, UpToDate: upToDate})
	}
	return out, nil
}

// wouldNotChange tells whether taking the resolved template over into mandate id would
// store no version; false when that cannot be told.
func (s *Server) wouldNotChange(r *request, id string, template []byte) bool {
	_, current, err := s.cfg.Mandates.Current(r.Context(), id)
	if err != nil {
		return false
	}
	doc, err := s.templateMandate(r, id, current, template)
	return err == nil && mandate.SameEditable(current, doc)
}

type rolloutTarget struct {
	MandateID  string `json:"mandate_id"`
	BaseDigest string `json:"base_digest"`
}

// wireRollout is the result per mandate, in the order of the targets.
type wireRollout struct {
	Results []wireRolloutResult `json:"results"`
}

type wireRolloutResult struct {
	MandateID string `json:"mandate_id"`
	Result    string `json:"result"`
	// Digest is the mandate's current version after the request; nil if unknown.
	Digest *string `json:"digest"`
}

// checkTargets refuses an empty, too long or repeating list of targets and malformed ones.
func checkTargets(targets []rolloutTarget) error {
	if len(targets) == 0 || len(targets) > maxRolloutTargets {
		return failField(codeInvalidInput, "/targets")
	}
	seen := make(map[string]bool, len(targets))
	for i, t := range targets {
		at := "/targets/" + strconv.Itoa(i)
		if !mandateIDPattern.MatchString(t.MandateID) || seen[t.MandateID] {
			return failField(codeInvalidInput, at+"/mandate_id")
		}
		if !digestPattern.MatchString(t.BaseDigest) {
			return failField(codeInvalidInput, at+"/base_digest")
		}
		seen[t.MandateID] = true
	}
	return nil
}

// applyTemplateToMandates answers POST api/templates/{name}/apply: the template, as the
// human saw it (template_digest), becomes a new version of every target mandate through
// the same path as applying it to one. Each mandate changes in its own transaction, so a
// refused one does not hold back the others. One separate confirmation covers every
// target that would gain a rule allowing critical actions without approval; without it,
// no mandate changes. Close to the request's deadline no further mandate is started; those
// are answered as skipped, so the answer still says which mandates changed.
func (s *Server) applyTemplateToMandates(r *request) (any, error) {
	name, err := templateName(r)
	if err != nil {
		return nil, err
	}
	var in struct {
		TemplateDigest  string          `json:"template_digest"`
		Targets         []rolloutTarget `json:"targets"`
		ConfirmCritical bool            `json:"confirm_critical"`
	}
	if err := r.decode(&in); err != nil {
		return nil, err
	}
	if !digestPattern.MatchString(in.TemplateDigest) {
		return nil, failField(codeInvalidInput, "/template_digest")
	}
	if err := checkTargets(in.Targets); err != nil {
		return nil, err
	}
	tdoc, tdigest, err := s.resolvedTemplate(r, name)
	var e *apiError
	if errors.As(err, &e) && e.code == codeInvalidInput {
		return nil, fail(codeNotFound) // the template of the path is unknown or hidden
	}
	if err != nil {
		return nil, err
	}
	if tdigest != in.TemplateDigest {
		return nil, fail(codeConflict) // the template changed since the human saw it
	}
	plans := make([]rolloutPlan, len(in.Targets))
	for i, t := range in.Targets {
		plans[i] = s.planRollout(r, t, tdoc)
	}
	if !in.ConfirmCritical && slices.ContainsFunc(plans, func(p rolloutPlan) bool { return p.critical }) {
		return nil, fail(codeCriticalConfirm)
	}
	origin := mandate.Origin{Kind: mandate.OriginTemplate, Template: name, TemplateDigest: tdigest}
	out := wireRollout{Results: make([]wireRolloutResult, 0, len(plans))}
	for _, p := range plans {
		switch {
		case p.result != "":
		case nearDeadline(r.Context()):
			p.result = rolloutSkipped
		default:
			p.result, p.digest = s.rollOut(r, p, in.ConfirmCritical, origin)
		}
		res := wireRolloutResult{MandateID: p.target.MandateID, Result: p.result}
		if p.digest != "" {
			res.Digest = &p.digest
		}
		if p.result == rolloutUpdated {
			s.publish(event{Type: "mandates.changed", ID: p.target.MandateID})
		}
		out.Results = append(out.Results, res)
	}
	return out, nil
}

// rolloutPlan is what applying the template to one target would do: a result already
// (refused before anything is stored), or the document of the new version.
type rolloutPlan struct {
	target   rolloutTarget
	result   string
	digest   string
	document []byte
	// critical: the new version would grant critical actions without approval anew.
	critical bool
}

func (s *Server) planRollout(r *request, t rolloutTarget, template []byte) rolloutPlan {
	p := rolloutPlan{target: t}
	info, current, err := s.cfg.Mandates.Current(r.Context(), t.MandateID)
	switch {
	case errors.Is(err, mandate.ErrNotFound):
		p.result = rolloutNotFound
		return p
	case err != nil:
		return s.failedRollout(p, err)
	}
	p.digest = info.Digest
	if p.result = s.refusal(r.Context(), info, t.BaseDigest); p.result != "" {
		return p
	}
	doc, err := s.templateMandate(r, t.MandateID, current, template)
	if err != nil {
		return s.failedRollout(p, err)
	}
	granted, err := mandate.NewCriticalGrant(current, doc)
	if err != nil {
		return s.failedRollout(p, err)
	}
	p.document, p.critical = doc, granted
	return p
}

// refusal is the result for a mandate that cannot take the template: revoked (also its
// agent) or changed since the human saw it; "" if it can.
func (s *Server) refusal(ctx context.Context, info mandate.Info, base string) string {
	if info.Status != mandate.StatusActive {
		return rolloutRevoked
	}
	a, err := s.cfg.Agents.Get(ctx, info.ClientID)
	if err != nil || a.Status != agent.StatusActive {
		return rolloutRevoked // a mandate without its agent can take nothing either
	}
	if info.Digest != base {
		return rolloutConflict
	}
	return ""
}

// rollOut stores the planned version of one mandate in its own transaction.
func (s *Server) rollOut(r *request, p rolloutPlan, confirm bool, origin mandate.Origin) (string, string) {
	info, err := s.cfg.Mandates.Update(r.Context(), p.target.MandateID, p.document, mandate.Change{BaseDigest: p.target.BaseDigest,
		ConfirmCritical: confirm, Origin: origin}, s.actor(r))
	switch {
	case err == nil && info.Digest == p.target.BaseDigest:
		return rolloutUnchanged, info.Digest
	case err == nil:
		return rolloutUpdated, info.Digest
	case errors.Is(err, mandate.ErrConflict), errors.Is(err, mandate.ErrCriticalConfirmation), errors.Is(err, mandate.ErrInvalid):
		// Changed, revoked or its agent revoked since it was planned: say which, if it can be told.
		now, nowErr := s.cfg.Mandates.Get(r.Context(), p.target.MandateID)
		if nowErr == nil {
			if refused := s.refusal(r.Context(), now, now.Digest); refused != "" {
				return refused, now.Digest
			}
			if errors.Is(err, mandate.ErrInvalid) {
				return s.failedRollout(p, err).result, now.Digest
			}
			return rolloutConflict, now.Digest
		}
		return rolloutConflict, ""
	}
	f := s.failedRollout(p, err)
	return f.result, f.digest
}

func (s *Server) failedRollout(p rolloutPlan, err error) rolloutPlan {
	s.cfg.Logger.Error("applying a template to a mandate failed", "mandate_id", p.target.MandateID, "error", err)
	p.result = rolloutFailed
	return p
}
