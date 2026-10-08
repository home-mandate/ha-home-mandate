// SPDX-License-Identifier: AGPL-3.0-or-later

package mandate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"time"
)

// Kinds of Origin.
const (
	// OriginUnknown is the origin of versions stored before origins were kept.
	OriginUnknown = "unknown"
	// OriginTemplate is a version whose rules, approval settings and limits were taken
	// from a template: at admission, for a new mandate or by applying the template.
	OriginTemplate = "template"
	// OriginEdit is a version a human wrote: in the editor, on the command line or by
	// taking over a rename.
	OriginEdit = "edit"
)

// Origin says where the rules of a version came from. It is shown to humans and never
// read by the evaluation.
type Origin struct {
	Kind string
	// Template and TemplateDigest name the template and the digest of its content, for
	// OriginTemplate only.
	Template       string
	TemplateDigest string
}

// edited is the origin of a change that names none.
func (o Origin) orEdit() Origin {
	if o == (Origin{}) {
		return Origin{Kind: OriginEdit}
	}
	return o
}

// check accepts the origins a new version may have: a template with name and digest, or
// an edit without either. Unknown is only for versions stored before.
func (o Origin) check() error {
	switch {
	case o.Kind == OriginTemplate && o.Template != "" && o.TemplateDigest != "":
		return nil
	case o.Kind == OriginEdit && o.Template == "" && o.TemplateDigest == "":
		return nil
	}
	return fmt.Errorf("%w: origin %q of a new version", ErrInvalid, o.Kind)
}

// editableKeys are the parts of a mandate a human edits; a version that keeps them all
// is no change.
var editableKeys = []string{"rules", "approval", "limits", "valid_from", "expires"}

// SameEditable tells whether two documents have the same rules, approval settings,
// limits and validity. Lists of texts (actions, weekdays, approvers) compare regardless
// of their order, which has no meaning; everything else, the metadata excluded, must be
// equal. A document that is no JSON object is never the same.
func SameEditable(current, next []byte) bool {
	a, okA := editable(current)
	b, okB := editable(next)
	return okA && okB && reflect.DeepEqual(a, b)
}

func editable(document []byte) (map[string]any, bool) {
	dec := json.NewDecoder(bytes.NewReader(document))
	dec.UseNumber() // numbers compare as written: at worst a version too many, never one too few
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil || doc == nil {
		return nil, false
	}
	out := map[string]any{}
	for _, k := range editableKeys {
		if v, ok := doc[k]; ok {
			out[k] = canonical(v)
		}
	}
	return out, true
}

// TemplateUse says from which template the rules of a mandate were last taken.
type TemplateUse struct {
	Template       string
	TemplateDigest string
	// At is when that version was stored.
	At time.Time
	// EditedSince is set when a later version came from somewhere else (an edit).
	EditedSince bool
}

// TemplateUses returns, by mandate ID, the template the rules of each mandate were last
// taken from; mandates whose versions name no template are missing.
func (s *Store) TemplateUses(ctx context.Context) (map[string]TemplateUse, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT v.mandate_id, v.template_name, v.template_digest, v.created_at,
			EXISTS (SELECT 1 FROM mandate_versions w WHERE w.mandate_id = v.mandate_id AND w.version > v.version)
		FROM mandate_versions v
		WHERE v.origin = 'template' AND v.version = (SELECT max(version) FROM mandate_versions x
			WHERE x.mandate_id = v.mandate_id AND x.origin = 'template')`)
	if err != nil {
		return nil, fmt.Errorf("mandate: template uses: %w", err)
	}
	defer rows.Close()
	out := map[string]TemplateUse{}
	for rows.Next() {
		var id, at string
		var u TemplateUse
		if err := rows.Scan(&id, &u.Template, &u.TemplateDigest, &at, &u.EditedSince); err != nil {
			return nil, fmt.Errorf("mandate: template uses: %w", err)
		}
		u.At, _ = time.Parse(timeFormat, at)
		out[id] = u
	}
	return out, rows.Err()
}
