// SPDX-License-Identifier: AGPL-3.0-or-later

// Package admission admits agents: a human who signed in picks a mandate template, and
// the agent, its mandate (an instance of the template) and its first tokens are created
// in one transaction (ARCHITECTURE section 6, decision W2). Templates are stored here;
// they are not mandates and are never evaluated themselves. Admissions are in the audit
// log (agent.registered, mandate.created); template changes are local settings for which
// SPEC-v0 has no event type.
package admission

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/mandate-spec/mandate-spec/displaytext"
	"github.com/mandate-spec/mandate-spec/evaluator"

	"github.com/home-mandate/home-mandate/internal/agent"
	"github.com/home-mandate/home-mandate/internal/audit"
	"github.com/home-mandate/home-mandate/internal/mandate"
)

var (
	// ErrInvalidTemplate means a template name or document is invalid.
	ErrInvalidTemplate = errors.New("admission: invalid template")
	// ErrTemplateNotFound means there is no template with that name.
	ErrTemplateNotFound = errors.New("admission: template not found")
	// ErrAgentNotActive means the agent is unknown or revoked.
	ErrAgentNotActive = errors.New("admission: agent unknown or revoked")
)

var templateName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// checkAgent is the agent a template is instantiated for when it is stored, to validate
// it as a mandate.
var checkAgent = agent.Agent{ClientID: "hm-client:template-check-00000000", DisplayName: "Template check"}

// Template describes a stored mandate template.
type Template struct {
	Name      string
	CreatedAt time.Time
	CreatedBy string
}

// Request is a human's decision to admit an agent.
type Request struct {
	DisplayName    string // chosen by the human
	Template       string
	OAuthClient    string // client ID of the agent's OAuth client
	ClientVerified bool   // OAuthClient is a fetched Client ID Metadata Document
	RedirectURIs   []string
	Resource       string // the tokens' resource
	// MandateName is the display name of the new mandate; empty means the template name.
	MandateName string
	// ConfirmCritical is the human's separate confirmation that the template's rules may
	// allow critical actions without approval (decision U9): without it, a template with
	// such a rule admits nobody.
	ConfirmCritical bool
	By              audit.Actor
}

// Store keeps the templates and admits agents.
type Store struct {
	db        *sql.DB
	agents    *agent.Store
	mandates  *mandate.Store
	principal string

	mu  sync.Mutex
	now func() time.Time
}

// New returns the admission of the household principal.
func New(db *sql.DB, agents *agent.Store, mandates *mandate.Store, principal string) *Store {
	return &Store{db: db, agents: agents, mandates: mandates, principal: principal, now: time.Now}
}

// SetClock replaces the clock, for tests.
func (s *Store) SetClock(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = now
}

func (s *Store) clock() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.now().UTC()
}

// PutTemplate stores or replaces a template. The document is a mandate whose id,
// principal, agent, created_by, created_at, valid_from and expires are replaced at
// admission; an instance of it must be a valid mandate.
func (s *Store) PutTemplate(ctx context.Context, name string, document []byte, by audit.Actor) error {
	if !templateName.MatchString(name) {
		return fmt.Errorf("%w: name must match %s", ErrInvalidTemplate, templateName)
	}
	if len(document) > evaluator.MaxMandateBytes {
		return fmt.Errorf("%w: too large", ErrInvalidTemplate)
	}
	instance, err := s.instantiate(document, checkAgent, by.ID, "")
	if err != nil {
		return err
	}
	if _, err := evaluator.Parse(instance); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidTemplate, err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO mandate_templates (name, document, created_at, created_by) VALUES (?, ?, ?, ?)
		ON CONFLICT (name) DO UPDATE SET document = excluded.document, created_at = excluded.created_at, created_by = excluded.created_by`,
		name, string(document), s.clock().Format(time.RFC3339Nano), by.ID); err != nil {
		return fmt.Errorf("admission: store template: %w", err)
	}
	return nil
}

// Templates lists the templates by name.
func (s *Store) Templates(ctx context.Context) ([]Template, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name, created_at, created_by FROM mandate_templates ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("admission: list templates: %w", err)
	}
	defer rows.Close()
	var list []Template
	for rows.Next() {
		var t Template
		var created string
		if err := rows.Scan(&t.Name, &created, &t.CreatedBy); err != nil {
			return nil, fmt.Errorf("admission: list templates: %w", err)
		}
		t.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		list = append(list, t)
	}
	return list, rows.Err()
}

// RemoveTemplate deletes a template; mandates made from it stay unchanged.
func (s *Store) RemoveTemplate(ctx context.Context, name string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM mandate_templates WHERE name = ?`, name)
	if err != nil {
		return fmt.Errorf("admission: remove template: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return ErrTemplateNotFound
	}
	return nil
}

// Admit registers the agent, stores its mandate from the template and issues its first
// tokens, in one transaction: if anything fails, nothing remains. Registration and
// mandate are in the audit log with the human as actor.
func (s *Store) Admit(ctx context.Context, req Request) (agent.Agent, agent.TokenPair, error) {
	if req.By.Kind == "" || req.By.ID == "" {
		return agent.Agent{}, agent.TokenPair{}, errors.New("admission: no actor")
	}
	// The actor becomes created_by of the mandate and actor.id in the audit log, both
	// text displayed to humans (SPEC-v0 section 3.1 item 8).
	if err := displaytext.Check(req.By.ID); err != nil {
		return agent.Agent{}, agent.TokenPair{}, fmt.Errorf("%w: actor: %w", mandate.ErrInvalid, err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return agent.Agent{}, agent.TokenPair{}, fmt.Errorf("admission: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	document, err := s.templateTx(ctx, tx, req.Template, req.ConfirmCritical)
	if err != nil {
		return agent.Agent{}, agent.TokenPair{}, err
	}
	a, err := s.agents.RegisterTx(ctx, tx, req.DisplayName, agent.Client{ID: req.OAuthClient, Verified: req.ClientVerified,
		RedirectURIs: req.RedirectURIs}, req.By)
	if err != nil {
		return agent.Agent{}, agent.TokenPair{}, err
	}
	if _, err := s.mandateFor(ctx, tx, document, a, mandateName(req.MandateName, req.Template), "", req.By); err != nil {
		return agent.Agent{}, agent.TokenPair{}, err
	}
	tokens, err := s.agents.IssueTokensTx(ctx, tx, a.ClientID, req.Resource)
	if err != nil {
		return agent.Agent{}, agent.TokenPair{}, err
	}
	if err := tx.Commit(); err != nil {
		return agent.Agent{}, agent.TokenPair{}, fmt.Errorf("admission: commit: %w", err)
	}
	return a, tokens, nil
}

// templateTx reads a template inside tx and refuses it when it grants critical actions
// without approval and the human did not confirm that separately.
func (s *Store) templateTx(ctx context.Context, tx *sql.Tx, name string, confirmCritical bool) ([]byte, error) {
	var document string
	err := tx.QueryRowContext(ctx, `SELECT document FROM mandate_templates WHERE name = ?`, name).Scan(&document)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTemplateNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("admission: read template: %w", err)
	}
	if !confirmCritical {
		granted, err := mandate.NewCriticalGrant(nil, []byte(document))
		if err != nil {
			return nil, err
		}
		if granted {
			return nil, mandate.ErrCriticalConfirmation
		}
	}
	return []byte(document), nil
}

// mandateFor stores the mandate of a from a template inside tx, under a new ID when
// suffix is set (a later mandate of an agent whose first one was revoked).
func (s *Store) mandateFor(ctx context.Context, tx *sql.Tx, template []byte, a agent.Agent, name, suffix string, by audit.Actor) (mandate.Info, error) {
	instance, err := s.instantiate(template, a, by.ID, suffix)
	if err != nil {
		return mandate.Info{}, err
	}
	info, err := s.mandates.PutTx(ctx, tx, instance, by)
	if err != nil {
		return mandate.Info{}, err
	}
	if err := s.mandates.SetNameTx(ctx, tx, info.ID, name); err != nil {
		return mandate.Info{}, err
	}
	info.Name = name
	return info, nil
}

func mandateName(name, template string) string {
	if name = strings.TrimSpace(name); name != "" {
		return name
	}
	return template
}

// NewMandate gives an active agent without an active mandate a new one from a template
// (POST api/mandates): like an admission, with the same separate confirmation for
// critical actions; mandate.ErrConflict if the agent has an active mandate.
func (s *Store) NewMandate(ctx context.Context, clientID, template, name string, confirmCritical bool, by audit.Actor) (mandate.Info, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return mandate.Info{}, fmt.Errorf("admission: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var a agent.Agent
	err = tx.QueryRowContext(ctx, `SELECT client_id, display_name, status FROM agents WHERE client_id = ?`, clientID).
		Scan(&a.ClientID, &a.DisplayName, &a.Status)
	if errors.Is(err, sql.ErrNoRows) || err == nil && a.Status != agent.StatusActive {
		return mandate.Info{}, ErrAgentNotActive
	}
	if err != nil {
		return mandate.Info{}, fmt.Errorf("admission: read agent: %w", err)
	}
	var active int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM mandates WHERE client_id = ? AND status = 'active'`, clientID).Scan(&active); err != nil {
		return mandate.Info{}, fmt.Errorf("admission: read mandates: %w", err)
	}
	if active > 0 {
		return mandate.Info{}, fmt.Errorf("%w: the agent has an active mandate", mandate.ErrConflict)
	}
	document, err := s.templateTx(ctx, tx, template, confirmCritical)
	if err != nil {
		return mandate.Info{}, err
	}
	var suffix [4]byte
	_, _ = rand.Read(suffix[:]) // crypto/rand.Read never fails (Go ≥ 1.24)
	info, err := s.mandateFor(ctx, tx, document, a, mandateName(name, template), hex.EncodeToString(suffix[:]), by)
	if err != nil {
		return mandate.Info{}, err
	}
	if err := tx.Commit(); err != nil {
		return mandate.Info{}, fmt.Errorf("admission: commit: %w", err)
	}
	return info, nil
}

// TemplateDocument returns the stored document of a template.
func (s *Store) TemplateDocument(ctx context.Context, name string) ([]byte, Template, error) {
	var document, created string
	t := Template{Name: name}
	err := s.db.QueryRowContext(ctx, `SELECT document, created_at, created_by FROM mandate_templates WHERE name = ?`, name).
		Scan(&document, &created, &t.CreatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, Template{}, ErrTemplateNotFound
	}
	if err != nil {
		return nil, Template{}, fmt.Errorf("admission: read template: %w", err)
	}
	t.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return []byte(document), t, nil
}

// UpdateTemplate stores a template edited by a human (PUT api/templates/{name}): like
// PutTemplate, but a rule with allow_critical that the stored template does not have in
// exactly this form needs the separate confirmation (decision U9).
func (s *Store) UpdateTemplate(ctx context.Context, name string, document []byte, confirmCritical bool, by audit.Actor) error {
	if !confirmCritical {
		current, _, err := s.TemplateDocument(ctx, name)
		if err != nil && !errors.Is(err, ErrTemplateNotFound) {
			return err
		}
		granted, err := mandate.NewCriticalGrant(current, document)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidTemplate, err)
		}
		if granted {
			return mandate.ErrCriticalConfirmation
		}
	}
	return s.PutTemplate(ctx, name, document, by)
}

// instantiate makes the mandate of a for a template. The mandate is valid from now on
// and carries no expiry of the template. Its ID is "m-" and the agent's slug, plus suffix
// for a later mandate of the same agent.
func (s *Store) instantiate(template []byte, a agent.Agent, createdBy, suffix string) ([]byte, error) {
	var doc map[string]any
	if err := json.Unmarshal(template, &doc); err != nil {
		return nil, fmt.Errorf("%w: not a JSON object", ErrInvalidTemplate)
	}
	now := s.clock().Format(time.RFC3339)
	doc["id"] = "m-" + strings.TrimPrefix(a.ClientID, "hm-client:")
	if suffix != "" {
		doc["id"] = doc["id"].(string) + "-" + suffix
	}
	doc["principal"] = s.principal
	doc["agent"] = map[string]any{"client_id": a.ClientID, "display_name": a.DisplayName}
	doc["created_by"] = createdBy
	doc["created_at"] = now
	doc["valid_from"] = now
	delete(doc, "expires")
	return json.Marshal(doc)
}

// InvalidTemplate is a stored template whose instance is not a valid mandate.
type InvalidTemplate struct {
	Name    string
	Problem string
}

// InvalidTemplates lists the templates that no longer yield a valid mandate, for example
// because the specification became stricter since they were stored. Admission with such
// a template fails.
func (s *Store) InvalidTemplates(ctx context.Context) ([]InvalidTemplate, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name, document FROM mandate_templates ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("admission: read templates: %w", err)
	}
	defer rows.Close()
	var out []InvalidTemplate
	for rows.Next() {
		var name, document string
		if err := rows.Scan(&name, &document); err != nil {
			return nil, fmt.Errorf("admission: read templates: %w", err)
		}
		instance, err := s.instantiate([]byte(document), checkAgent, checkAgent.ClientID, "")
		if err == nil {
			_, err = evaluator.Parse(instance)
		}
		if err != nil {
			problem, _, _ := strings.Cut(err.Error(), "\n")
			out = append(out, InvalidTemplate{Name: name, Problem: problem})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("admission: read templates: %w", err)
	}
	return out, nil
}
