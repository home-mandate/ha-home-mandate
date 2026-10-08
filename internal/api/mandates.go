// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/home-mandate/ha-home-mandate/internal/admission"
	"github.com/home-mandate/ha-home-mandate/internal/agent"
	"github.com/home-mandate/ha-home-mandate/internal/mandate"
)

const mandateType = "https://home-mandate.org/mandate/v0"

var (
	mandateIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{4,64}$`)
	versionPattern   = regexp.MustCompile(`^[1-9][0-9]{0,8}$`)
	digestPattern    = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	// draftKeys are the fields of a mandate a human edits (types.ts MandateDraft).
	draftKeys = []string{"rules", "approval", "limits", "valid_from", "expires"}
)

type wireMandateSummary struct {
	ID                string  `json:"id"`
	Name              string  `json:"name"`
	ClientID          string  `json:"client_id"`
	AgentDisplayName  string  `json:"agent_display_name"`
	Status            string  `json:"status"`
	Digest            string  `json:"digest"`
	RuleCount         int     `json:"rule_count"`
	ValidFrom         string  `json:"valid_from"`
	Expires           *string `json:"expires"`
	MaxActionsPerHour int     `json:"max_actions_per_hour"`
	UpdatedAt         string  `json:"updated_at"`
	// StaleReferences are rules on devices or areas Home Assistant does not have.
	StaleReferences []wireStaleReference `json:"stale_references"`
	// RulesFrom is the template the rules were last taken from; nil if unknown.
	RulesFrom *wireRulesFrom `json:"rules_from"`
}

// wireRulesFrom says from which template the rules of a mandate were last taken.
type wireRulesFrom struct {
	Template       string `json:"template"`
	TemplateDigest string `json:"template_digest"`
	At             string `json:"at"`
	// EditedSince is set when a later version came from an edit.
	EditedSince bool `json:"edited_since"`
}

func rulesFrom(uses map[string]mandate.TemplateUse, id string) *wireRulesFrom {
	u, ok := uses[id]
	if !ok {
		return nil
	}
	return &wireRulesFrom{Template: u.Template, TemplateDigest: u.TemplateDigest, At: *formatTime(u.At), EditedSince: u.EditedSince}
}

type wireVersion struct {
	Number        int     `json:"number"`
	Digest        string  `json:"digest"`
	CreatedAt     string  `json:"created_at"`
	CreatedBy     string  `json:"created_by"`
	CreatedByName *string `json:"created_by_name"`
	// Origin is where the rules came from: template, edit or unknown (stored before
	// origins were kept); Template and TemplateDigest only for a template.
	Origin         string  `json:"origin"`
	Template       *string `json:"template"`
	TemplateDigest *string `json:"template_digest"`
}

type wireMandateDetail struct {
	Summary  wireMandateSummary `json:"summary"`
	Document json.RawMessage    `json:"document"`
	Versions []wireVersion      `json:"versions"`
}

// docFields are the fields of a stored document the summary shows.
type docFields struct {
	Principal string          `json:"principal"`
	Agent     json.RawMessage `json:"agent"`
	Rules     []struct {
		ID string `json:"id"`
	} `json:"rules"`
	ValidFrom string `json:"valid_from"`
	Expires   string `json:"expires"`
	AgentName struct {
		DisplayName string `json:"display_name"`
	} `json:"-"`
}

func parseDoc(doc []byte) (docFields, error) {
	var f docFields
	if err := json.Unmarshal(doc, &f); err != nil {
		return docFields{}, err
	}
	_ = json.Unmarshal(f.Agent, &f.AgentName)
	return f, nil
}

func (s *Server) summary(info mandate.Info, doc []byte, uses map[string]mandate.TemplateUse) (wireMandateSummary, error) {
	f, err := parseDoc(doc)
	if err != nil {
		return wireMandateSummary{}, err
	}
	return wireMandateSummary{ID: info.ID, Name: nameOf(info), ClientID: info.ClientID, AgentDisplayName: f.AgentName.DisplayName,
		Status: info.Status, Digest: info.Digest, RuleCount: len(f.Rules), ValidFrom: f.ValidFrom, Expires: optional(f.Expires),
		MaxActionsPerHour: info.MaxActionsPerHour, UpdatedAt: *formatTime(info.UpdatedAt),
		StaleReferences: s.staleReferences(info, doc), RulesFrom: rulesFrom(uses, info.ID)}, nil
}

func (s *Server) getMandates(r *request) (any, error) {
	list, err := s.cfg.Mandates.List(r.Context())
	if err != nil {
		return nil, err
	}
	uses, err := s.cfg.Mandates.TemplateUses(r.Context())
	if err != nil {
		return nil, err
	}
	out := make([]wireMandateSummary, 0, len(list))
	for _, m := range list {
		info, doc, err := s.cfg.Mandates.Current(r.Context(), m.ID)
		if err != nil {
			return nil, err
		}
		sum, err := s.summary(info, doc, uses)
		if err != nil {
			return nil, err
		}
		out = append(out, sum)
	}
	return out, nil
}

// mandateID returns the {id} of the path, or not_found for anything that cannot be one.
func mandateID(r *request) (string, error) {
	id := r.PathValue("id")
	if !mandateIDPattern.MatchString(id) {
		return "", fail(codeNotFound)
	}
	return id, nil
}

func (s *Server) detail(ctx context.Context, id string) (wireMandateDetail, error) {
	info, doc, err := s.cfg.Mandates.Current(ctx, id)
	if errors.Is(err, mandate.ErrNotFound) {
		return wireMandateDetail{}, fail(codeNotFound)
	}
	if err != nil {
		return wireMandateDetail{}, err
	}
	uses, err := s.cfg.Mandates.TemplateUses(ctx)
	if err != nil {
		return wireMandateDetail{}, err
	}
	sum, err := s.summary(info, doc, uses)
	if err != nil {
		return wireMandateDetail{}, err
	}
	versions, err := s.cfg.Mandates.Versions(ctx, id)
	if err != nil {
		return wireMandateDetail{}, err
	}
	out := wireMandateDetail{Summary: sum, Document: doc, Versions: make([]wireVersion, 0, len(versions))}
	for _, v := range slices.Backward(versions) {
		w := wireVersion{Number: v.Number, Digest: v.Digest, CreatedAt: *formatTime(v.CreatedAt),
			CreatedBy: v.CreatedBy, CreatedByName: s.users.name(ctx, v.CreatedBy), Origin: v.Origin.Kind}
		if v.Origin.Kind == mandate.OriginTemplate {
			w.Template, w.TemplateDigest = &v.Origin.Template, &v.Origin.TemplateDigest
		}
		out.Versions = append(out.Versions, w)
	}
	return out, nil
}

func (s *Server) getMandate(r *request) (any, error) {
	id, err := mandateID(r)
	if err != nil {
		return nil, err
	}
	return s.detail(r.Context(), id)
}

func (s *Server) getMandateVersion(r *request) (any, error) {
	id, err := mandateID(r)
	if err != nil {
		return nil, err
	}
	n := r.PathValue("number")
	if !versionPattern.MatchString(n) {
		return nil, fail(codeNotFound)
	}
	number, _ := strconv.Atoi(n)
	doc, _, err := s.cfg.Mandates.VersionDocument(r.Context(), id, number)
	if errors.Is(err, mandate.ErrNotFound) {
		return nil, fail(codeNotFound)
	}
	if err != nil {
		return nil, err
	}
	return json.RawMessage(doc), nil
}

// documentOf builds the document of a new version from the draft a human edited: the
// identity of the mandate comes from its current version, never from the request.
func (s *Server) documentOf(r *request, id string, current []byte, draft json.RawMessage) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(draft))
	dec.UseNumber()
	var fields map[string]any
	if err := dec.Decode(&fields); err != nil || fields == nil {
		return nil, failField(codeInvalidInput, "/draft")
	}
	for key := range fields {
		if !slices.Contains(draftKeys, key) {
			return nil, failField(codeInvalidInput, "/draft/"+pointerSegment(key))
		}
	}
	var cur struct {
		Principal string          `json:"principal"`
		Agent     json.RawMessage `json:"agent"`
	}
	if err := json.Unmarshal(current, &cur); err != nil {
		return nil, err
	}
	fields["type"], fields["id"], fields["principal"], fields["agent"] = mandateType, id, cur.Principal, cur.Agent
	fields["default"], fields["created_by"], fields["created_at"] = "deny", r.user, s.now().Format("2006-01-02T15:04:05Z")
	return json.Marshal(fields)
}

// mandateError maps the errors of storing a mandate; field is where an invalid mandate
// points to when the error does not tell.
func mandateError(err error, prefix string, draft json.RawMessage) error {
	switch {
	case errors.Is(err, mandate.ErrNotFound):
		return fail(codeNotFound)
	case errors.Is(err, mandate.ErrConflict):
		return fail(codeConflict)
	case errors.Is(err, mandate.ErrCriticalConfirmation):
		return fail(codeCriticalConfirm)
	case errors.Is(err, mandate.ErrNoApprovers):
		return fail(codeNoApprovers)
	case errors.Is(err, mandate.ErrInvalid):
		return failField(codeInvalidMandate, invalidField(err, prefix, draft))
	}
	return err
}

var (
	ruleInError    = regexp.MustCompile(`rule "([^"]*)"`)
	pointerEscaper = strings.NewReplacer("~", "~0", "/", "~1")
)

func pointerSegment(s string) string {
	return pointerEscaper.Replace(s)
}

// invalidField finds the part of the draft an invalid mandate is about: the location of
// a schema error, the rule a check of SPEC-v0 section 3.1 names, or the draft itself.
func invalidField(err error, prefix string, draft json.RawMessage) string {
	var ve *jsonschema.ValidationError
	if errors.As(err, &ve) {
		for len(ve.Causes) > 0 {
			ve = ve.Causes[0]
		}
		if len(ve.InstanceLocation) > 0 && slices.Contains(draftKeys, ve.InstanceLocation[0]) {
			parts := make([]string, len(ve.InstanceLocation))
			for i, p := range ve.InstanceLocation {
				parts[i] = pointerSegment(p)
			}
			return prefix + "/" + strings.Join(parts, "/")
		}
		return prefix
	}
	msg := err.Error()
	if m := ruleInError.FindStringSubmatch(msg); m != nil {
		var d struct {
			Rules []struct {
				ID string `json:"id"`
			} `json:"rules"`
		}
		_ = json.Unmarshal(draft, &d)
		for i, rule := range d.Rules {
			if rule.ID == m[1] {
				return prefix + "/rules/" + strconv.Itoa(i)
			}
		}
	}
	if strings.Contains(msg, "expires") {
		return prefix + "/expires"
	}
	return prefix
}

func (s *Server) putMandate(r *request) (any, error) {
	id, err := mandateID(r)
	if err != nil {
		return nil, err
	}
	var in struct {
		Name            string          `json:"name"`
		Draft           json.RawMessage `json:"draft"`
		BaseDigest      string          `json:"base_digest"`
		ConfirmCritical bool            `json:"confirm_critical"`
	}
	if err := r.decode(&in); err != nil {
		return nil, err
	}
	name, ok := validDisplayName(in.Name)
	if !ok {
		return nil, failField(codeInvalidInput, "/name")
	}
	if !digestPattern.MatchString(in.BaseDigest) {
		return nil, failField(codeInvalidInput, "/base_digest")
	}
	if len(in.Draft) == 0 {
		return nil, failField(codeInvalidInput, "/draft")
	}
	info, current, err := s.cfg.Mandates.Current(r.Context(), id)
	if err != nil {
		return nil, mandateError(err, "/draft", in.Draft)
	}
	if sameDraft(current, in.Draft) {
		// A rename alone stores no version (its metadata, created_by and created_at,
		// would change the digest); the conflict rules apply all the same.
		if info.Status != mandate.StatusActive || info.Digest != in.BaseDigest {
			return nil, fail(codeConflict)
		}
		if err := s.cfg.Mandates.SetName(r.Context(), id, name); err != nil {
			return nil, mandateError(err, "/draft", in.Draft)
		}
	} else {
		doc, err := s.documentOf(r, id, current, in.Draft)
		if err != nil {
			return nil, err
		}
		if err := s.checkResources(doc, "/draft"); err != nil {
			return nil, err
		}
		if _, err := s.cfg.Mandates.Update(r.Context(), id, doc, mandate.Change{BaseDigest: in.BaseDigest,
			ConfirmCritical: in.ConfirmCritical, Name: name}, s.actor(r)); err != nil {
			return nil, mandateError(err, "/draft", in.Draft)
		}
	}
	s.publish(event{Type: "mandates.changed", ID: id})
	return s.detail(r.Context(), id)
}

// sameDraft tells whether draft has exactly the editable content of the document: the
// same fields with the same values (JSON objects compare regardless of key order).
func sameDraft(document []byte, draft json.RawMessage) bool {
	var doc, next map[string]any
	if json.Unmarshal(document, &doc) != nil || json.Unmarshal(draft, &next) != nil {
		return false
	}
	current := map[string]any{}
	for _, k := range draftKeys {
		if v, ok := doc[k]; ok {
			current[k] = v
		}
	}
	a, errA := json.Marshal(current)
	b, errB := json.Marshal(next)
	return errA == nil && errB == nil && bytes.Equal(a, b)
}

// templateDraft returns the parts of a template that a mandate takes over.
func templateDraft(document []byte) (map[string]json.RawMessage, error) {
	var t map[string]json.RawMessage
	if err := json.Unmarshal(document, &t); err != nil {
		return nil, err
	}
	return t, nil
}

// Results of applying a template to a mandate.
const (
	applyUpdated   = "updated"
	applyUnchanged = "unchanged"
)

// wireApplyResult is the mandate after a template was applied, and whether that stored
// a version.
type wireApplyResult struct {
	wireMandateDetail
	Result string `json:"result"`
}

// templateMandate builds the document of a new version of a mandate from the current
// one with the rules, approval settings and limits of a resolved template; validity and
// identity stay (decision D3).
func (s *Server) templateMandate(r *request, id string, current, template []byte) ([]byte, error) {
	tmpl, err := templateDraft(template)
	if err != nil {
		return nil, err
	}
	var draft map[string]json.RawMessage
	if err := json.Unmarshal(current, &draft); err != nil {
		return nil, err
	}
	for _, k := range []string{"rules", "approval", "limits"} {
		v, ok := tmpl[k]
		if !ok {
			return nil, failField(codeInvalidMandate, "/template")
		}
		draft[k] = v
	}
	edited := map[string]json.RawMessage{}
	for _, k := range draftKeys {
		if v, ok := draft[k]; ok {
			edited[k] = v
		}
	}
	raw, _ := json.Marshal(edited)
	return s.documentOf(r, id, current, raw)
}

// resolvedTemplate reads a template for applying it, as the human r is from.
func (s *Server) resolvedTemplate(r *request, name string) ([]byte, string, error) {
	doc, digest, err := s.cfg.Admission.Resolved(r.Context(), name, s.actor(r))
	switch {
	case errors.Is(err, admission.ErrTemplateNotFound):
		return nil, "", failField(codeInvalidInput, "/template")
	case errors.Is(err, mandate.ErrNoApprovers):
		return nil, "", fail(codeNoApprovers)
	case err != nil:
		return nil, "", mandateError(err, "/template", nil)
	}
	return doc, digest, nil
}

// applyTemplate makes the template's rules, approval settings and limits a new version
// of the mandate (decision D3); dates stay, and so does the name unless the request names
// another. Same conflict and U9 rules as an edit. A template that brings nothing new
// stores no version and answers "unchanged".
func (s *Server) applyTemplate(r *request) (any, error) {
	id, err := mandateID(r)
	if err != nil {
		return nil, err
	}
	var in struct {
		Template        string  `json:"template"`
		BaseDigest      string  `json:"base_digest"`
		Name            *string `json:"name"`
		ConfirmCritical bool    `json:"confirm_critical"`
	}
	if err := r.decode(&in); err != nil {
		return nil, err
	}
	if !digestPattern.MatchString(in.BaseDigest) {
		return nil, failField(codeInvalidInput, "/base_digest")
	}
	name := ""
	if in.Name != nil {
		var ok bool
		if name, ok = validDisplayName(*in.Name); !ok {
			return nil, failField(codeInvalidInput, "/name")
		}
	}
	tdoc, tdigest, err := s.resolvedTemplate(r, in.Template)
	if err != nil {
		return nil, err
	}
	_, current, err := s.cfg.Mandates.Current(r.Context(), id)
	if err != nil {
		return nil, mandateError(err, "/template", nil)
	}
	doc, err := s.templateMandate(r, id, current, tdoc)
	if err != nil {
		return nil, err
	}
	info, err := s.cfg.Mandates.Update(r.Context(), id, doc, mandate.Change{BaseDigest: in.BaseDigest,
		ConfirmCritical: in.ConfirmCritical, Name: name,
		Origin: mandate.Origin{Kind: mandate.OriginTemplate, Template: in.Template, TemplateDigest: tdigest}}, s.actor(r))
	if err != nil {
		return nil, mandateError(err, "/template", nil)
	}
	result := applyUpdated
	if info.Digest == in.BaseDigest {
		result = applyUnchanged
	}
	s.publish(event{Type: "mandates.changed", ID: id})
	detail, err := s.detail(r.Context(), id)
	if err != nil {
		return nil, err
	}
	return wireApplyResult{wireMandateDetail: detail, Result: result}, nil
}

// createMandate gives an active agent without an active mandate a new one from a
// template (POST api/mandates).
func (s *Server) createMandate(r *request) (any, error) {
	var in struct {
		ClientID        string  `json:"client_id"`
		Template        string  `json:"template"`
		Name            *string `json:"name"`
		ConfirmCritical bool    `json:"confirm_critical"`
	}
	if err := r.decode(&in); err != nil {
		return nil, err
	}
	name := ""
	if in.Name != nil && strings.TrimSpace(*in.Name) != "" {
		var ok bool
		if name, ok = validDisplayName(*in.Name); !ok {
			return nil, failField(codeInvalidInput, "/name")
		}
	}
	a, err := s.cfg.Agents.Get(r.Context(), in.ClientID)
	if errors.Is(err, agent.ErrNotFound) {
		return nil, fail(codeNotFound)
	}
	if err != nil {
		return nil, err
	}
	if a.Status != agent.StatusActive {
		return nil, fail(codeConflict)
	}
	info, err := s.cfg.Admission.NewMandate(r.Context(), a.ClientID, in.Template, name, in.ConfirmCritical, s.actor(r))
	switch {
	case errors.Is(err, admission.ErrTemplateNotFound):
		return nil, failField(codeInvalidInput, "/template")
	case errors.Is(err, admission.ErrAgentNotActive):
		return nil, fail(codeConflict)
	case err != nil:
		return nil, mandateError(err, "/template", nil)
	}
	s.publish(event{Type: "mandates.changed", ID: info.ID})
	s.publish(event{Type: "agents.changed"})
	return s.detail(r.Context(), info.ID)
}

// revokeMandate revokes a mandate and ends the open approval requests of its agent: any
// approval would be refused by the check after the answer anyway.
func (s *Server) revokeMandate(r *request) (any, error) {
	id, err := mandateID(r)
	if err != nil {
		return nil, err
	}
	if err := s.cfg.Mandates.Revoke(r.Context(), id, s.actor(r)); err != nil {
		return nil, mandateError(err, "", nil)
	}
	info, doc, err := s.cfg.Mandates.Current(r.Context(), id)
	if err != nil {
		return nil, err
	}
	s.cfg.Approvals.CancelAgent(info.ClientID)
	s.publish(event{Type: "mandates.changed", ID: id})
	s.publish(event{Type: "agents.changed"})
	uses, err := s.cfg.Mandates.TemplateUses(r.Context())
	if err != nil {
		return nil, err
	}
	return s.summary(info, doc, uses)
}
