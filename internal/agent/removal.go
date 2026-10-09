// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/home-mandate/ha-home-mandate/internal/audit"
)

// Removal and reconnection (SPEC-v0 section 11.3, issues #21 and #22).
var (
	// ErrNotRevoked means the agent is active: only a revoked agent is removed.
	ErrNotRevoked = errors.New("agent: not revoked")
	// ErrNotRemoved means the agent was not removed: only the data of a removed agent
	// is deleted.
	ErrNotRemoved = errors.New("agent: not removed")
	// ErrNotReconnectable means the agent cannot be reconnected for this client: it was
	// admitted with another OAuth client, or it still holds a valid token.
	ErrNotReconnectable = errors.New("agent: cannot be reconnected")
)

// RemoveTx removes a revoked agent from the lists inside tx and records agent.removed
// with by as actor (a human, or the system for the retention). It reports whether the
// agent was removed now; removing a removed agent is a no-op. The agent stays revoked,
// its rows stay until PurgeTx, and its mandates are not touched.
func (s *Store) RemoveTx(ctx context.Context, tx *sql.Tx, clientID string, by audit.Actor) (bool, error) {
	var status, name string
	var removed bool
	err := tx.QueryRowContext(ctx, `SELECT status, display_name, removed_at IS NOT NULL FROM agents WHERE client_id = ? AND purged_at IS NULL`,
		clientID).Scan(&status, &name, &removed)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, ErrNotFound
	case err != nil:
		return false, fmt.Errorf("agent: read: %w", err)
	case status != StatusRevoked:
		return false, ErrNotRevoked
	case removed:
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE agents SET removed_at = ?, removed_by = ? WHERE client_id = ?`,
		s.clock().Format(timeFormat), by.ID, clientID); err != nil {
		return false, fmt.Errorf("agent: remove: %w", err)
	}
	if _, err := s.log.AppendTx(ctx, tx, audit.Entry{Event: audit.EventAgentRemoved, Actor: &by,
		Agent: &audit.Agent{ClientID: clientID, DisplayName: name}}); err != nil {
		return false, err
	}
	return true, nil
}

// PurgeTx deletes the data of a removed agent inside tx: its tokens, the OAuth client it
// was admitted with and who admitted, revoked and removed it. What stays is a tombstone
// with its client ID and display name, so that its name still resolves; Get and List
// leave it out. Purging a purged agent is a no-op.
func (s *Store) PurgeTx(ctx context.Context, tx *sql.Tx, clientID string) error {
	var removed, purged bool
	err := tx.QueryRowContext(ctx, `SELECT removed_at IS NOT NULL, purged_at IS NOT NULL FROM agents WHERE client_id = ?`, clientID).
		Scan(&removed, &purged)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return ErrNotFound
	case err != nil:
		return fmt.Errorf("agent: read: %w", err)
	case !removed:
		return ErrNotRemoved
	case purged:
		return nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM tokens WHERE client_id = ?`, clientID); err != nil {
		return fmt.Errorf("agent: purge tokens: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE agents SET oauth_client = '', client_verified = 0, redirect_uris = '[]', created_by = '',
		revoked_by = '', removed_by = '', purged_at = ? WHERE client_id = ?`, s.clock().Format(timeFormat), clientID); err != nil {
		return fmt.Errorf("agent: purge: %w", err)
	}
	return nil
}

// Name returns the display name of an agent, also of a removed one whose data was
// deleted.
func (s *Store) Name(ctx context.Context, clientID string) (string, error) {
	var name string
	err := s.db.QueryRowContext(ctx, `SELECT display_name FROM agents WHERE client_id = ?`, clientID).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("agent: read: %w", err)
	}
	return name, nil
}

// Disconnected returns the agents a human may reconnect for client (issue #22): active,
// admitted with this OAuth client (the same client ID, verified alike) and without any
// valid token, e.g. after an emergency stop. Several agents can share an OAuth client, so
// the list is an offer to a human, never a match.
func (s *Store) Disconnected(ctx context.Context, client Client) ([]Agent, error) {
	if client.ID == "" {
		return nil, nil
	}
	return s.query(ctx, `WHERE a.status = 'active' AND a.oauth_client = ? AND a.client_verified = ?
		AND NOT EXISTS (SELECT 1 FROM tokens t WHERE t.client_id = a.client_id AND `+validToken+`)`,
		client.ID, client.Verified, s.clock().Format(timeFormat))
}

// ReconnectTx gives an existing agent new tokens inside tx instead of admitting it anew
// (SPEC-v0 section 11.3): the agent keeps its client ID, its mandates and its history;
// withdrawn tokens stay invalid. Only a human reconnects, only an active agent that was
// admitted with client and holds no valid token, and never during the emergency stop.
// The reconnection is recorded as agent.reconnected with by as actor.
func (s *Store) ReconnectTx(ctx context.Context, tx *sql.Tx, clientID string, client Client, resource string, by audit.Actor) (TokenPair, error) {
	if by.Kind != audit.ActorUser || by.ID == "" {
		return TokenPair{}, errors.New("agent: only a human reconnects an agent")
	}
	var name, status, oauthClient string
	var verified, connected bool
	err := tx.QueryRowContext(ctx, `SELECT a.display_name, a.status, a.oauth_client, a.client_verified,
			EXISTS (SELECT 1 FROM tokens t WHERE t.client_id = a.client_id AND `+validToken+`)
		FROM agents a WHERE a.client_id = ? AND a.purged_at IS NULL`, s.clock().Format(timeFormat), clientID).
		Scan(&name, &status, &oauthClient, &verified, &connected)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return TokenPair{}, ErrNotFound
	case err != nil:
		return TokenPair{}, fmt.Errorf("agent: read: %w", err)
	case status != StatusActive:
		return TokenPair{}, ErrRevoked
	case client.ID == "" || oauthClient != client.ID || verified != client.Verified || connected:
		return TokenPair{}, ErrNotReconnectable
	}
	tokens, err := s.IssueTokensTx(ctx, tx, clientID, resource)
	if err != nil {
		return TokenPair{}, err
	}
	if _, err := s.log.AppendTx(ctx, tx, audit.Entry{Event: audit.EventAgentReconnected, Actor: &by,
		Agent: &audit.Agent{ClientID: clientID, DisplayName: name}}); err != nil {
		return TokenPair{}, err
	}
	return tokens, nil
}
