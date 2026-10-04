// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"

	"github.com/home-mandate/home-mandate/internal/catalog"
	"github.com/home-mandate/home-mandate/internal/mandate"
)

// wireRename is an entity Home Assistant renamed while active mandates still name a
// former ID. Until a human resolves it, those rules keep applying to the entity and the
// stricter evaluation wins.
type wireRename struct {
	EntityID string               `json:"entity_id"`
	Name     string               `json:"name"`
	Formers  []string             `json:"formers"`
	Mandates []wireRenamedMandate `json:"mandates"`
}

type wireRenamedMandate struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Rules are the IDs of the rules that name a former ID.
	Rules []string `json:"rules"`
	// Critical: one of them allows critical actions without approval; taking the rename
	// over is a new grant and needs the separate confirmation.
	Critical bool `json:"critical"`
}

// affected is an active mandate whose rules name a former ID of a renamed entity.
type affected struct {
	info     mandate.Info
	document []byte
	rules    []string
	critical bool
}

// affectedMandates returns the active mandates whose rules name one of formers.
func (s *Server) affectedMandates(ctx context.Context, formers []string) ([]affected, error) {
	list, err := s.cfg.Mandates.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []affected
	for _, m := range list {
		if m.Status != mandate.StatusActive {
			continue
		}
		info, doc, err := s.cfg.Mandates.Current(ctx, m.ID)
		if err != nil {
			return nil, err
		}
		var d struct {
			Rules []struct {
				ID       string `json:"id"`
				Resource struct {
					EntityID string `json:"entity_id"`
				} `json:"resource"`
				AllowCritical bool `json:"allow_critical"`
			} `json:"rules"`
		}
		if err := json.Unmarshal(doc, &d); err != nil {
			return nil, err
		}
		a := affected{info: info, document: doc}
		for _, r := range d.Rules {
			if slices.Contains(formers, r.Resource.EntityID) {
				a.rules = append(a.rules, r.ID)
				a.critical = a.critical || r.AllowCritical
			}
		}
		if len(a.rules) > 0 {
			out = append(out, a)
		}
	}
	return out, nil
}

// getRenames lists the renames that active mandates are affected by.
func (s *Server) getRenames(r *request) (any, error) {
	out := []wireRename{}
	open := s.cfg.Renames.Open()
	for _, id := range slices.Sorted(maps.Keys(open)) {
		list, err := s.affectedMandates(r.Context(), open[id])
		if err != nil {
			return nil, err
		}
		if len(list) == 0 {
			continue
		}
		name := id
		if d, ok := s.cfg.Catalog.Lookup(id); ok && d.Name() != "" {
			name = d.Name()
		}
		w := wireRename{EntityID: id, Name: name, Formers: open[id], Mandates: []wireRenamedMandate{}}
		for _, a := range list {
			w.Mandates = append(w.Mandates, wireRenamedMandate{ID: a.info.ID, Name: nameOf(a.info), Rules: a.rules, Critical: a.critical})
		}
		out = append(out, w)
	}
	return out, nil
}

// renameInput is the entity a human resolves the rename of.
func (s *Server) renameInput(r *request) (string, []string, bool, error) {
	var in struct {
		EntityID        *string `json:"entity_id"`
		ConfirmCritical bool    `json:"confirm_critical"`
	}
	if err := r.decode(&in); err != nil {
		return "", nil, false, err
	}
	if in.EntityID == nil {
		return "", nil, false, failField(codeInvalidInput, "/entity_id")
	}
	formers := s.cfg.Renames.Open()[*in.EntityID]
	if len(formers) == 0 {
		return "", nil, false, fail(codeNotFound)
	}
	return *in.EntityID, formers, in.ConfirmCritical, nil
}

// applyRename takes a rename over into the mandates: every rule that names a former ID
// names the current one in a new version of its mandate. A rule that allows critical
// actions without approval is a new grant then; without the separate confirmation
// nothing is changed. Then the rename is resolved.
func (s *Server) applyRename(r *request) (any, error) {
	entityID, formers, confirm, err := s.renameInput(r)
	if err != nil {
		return nil, err
	}
	if _, ok := s.cfg.Catalog.Lookup(entityID); !ok {
		return nil, fail(codeNotFound)
	}
	list, err := s.affectedMandates(r.Context(), formers)
	if err != nil {
		return nil, err
	}
	type change struct {
		a   affected
		doc []byte
	}
	changes := make([]change, 0, len(list))
	for _, a := range list {
		doc, err := s.renamedDocument(r, a, formers, entityID)
		if err != nil {
			return nil, err
		}
		if err := s.checkResources(doc, "/entity_id"); err != nil {
			return nil, err
		}
		if !confirm {
			grant, err := mandate.NewCriticalGrant(a.document, doc)
			if err != nil {
				return nil, err
			}
			if grant {
				return nil, fail(codeCriticalConfirm)
			}
		}
		changes = append(changes, change{a, doc})
	}
	for _, c := range changes {
		if _, err := s.cfg.Mandates.Update(r.Context(), c.a.info.ID, c.doc, mandate.Change{BaseDigest: c.a.info.Digest,
			ConfirmCritical: confirm}, s.actor(r)); err != nil {
			return nil, mandateError(err, "/entity_id", nil)
		}
		s.publish(event{Type: "mandates.changed", ID: c.a.info.ID})
	}
	return s.resolveRename(r, entityID, catalog.ResolutionApplied, len(changes))
}

// dismissRename resolves a rename without changing a mandate: rules on the former IDs no
// longer apply to the renamed entity.
func (s *Server) dismissRename(r *request) (any, error) {
	entityID, _, _, err := s.renameInput(r)
	if err != nil {
		return nil, err
	}
	return s.resolveRename(r, entityID, catalog.ResolutionDismissed, 0)
}

func (s *Server) resolveRename(r *request, entityID, resolution string, mandates int) (any, error) {
	formers, err := s.cfg.Renames.Resolve(r.Context(), entityID, resolution, r.user)
	if err != nil {
		s.cfg.Logger.Error("rename not resolved", "entity_id", entityID, "resolution", resolution, "by", r.user, "error", err)
		return nil, err
	}
	// The audit log of the specification has no event for directory changes yet.
	s.cfg.Logger.Warn("rename resolved", "entity_id", entityID, "formers", formers, "resolution", resolution,
		"mandates", mandates, "by", r.user)
	s.publish(event{Type: "devices.changed"})
	return nil, nil
}

// renamedDocument is the next version of an affected mandate: the same editable fields
// with every former ID replaced by the current one.
func (s *Server) renamedDocument(r *request, a affected, formers []string, entityID string) ([]byte, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(a.document, &fields); err != nil {
		return nil, err
	}
	var rules []map[string]any
	if err := json.Unmarshal(fields["rules"], &rules); err != nil {
		return nil, err
	}
	for _, rule := range rules {
		res, _ := rule["resource"].(map[string]any)
		if id, _ := res["entity_id"].(string); slices.Contains(formers, id) {
			res["entity_id"] = entityID
		}
	}
	encoded, err := json.Marshal(rules)
	if err != nil {
		return nil, err
	}
	fields["rules"] = encoded
	draft := map[string]json.RawMessage{}
	for _, k := range draftKeys {
		if v, ok := fields[k]; ok {
			draft[k] = v
		}
	}
	raw, err := json.Marshal(draft)
	if err != nil {
		return nil, err
	}
	doc, err := s.documentOf(r, a.info.ID, a.document, raw)
	if err != nil {
		return nil, errors.Join(err, errors.New("api: renamed document"))
	}
	return doc, nil
}
