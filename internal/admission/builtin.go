// SPDX-License-Identifier: AGPL-3.0-or-later

package admission

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/home-mandate/home-mandate/internal/i18n"
)

// Base templates (ARCHITECTURE section 6) ship with Home-Mandate, so that a new
// installation can admit an agent at once. They are used, loaded into the editor and
// saved under a new name, or hidden, but never changed or removed; names starting with
// reservedPrefix are theirs.

//go:embed builtin/*.json
var builtinFiles embed.FS

const (
	reservedPrefix = "hm-"
	hiddenKey      = "template_hidden:" // settings key of a hidden base template
)

var (
	// ErrBuiltinTemplate means a base template was to be changed or removed.
	ErrBuiltinTemplate = errors.New("admission: base templates cannot be changed")
	// ErrReservedName means a household template's name is not allowed (also wraps
	// ErrInvalidTemplate).
	ErrReservedName = fmt.Errorf("%w: name not allowed", ErrInvalidTemplate)
)

type builtin struct {
	name               string
	title, description i18n.Key
}

// builtins in the order they are offered: the most cautious first.
var builtins = []builtin{
	{"hm-read-only", i18n.TemplateReadOnlyTitle, i18n.TemplateReadOnlyDescription},
	{"hm-light-climate", i18n.TemplateLightClimateTitle, i18n.TemplateLightClimateDescription},
	{"hm-voice-cautious", i18n.TemplateVoiceCautiousTitle, i18n.TemplateVoiceCautiousDescription},
}

func builtinNamed(name string) (builtin, bool) {
	for _, b := range builtins {
		if b.name == name {
			return b, true
		}
	}
	return builtin{}, false
}

func builtinDocument(name string) []byte {
	data, err := builtinFiles.ReadFile("builtin/" + name + ".json")
	if err != nil {
		panic(fmt.Sprintf("admission: base template %s: %v", name, err)) // embedded, always there
	}
	return data
}

// texts are a message in every supported language.
func texts(key i18n.Key) map[string]string {
	out := make(map[string]string, len(i18n.Supported))
	for _, lang := range i18n.Supported {
		out[string(lang)] = i18n.T(lang, key, nil)
	}
	return out
}

// checkOwnName refuses the names of base templates and the reserved prefix for the
// household's own templates.
func checkOwnName(name string) error {
	if _, ok := builtinNamed(name); ok {
		return ErrBuiltinTemplate
	}
	if !templateName.MatchString(name) || strings.HasPrefix(name, reservedPrefix) {
		return fmt.Errorf("%w: must match %s and not start with %q", ErrReservedName, templateName, reservedPrefix)
	}
	return nil
}

// hidden returns the hidden base templates.
func hidden(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}) (map[string]bool, error) {
	rows, err := q.QueryContext(ctx, `SELECT key FROM settings WHERE key LIKE ? || '%'`, hiddenKey)
	if err != nil {
		return nil, fmt.Errorf("admission: read hidden templates: %w", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("admission: read hidden templates: %w", err)
		}
		out[strings.TrimPrefix(key, hiddenKey)] = true
	}
	return out, rows.Err()
}

// SetHidden hides a base template from admission, or shows it again. Hidden, it is neither
// offered nor accepted; the editor can still load it. Only base templates can be hidden.
func (s *Store) SetHidden(ctx context.Context, name string, hide bool) error {
	if _, ok := builtinNamed(name); !ok {
		return fmt.Errorf("%w: only base templates can be hidden", ErrInvalidTemplate)
	}
	var err error
	if hide {
		_, err = s.db.ExecContext(ctx, `INSERT INTO settings (key, value, updated_at) VALUES (?, '1', ?)
			ON CONFLICT (key) DO UPDATE SET updated_at = excluded.updated_at`, hiddenKey+name, s.clock().Format(time.RFC3339Nano))
	} else {
		_, err = s.db.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, hiddenKey+name)
	}
	if err != nil {
		return fmt.Errorf("admission: hide template: %w", err)
	}
	return nil
}

// humanUser is the form of a Home Assistant user ID: the admitting human becomes an
// approver only if they are one (the command line is no person).
var humanUser = regexp.MustCompile(`^[0-9a-f]{32}$`)
