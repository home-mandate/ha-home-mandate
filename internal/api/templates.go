// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/home-mandate/home-mandate/internal/admission"
	"github.com/home-mandate/home-mandate/internal/mandate"
)

var templateNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// wireTemplateInfo is what the list and a single template say about it. Base templates
// have no creation and carry a title and description per language.
type wireTemplateInfo struct {
	Name          string            `json:"name"`
	RuleCount     int               `json:"rule_count"`
	CreatedAt     *string           `json:"created_at"`
	CreatedBy     string            `json:"created_by"`
	CreatedByName *string           `json:"created_by_name"`
	Builtin       bool              `json:"builtin"`
	Hidden        bool              `json:"hidden"`
	Title         map[string]string `json:"title"`
	Description   map[string]string `json:"description"`
	Digest        string            `json:"digest"`
}

type wireTemplate struct {
	wireTemplateInfo
	Draft json.RawMessage `json:"draft"`
}

func (s *Server) templateInfo(r *request, t admission.Template, doc []byte) (wireTemplateInfo, error) {
	f, err := parseDoc(doc)
	if err != nil {
		return wireTemplateInfo{}, err
	}
	title, description := t.Title, t.Description
	if title == nil {
		title, description = map[string]string{}, map[string]string{}
	}
	out := wireTemplateInfo{Name: t.Name, RuleCount: len(f.Rules), CreatedAt: formatTime(t.CreatedAt), CreatedBy: t.CreatedBy,
		Builtin: t.Builtin, Hidden: t.Hidden, Title: title, Description: description, Digest: t.Digest}
	if t.CreatedBy != "" {
		out.CreatedByName = s.users.name(r.Context(), t.CreatedBy)
	}
	return out, nil
}

func (s *Server) getTemplates(r *request) (any, error) {
	list, err := s.cfg.Admission.Templates(r.Context())
	if err != nil {
		return nil, err
	}
	out := make([]wireTemplateInfo, 0, len(list))
	for _, t := range list {
		doc, full, err := s.cfg.Admission.TemplateDocument(r.Context(), t.Name)
		if err != nil {
			return nil, err
		}
		info, err := s.templateInfo(r, full, doc)
		if err != nil {
			return nil, err
		}
		out = append(out, info)
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
	return s.wireTemplateOf(r, t, doc)
}

func (s *Server) wireTemplateOf(r *request, t admission.Template, doc []byte) (wireTemplate, error) {
	draft, err := draftOf(doc, t.CreatedAt)
	if err != nil {
		return wireTemplate{}, err
	}
	info, err := s.templateInfo(r, t, doc)
	if err != nil {
		return wireTemplate{}, err
	}
	return wireTemplate{wireTemplateInfo: info, Draft: draft}, nil
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
		BaseDigest      *string         `json:"base_digest"` // null for a new template
		ConfirmCritical bool            `json:"confirm_critical"`
	}
	if err := r.decode(&in); err != nil {
		return nil, err
	}
	base := ""
	if in.BaseDigest != nil {
		if !digestPattern.MatchString(*in.BaseDigest) {
			return nil, failField(codeInvalidInput, "/base_digest")
		}
		base = *in.BaseDigest
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
	if err := s.checkResources(doc, "/draft"); err != nil {
		return nil, err
	}
	err = s.cfg.Admission.UpdateTemplate(r.Context(), name, doc, base, in.ConfirmCritical, s.actor(r))
	switch {
	case errors.Is(err, admission.ErrBuiltinTemplate):
		return nil, fail(codeBuiltinTemplate)
	case errors.Is(err, mandate.ErrConflict):
		return nil, fail(codeConflict)
	case errors.Is(err, mandate.ErrCriticalConfirmation):
		return nil, fail(codeCriticalConfirm)
	case errors.Is(err, admission.ErrInvalidTemplate) && strings.HasPrefix(name, "hm-"):
		return nil, failField(codeInvalidInput, "/name")
	case errors.Is(err, admission.ErrInvalidTemplate):
		return nil, failField(codeInvalidMandate, invalidField(err, "/draft", in.Draft))
	case err != nil:
		return nil, err
	}
	s.publish(event{Type: "templates.changed"})
	stored, t, err := s.cfg.Admission.TemplateDocument(r.Context(), name)
	if err != nil {
		return nil, err
	}
	return s.wireTemplateOf(r, t, stored)
}

func (s *Server) deleteTemplate(r *request) (any, error) {
	name, err := templateName(r)
	if err != nil {
		return nil, err
	}
	switch err := s.cfg.Admission.RemoveTemplate(r.Context(), name); {
	case errors.Is(err, admission.ErrTemplateNotFound):
		return nil, fail(codeNotFound)
	case errors.Is(err, admission.ErrBuiltinTemplate):
		return nil, fail(codeBuiltinTemplate)
	case err != nil:
		return nil, err
	}
	s.publish(event{Type: "templates.changed"})
	return nil, nil
}

// putTemplateHidden hides a base template from admission, or shows it again.
func (s *Server) putTemplateHidden(r *request) (any, error) {
	name, err := templateName(r)
	if err != nil {
		return nil, err
	}
	var in struct {
		Hidden *bool `json:"hidden"`
	}
	if err := r.decode(&in); err != nil {
		return nil, err
	}
	if in.Hidden == nil {
		return nil, failField(codeInvalidInput, "/hidden")
	}
	if err := s.cfg.Admission.SetHidden(r.Context(), name, *in.Hidden); errors.Is(err, admission.ErrInvalidTemplate) {
		return nil, fail(codeNotFound) // only base templates can be hidden
	} else if err != nil {
		return nil, err
	}
	s.publish(event{Type: "templates.changed"})
	return nil, nil
}
