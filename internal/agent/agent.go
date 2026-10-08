// SPDX-License-Identifier: AGPL-3.0-or-later

// Package agent manages agents, their OAuth tokens and the emergency stop. Tokens are
// 256-bit random values from crypto/rand; only their SHA-256 hash is stored. Every
// registration, revocation and emergency stop is written to the audit log in the same
// transaction.
package agent

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/home-mandate/ha-home-mandate/internal/audit"
	"github.com/home-mandate/ha-home-mandate/internal/untrusted"
)

var (
	// ErrNotFound means there is no agent with that client ID.
	ErrNotFound = errors.New("agent: not found")
	// ErrInvalidName means the display name is empty, too long or contains control,
	// format or separator characters (SPEC-v0 section 3.1 item 8).
	ErrInvalidName = errors.New("agent: invalid display name")
	// ErrRevoked means the agent was revoked.
	ErrRevoked = errors.New("agent: revoked")
	// ErrUnauthorized means a token is unknown, malformed, expired, revoked or bound to
	// another resource, its agent is revoked or the emergency stop is active. Callers must not tell these cases apart to the client.
	ErrUnauthorized = errors.New("agent: unauthorized")
)

// Agent statuses.
const (
	StatusActive  = "active"
	StatusRevoked = "revoked"
)

const (
	clientIDNamespace = "hm-client:"
	maxNameRunes      = 80
	maxSlugLen        = 24
	timeFormat        = time.RFC3339Nano
)

// Agent is a registered agent.
type Agent struct {
	ClientID    string
	DisplayName string
	Status      string
	CreatedAt   time.Time
	CreatedBy   string
	// OAuthClient is the OAuth client ID the agent was admitted with: a Client ID
	// Metadata Document URL (ClientVerified) or a free identifier from a pairing code.
	OAuthClient    string
	ClientVerified bool
	// RedirectURIs are the redirect URIs of the client at admission; never widened later.
	RedirectURIs []string
	// RevokedAt and RevokedBy are set once the agent is revoked.
	RevokedAt time.Time
	RevokedBy string
}

// Client is the OAuth client an agent is admitted with.
type Client struct {
	ID           string
	Verified     bool
	RedirectURIs []string
}

// Store manages agents in the database opened by internal/store.
type Store struct {
	db  *sql.DB
	log *audit.Log

	mu  sync.Mutex
	now func() time.Time
}

// New returns a Store that records changes in log.
func New(db *sql.DB, log *audit.Log) *Store {
	return &Store{db: db, log: log, now: time.Now}
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

// Register creates an agent with a new client ID of the form hm-client:<slug>-<hex>.
func (s *Store) Register(ctx context.Context, displayName string, by audit.Actor) (Agent, error) {
	var a Agent
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var err error
		a, err = s.RegisterTx(ctx, tx, displayName, Client{}, by)
		return err
	})
	if err != nil {
		return Agent{}, err
	}
	return a, nil
}

// RegisterTx registers an agent inside tx for the OAuth client it is admitted with.
func (s *Store) RegisterTx(ctx context.Context, tx *sql.Tx, displayName string, client Client, by audit.Actor) (Agent, error) {
	name, err := validName(displayName)
	if err != nil {
		return Agent{}, err
	}
	uris := client.RedirectURIs
	if uris == nil {
		uris = []string{}
	}
	encoded, err := json.Marshal(uris)
	if err != nil {
		return Agent{}, fmt.Errorf("agent: redirect URIs: %w", err)
	}
	a := Agent{ClientID: newClientID(name), DisplayName: name, Status: StatusActive, CreatedAt: s.clock(), CreatedBy: by.ID,
		OAuthClient: client.ID, ClientVerified: client.Verified, RedirectURIs: uris}
	if _, err := tx.ExecContext(ctx, `INSERT INTO agents (client_id, display_name, status, created_at, created_by, oauth_client, client_verified, redirect_uris)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, a.ClientID, a.DisplayName, a.Status, a.CreatedAt.Format(timeFormat), a.CreatedBy,
		a.OAuthClient, a.ClientVerified, string(encoded)); err != nil {
		return Agent{}, fmt.Errorf("agent: insert: %w", err)
	}
	_, err = s.log.AppendTx(ctx, tx, audit.Entry{Event: audit.EventAgentRegistered, Actor: &by,
		Agent: &audit.Agent{ClientID: a.ClientID, DisplayName: a.DisplayName}})
	if err != nil {
		return Agent{}, err
	}
	return a, nil
}

// Revoke revokes the agent and all its tokens at once. Revoking a revoked agent is a
// no-op.
func (s *Store) Revoke(ctx context.Context, clientID string, by audit.Actor) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		_, err := s.RevokeTx(ctx, tx, clientID, by)
		return err
	})
}

// RevokeTx is Revoke inside tx, so that the agent, its tokens and its mandate end in
// one transaction. It reports whether the agent was active until now.
func (s *Store) RevokeTx(ctx context.Context, tx *sql.Tx, clientID string, by audit.Actor) (bool, error) {
	var status, name string
	err := tx.QueryRowContext(ctx, `SELECT status, display_name FROM agents WHERE client_id = ?`, clientID).Scan(&status, &name)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, fmt.Errorf("agent: read: %w", err)
	}
	if status == StatusRevoked {
		return false, nil
	}
	now := s.clock().Format(timeFormat)
	if _, err := tx.ExecContext(ctx, `UPDATE agents SET status = ?, revoked_at = ?, revoked_by = ? WHERE client_id = ?`,
		StatusRevoked, now, by.ID, clientID); err != nil {
		return false, fmt.Errorf("agent: revoke: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE tokens SET revoked_at = ? WHERE client_id = ? AND revoked_at IS NULL`, now, clientID); err != nil {
		return false, fmt.Errorf("agent: revoke tokens: %w", err)
	}
	if _, err := s.log.AppendTx(ctx, tx, audit.Entry{Event: audit.EventAgentRevoked, Actor: &by,
		Agent: &audit.Agent{ClientID: clientID, DisplayName: name}}); err != nil {
		return false, err
	}
	return true, nil
}

// Get returns the agent with clientID.
func (s *Store) Get(ctx context.Context, clientID string) (Agent, error) {
	rows, err := s.query(ctx, `WHERE client_id = ?`, clientID)
	if err != nil {
		return Agent{}, err
	}
	if len(rows) == 0 {
		return Agent{}, ErrNotFound
	}
	return rows[0], nil
}

// List returns all agents in registration order.
func (s *Store) List(ctx context.Context) ([]Agent, error) {
	return s.query(ctx, ``)
}

func (s *Store) query(ctx context.Context, where string, args ...any) ([]Agent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT client_id, display_name, status, created_at, created_by, oauth_client, client_verified,
			redirect_uris, coalesce(revoked_at, ''), revoked_by
		FROM agents `+where+` ORDER BY rowid`, args...)
	if err != nil {
		return nil, fmt.Errorf("agent: query: %w", err)
	}
	defer rows.Close()
	var agents []Agent
	for rows.Next() {
		var a Agent
		var createdAt, uris, revokedAt string
		if err := rows.Scan(&a.ClientID, &a.DisplayName, &a.Status, &createdAt, &a.CreatedBy, &a.OAuthClient, &a.ClientVerified,
			&uris, &revokedAt, &a.RevokedBy); err != nil {
			return nil, fmt.Errorf("agent: query: %w", err)
		}
		a.CreatedAt, _ = time.Parse(timeFormat, createdAt)
		a.RevokedAt, _ = time.Parse(timeFormat, revokedAt)
		if err := json.Unmarshal([]byte(uris), &a.RedirectURIs); err != nil {
			return nil, fmt.Errorf("agent: redirect URIs of %s: %w", a.ClientID, err)
		}
		agents = append(agents, a)
	}
	return agents, rows.Err()
}

func (s *Store) inTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("agent: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("agent: commit: %w", err)
	}
	return nil
}

// validName trims surrounding spaces and rejects texts that could mislead a human in an
// approval request, and names that show no letter or digit once cleaned as the UI shows
// them (only blank-looking letters, selectors or marks).
func validName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > maxNameRunes || !utf8.ValidString(name) {
		return "", ErrInvalidName
	}
	for _, r := range name {
		if unicode.In(r, unicode.Cc, unicode.Cf, unicode.Zl, unicode.Zp) {
			return "", ErrInvalidName
		}
	}
	if !strings.ContainsFunc(untrusted.Clean(name, untrusted.Max), func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) {
		return "", ErrInvalidName
	}
	return name, nil
}

// ValidName reports whether name is acceptable as a display name, and returns it trimmed.
func ValidName(name string) (string, bool) {
	n, err := validName(name)
	return n, err == nil
}

// newClientID derives a readable, unique client ID from the display name.
func newClientID(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
		if b.Len() >= maxSlugLen {
			break
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		slug = "agent"
	}
	var suffix [4]byte
	_, _ = rand.Read(suffix[:])
	return clientIDNamespace + slug + "-" + hex.EncodeToString(suffix[:])
}
