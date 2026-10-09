// SPDX-License-Identifier: AGPL-3.0-or-later

// Package removal removes revoked agents and mandates from the lists (SPEC-v0 section
// 11.3, issue #21), for a human in the UI or on the command line and for the retention.
//
// A removed agent or mandate is hidden at once and stays revoked for good. Its data is
// kept as long as audit entries refer to it (SPEC-v0 section 9.3): only then does the
// retention delete it, keeping a tombstone with its ID, display name and, for a
// mandate, the highest version issued (rollback protection, SPEC-v0 section 3.5). The
// retention also removes revoked agents and mandates that no audit entry refers to any
// more, as the system; one a human removed before is not recorded a second time.
package removal

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/home-mandate/ha-home-mandate/internal/agent"
	"github.com/home-mandate/ha-home-mandate/internal/audit"
	"github.com/home-mandate/ha-home-mandate/internal/mandate"
)

// System is the actor of removals by the retention.
var System = audit.Actor{Kind: audit.ActorSystem, ID: "retention"}

// Service removes agents and mandates of one database.
type Service struct {
	db       *sql.DB
	agents   *agent.Store
	mandates *mandate.Store
}

// New returns the service for the stores of db.
func New(db *sql.DB, agents *agent.Store, mandates *mandate.Store) *Service {
	return &Service{db: db, agents: agents, mandates: mandates}
}

// AgentOptions say what else happens when an agent is removed.
type AgentOptions struct {
	// Revoke revokes an active agent (its tokens and its active mandate) first, in the
	// same transaction; without it only a revoked agent is removed.
	Revoke bool
	// Mandates removes the agent's mandates too, each recorded as mandate.removed; one
	// that is not revoked yet is revoked first.
	Mandates bool
}

// Result names what was removed (or, for Expired.Purged, whose data was deleted).
type Result struct {
	// Revoked says that RemoveAgent revoked the agent; its open approval requests end.
	Revoked  bool
	Agents   []string
	Mandates []string
}

// RemoveAgent removes an agent, and with opt its mandates, in one transaction, with by
// as actor. Revocations are recorded before the removals (SPEC-v0 section 11.3).
// Removing a removed agent removes nothing of the agent itself.
func (s *Service) RemoveAgent(ctx context.Context, clientID string, opt AgentOptions, by audit.Actor) (Result, error) {
	var res Result
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var err error
		res, err = s.removeAgentTx(ctx, tx, clientID, opt, by)
		return err
	})
	if err != nil {
		return Result{}, err
	}
	return res, nil
}

func (s *Service) removeAgentTx(ctx context.Context, tx *sql.Tx, clientID string, opt AgentOptions, by audit.Actor) (Result, error) {
	var res Result
	if opt.Revoke {
		revoked, err := s.agents.RevokeTx(ctx, tx, clientID, by)
		if err != nil {
			return Result{}, err
		}
		if err := s.mandates.RevokeAgentTx(ctx, tx, clientID, by); err != nil {
			return Result{}, err
		}
		res.Revoked = revoked
	}
	if opt.Mandates {
		ids, err := ids(ctx, tx, `SELECT id FROM mandates WHERE client_id = ? AND removed_at IS NULL AND purged_at IS NULL ORDER BY rowid`, clientID)
		if err != nil {
			return Result{}, err
		}
		// Only the mandates of a revoked agent: a refused removal below rolls this back.
		for _, id := range ids {
			if err := s.mandates.RevokeTx(ctx, tx, id, by); err != nil {
				return Result{}, err
			}
			if _, err := s.mandates.RemoveTx(ctx, tx, id, by); err != nil {
				return Result{}, err
			}
			res.Mandates = append(res.Mandates, id)
		}
	}
	removed, err := s.agents.RemoveTx(ctx, tx, clientID, by)
	if err != nil {
		return Result{}, err
	}
	if removed {
		res.Agents = []string{clientID}
	}
	return res, nil
}

// RemoveMandate removes a revoked mandate with by as actor; a removed one stays as it is.
func (s *Service) RemoveMandate(ctx context.Context, id string, by audit.Actor) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		_, err := s.mandates.RemoveTx(ctx, tx, id, by)
		return err
	})
}

// RemoveRevoked removes every revoked agent with its mandates and every other revoked
// mandate that is still listed, with by as actor ("Remove all revoked"). Each agent is
// removed in its own transaction, so a failure keeps what was removed before.
func (s *Service) RemoveRevoked(ctx context.Context, by audit.Actor) (Result, error) {
	var res Result
	agents, err := ids(ctx, s.db, `SELECT client_id FROM agents WHERE status = 'revoked' AND removed_at IS NULL AND purged_at IS NULL ORDER BY rowid`)
	if err != nil {
		return res, err
	}
	for _, clientID := range agents {
		r, err := s.RemoveAgent(ctx, clientID, AgentOptions{Mandates: true}, by)
		if err != nil {
			return res, err
		}
		res.Agents = append(res.Agents, r.Agents...)
		res.Mandates = append(res.Mandates, r.Mandates...)
	}
	mandates, err := ids(ctx, s.db, `SELECT id FROM mandates WHERE status = 'revoked' AND removed_at IS NULL AND purged_at IS NULL ORDER BY rowid`)
	if err != nil {
		return res, err
	}
	for _, id := range mandates {
		if err := s.RemoveMandate(ctx, id, by); err != nil {
			return res, err
		}
		res.Mandates = append(res.Mandates, id)
	}
	return res, nil
}

// Expired is what one run of the retention did.
type Expired struct {
	// Removed were revoked and removed by the system now.
	Removed Result
	// Purged were removed before; their data is deleted now.
	Purged Result
}

// Count is the number of agents and mandates touched.
func (e Expired) Count() int {
	return len(e.Removed.Agents) + len(e.Removed.Mandates) + len(e.Purged.Agents) + len(e.Purged.Mandates)
}

// Conditions of the retention: no audit entry refers to the agent or mandate any more.
const (
	noAgentEntry   = `NOT EXISTS (SELECT 1 FROM audit_log l WHERE l.client_id = agents.client_id)`
	noMandateEntry = `NOT EXISTS (SELECT 1 FROM audit_log l WHERE l.mandate_id = mandates.id)`
)

// Expire is the retention's part (daily, after the audit log was truncated): it deletes
// the data of removed agents and mandates that no audit entry refers to any more, then
// removes, as the system, revoked ones that no entry refers to. A removal recorded now
// refers to them again, so their data goes with a later run. Each one is handled in its
// own transaction.
func (s *Service) Expire(ctx context.Context) (Expired, error) {
	var out Expired
	steps := []struct {
		query string
		into  *[]string
		do    func(*sql.Tx, string) error
	}{
		{`SELECT id FROM mandates WHERE removed_at IS NOT NULL AND purged_at IS NULL AND ` + noMandateEntry + ` ORDER BY rowid`,
			&out.Purged.Mandates, func(tx *sql.Tx, id string) error { return s.mandates.PurgeTx(ctx, tx, id) }},
		{`SELECT client_id FROM agents WHERE removed_at IS NOT NULL AND purged_at IS NULL AND ` + noAgentEntry + ` ORDER BY rowid`,
			&out.Purged.Agents, func(tx *sql.Tx, id string) error { return s.agents.PurgeTx(ctx, tx, id) }},
		{`SELECT id FROM mandates WHERE status = 'revoked' AND removed_at IS NULL AND purged_at IS NULL AND ` + noMandateEntry + ` ORDER BY rowid`,
			&out.Removed.Mandates, func(tx *sql.Tx, id string) error { _, err := s.mandates.RemoveTx(ctx, tx, id, System); return err }},
		{`SELECT client_id FROM agents WHERE status = 'revoked' AND removed_at IS NULL AND purged_at IS NULL AND ` + noAgentEntry + ` ORDER BY rowid`,
			&out.Removed.Agents, func(tx *sql.Tx, id string) error { _, err := s.agents.RemoveTx(ctx, tx, id, System); return err }},
	}
	for _, step := range steps {
		list, err := ids(ctx, s.db, step.query)
		if err != nil {
			return out, err
		}
		for _, id := range list {
			if err := s.inTx(ctx, func(tx *sql.Tx) error { return step.do(tx, id) }); err != nil {
				return out, err
			}
			*step.into = append(*step.into, id)
		}
	}
	return out, nil
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// ids returns the first column of a query.
func ids(ctx context.Context, q querier, query string, args ...any) ([]string, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("removal: query: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("removal: query: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("removal: query: %w", err)
	}
	return out, nil
}

func (s *Service) inTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("removal: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("removal: commit: %w", err)
	}
	return nil
}
