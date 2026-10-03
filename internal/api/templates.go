// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/home-mandate/home-mandate/internal/admission"
	"github.com/home-mandate/home-mandate/internal/mandate"
)

var templateNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

type wireTemplateSummary struct {
	Name          string  `json:"name"`
	RuleCount     int     `json:"rule_count"`
	CreatedAt     string  `json:"created_at"`
	CreatedBy     string  `json:"created_by"`
	CreatedByName *string `json:"created_by_name"`
}

type wireTemplate struct {
	Name  string          `json:"name"`
	Draft json.RawMessage `json:"draft"`
}

func (s *Server) getTemplates(r *request) (any, error) {
	list, err := s.cfg.Admission.Templates(r.Context())
	if err != nil {
		return nil, err
	}
	out := make([]wireTemplateSummary, 0, len(list))
	for _, t := range list {
		doc, _, err := s.cfg.Admission.TemplateDocument(r.Context(), t.Name)
		if err != nil {
			return nil, err
		}
		f, err := parseDoc(doc)
		if err != nil {
			return nil, err
		}
		out = append(out, wireTemplateSummary{Name: t.Name, RuleCount: len(f.Rules), CreatedAt: *formatTime(t.CreatedAt),
			CreatedBy: t.CreatedBy, CreatedByName: s.users.name(r.Context(), t.CreatedBy)})
	}
	return out, nil
}

func templateName(r *request) (string, error) {
	name := r.PathValue("name")
	if !templateNamePattern.MatchString(name) {
		return "", fail(codeNotFound)
	}
	return name, nil
}

// draftOf returns the editable part of a stored template. A template stored by the
// command line may lack valid_from (it is set at admission); the draft then starts from
// the template's own creation, which is replaced when a mandate is made from it.
func draftOf(document []byte, created time.Time) (json.RawMessage, error) {
	var all map[string]json.RawMessage
	if err := json.Unmarshal(document, &all); err != nil {
		return nil, err
	}
	draft := map[string]json.RawMessage{}
	for _, k := range draftKeys {
		if v, ok := all[k]; ok && k != "expires" {
			draft[k] = v
		}
	}
	if _, ok := draft["valid_from"]; !ok {
		from, _ := json.Marshal(created.UTC().Format(time.RFC3339))
		draft["valid_from"] = from
	}
	return json.Marshal(draft)
}

func (s *Server) getTemplate(r *request) (any, error) {
	name, err := templateName(r)
	if err != nil {
		return nil, err
	}
	doc, t, err := s.cfg.Admission.TemplateDocument(r.Context(), name)
	if errors.Is(err, admission.ErrTemplateNotFound) {
		return nil, fail(codeNotFound)
	}
	if err != nil {
		return nil, err
	}
	draft, err := draftOf(doc, t.CreatedAt)
	if err != nil {
		return nil, err
	}
	return wireTemplate{Name: name, Draft: draft}, nil
}

// putTemplate stores a template from a draft. The template is checked as a mandate
// (admission instantiates it for a check agent); a rule with allow_critical that the
// stored template does not have in exactly this form needs confirm_critical (U9).
func (s *Server) putTemplate(r *request) (any, error) {
	name := r.PathValue("name")
	if !templateNamePattern.MatchString(name) {
		return nil, failField(codeInvalidInput, "/name")
	}
	var in struct {
		Draft           json.RawMessage `json:"draft"`
		ConfirmCritical bool            `json:"confirm_critical"`
	}
	if err := r.decode(&in); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(in.Draft))
	dec.UseNumber()
	var fields map[string]any
	if err := dec.Decode(&fields); err != nil || fields == nil {
		return nil, failField(codeInvalidInput, "/draft")
	}
	for key := range fields {
		if key != "rules" && key != "approval" && key != "limits" && key != "valid_from" && key != "expires" {
			return nil, failField(codeInvalidInput, "/draft/"+pointerSegment(key))
		}
	}
	delete(fields, "expires") // a template carries no expiry; admission removes it anyway
	fields["type"], fields["default"] = mandateType, "deny"
	doc, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	err = s.cfg.Admission.UpdateTemplate(r.Context(), name, doc, in.ConfirmCritical, s.actor(r))
	switch {
	case errors.Is(err, mandate.ErrCriticalConfirmation):
		return nil, fail(codeCriticalConfirm)
	case errors.Is(err, admission.ErrInvalidTemplate):
		return nil, failField(codeInvalidMandate, invalidField(err, "/draft", in.Draft))
	case err != nil:
		return nil, err
	}
	s.publish(event{Type: "templates.changed"})
	draft, err := draftOf(doc, s.now())
	if err != nil {
		return nil, err
	}
	return wireTemplate{Name: name, Draft: draft}, nil
}

func (s *Server) deleteTemplate(r *request) (any, error) {
	name, err := templateName(r)
	if err != nil {
		return nil, err
	}
	if err := s.cfg.Admission.RemoveTemplate(r.Context(), name); errors.Is(err, admission.ErrTemplateNotFound) {
		return nil, fail(codeNotFound)
	} else if err != nil {
		return nil, err
	}
	s.publish(event{Type: "templates.changed"})
	return nil, nil
}
