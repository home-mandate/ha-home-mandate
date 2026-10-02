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
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

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
	Resource       string // the tokens' resource
	By             audit.Actor
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
	instance, err := s.instantiate(document, checkAgent, by.ID)
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
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return agent.Agent{}, agent.TokenPair{}, fmt.Errorf("admission: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var document string
	err = tx.QueryRowContext(ctx, `SELECT document FROM mandate_templates WHERE name = ?`, req.Template).Scan(&document)
	if errors.Is(err, sql.ErrNoRows) {
		return agent.Agent{}, agent.TokenPair{}, ErrTemplateNotFound
	}
	if err != nil {
		return agent.Agent{}, agent.TokenPair{}, fmt.Errorf("admission: read template: %w", err)
	}
	a, err := s.agents.RegisterTx(ctx, tx, req.DisplayName, req.OAuthClient, req.ClientVerified, req.By)
	if err != nil {
		return agent.Agent{}, agent.TokenPair{}, err
	}
	instance, err := s.instantiate([]byte(document), a, req.By.ID)
	if err != nil {
		return agent.Agent{}, agent.TokenPair{}, err
	}
	if _, err := s.mandates.PutTx(ctx, tx, instance, req.By); err != nil {
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

// instantiate makes the mandate of a for a template. The mandate is valid from now on
// and carries no expiry of the template.
func (s *Store) instantiate(template []byte, a agent.Agent, createdBy string) ([]byte, error) {
	var doc map[string]any
	if err := json.Unmarshal(template, &doc); err != nil {
		return nil, fmt.Errorf("%w: not a JSON object", ErrInvalidTemplate)
	}
	now := s.clock().Format(time.RFC3339)
	doc["id"] = "m-" + strings.TrimPrefix(a.ClientID, "hm-client:")
	doc["principal"] = s.principal
	doc["agent"] = map[string]any{"client_id": a.ClientID, "display_name": a.DisplayName}
	doc["created_by"] = createdBy
	doc["created_at"] = now
	doc["valid_from"] = now
	delete(doc, "expires")
	return json.Marshal(doc)
}
