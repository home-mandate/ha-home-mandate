// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/audit"
)

// Token lifetimes (ARCHITECTURE section 6).
const (
	AccessTokenTTL  = 10 * time.Minute
	RefreshTokenTTL = 30 * 24 * time.Hour
)

var (
	// ErrInvalidGrant means a refresh token is unknown, malformed, expired, revoked,
	// reused, bound to another resource, or its agent is revoked.
	ErrInvalidGrant = errors.New("agent: invalid grant")
	// ErrRefreshReused means a rotated refresh token was presented again; its whole
	// family has been revoked. It is also an ErrInvalidGrant.
	ErrRefreshReused = fmt.Errorf("%w: refresh token reused", ErrInvalidGrant)
	// ErrRefreshWrongClient means a refresh token was presented by another OAuth client
	// than the agent's; nothing is revoked. It is also an ErrInvalidGrant.
	ErrRefreshWrongClient = fmt.Errorf("%w: refresh token of another client", ErrInvalidGrant)
	// ErrEmergencyStop means no tokens are issued while the emergency stop is active.
	ErrEmergencyStop = errors.New("agent: emergency stop active")
)

const (
	accessPrefix  = "hma_"
	refreshPrefix = "hmr_"
	tokenBytes    = 32

	kindAccess  = "access"
	kindRefresh = "refresh"

	settingEmergencyStop = "emergency_stop"
	// settingEmergencyStopBy is who switched the stop on; written with it.
	settingEmergencyStopBy = "emergency_stop_by"
	stopOn                 = "on"
	stopOff                = "off"

	errRefreshReused      = "refresh_token_reused"
	errRefreshWrongClient = "refresh_token_wrong_client"
)

var tokenLen = len(accessPrefix) + base64.RawURLEncoding.EncodedLen(tokenBytes)

// TokenPair is the result of issuing or refreshing: an access token valid until
// ExpiresAt and a refresh token for the next pair. Both are returned once and never
// stored.
type TokenPair struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
}

// IssueTokens starts a new token family for an active agent, bound to resource.
func (s *Store) IssueTokens(ctx context.Context, clientID, resource string) (TokenPair, error) {
	var p TokenPair
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var err error
		p, err = s.IssueTokensTx(ctx, tx, clientID, resource)
		return err
	})
	return p, err
}

// IssueTokensTx is IssueTokens inside tx, so that admitting an agent and issuing its
// first tokens commit together.
func (s *Store) IssueTokensTx(ctx context.Context, tx *sql.Tx, clientID, resource string) (TokenPair, error) {
	if resource == "" {
		return TokenPair{}, errors.New("agent: tokens need a resource")
	}
	stopped, err := emergencyStop(ctx, tx)
	if err != nil {
		return TokenPair{}, err
	}
	if stopped {
		return TokenPair{}, ErrEmergencyStop
	}
	var status string
	err = tx.QueryRowContext(ctx, `SELECT status FROM agents WHERE client_id = ?`, clientID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return TokenPair{}, ErrNotFound
	}
	if err != nil {
		return TokenPair{}, fmt.Errorf("agent: read: %w", err)
	}
	if status != StatusActive {
		return TokenPair{}, ErrRevoked
	}
	return s.insertPair(ctx, tx, clientID, newFamilyID(), resource)
}

func (s *Store) insertPair(ctx context.Context, tx *sql.Tx, clientID, family, resource string) (TokenPair, error) {
	now := s.clock()
	p := TokenPair{AccessToken: newToken(accessPrefix), RefreshToken: newToken(refreshPrefix), ExpiresAt: now.Add(AccessTokenTTL)}
	for _, t := range []struct {
		token, kind string
		ttl         time.Duration
	}{{p.AccessToken, kindAccess, AccessTokenTTL}, {p.RefreshToken, kindRefresh, RefreshTokenTTL}} {
		if _, err := tx.ExecContext(ctx, `INSERT INTO tokens (token_hash, kind, client_id, family_id, resource, created_at, expires_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, hashToken(t.token), t.kind, clientID, family, resource,
			now.Format(timeFormat), now.Add(t.ttl).Format(timeFormat)); err != nil {
			return TokenPair{}, fmt.Errorf("agent: store token: %w", err)
		}
	}
	return p, nil
}

// Authenticate returns the agent an access token belongs to, if the token is valid for
// resource. Every failure is ErrUnauthorized, except database errors. The emergency
// stop, the agent's status and the token's revocation are read on every call.
func (s *Store) Authenticate(ctx context.Context, token, resource string) (Agent, error) {
	return s.authenticate(ctx, token, resource, true)
}

// StillAuthorized is Authenticate without the token's expiry: whether the access token
// that made an approval request still counts when the answer comes, possibly after the
// token expired (issue #27). A revocation of the token, its family or the agent, the
// emergency stop and another resource still fail.
func (s *Store) StillAuthorized(ctx context.Context, token, resource string) (Agent, error) {
	return s.authenticate(ctx, token, resource, false)
}

func (s *Store) authenticate(ctx context.Context, token, resource string, checkExpiry bool) (Agent, error) {
	if resource == "" || !wellFormed(token, accessPrefix) {
		return Agent{}, ErrUnauthorized
	}
	var a Agent
	var createdAt, kind, tokenResource, expiresAt, stop string
	var revokedAt sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT a.client_id, a.display_name, a.status, a.created_at, a.created_by,
			a.oauth_client, a.client_verified, t.kind, t.resource, t.expires_at, t.revoked_at,
			COALESCE((SELECT value FROM settings WHERE key = ?), ?)
		FROM tokens t JOIN agents a ON a.client_id = t.client_id WHERE t.token_hash = ?`,
		settingEmergencyStop, stopOff, hashToken(token)).
		Scan(&a.ClientID, &a.DisplayName, &a.Status, &createdAt, &a.CreatedBy, &a.OAuthClient, &a.ClientVerified,
			&kind, &tokenResource, &expiresAt, &revokedAt, &stop)
	if errors.Is(err, sql.ErrNoRows) {
		return Agent{}, ErrUnauthorized
	}
	if err != nil {
		return Agent{}, fmt.Errorf("agent: authenticate: %w", err)
	}
	expires, err := time.Parse(timeFormat, expiresAt)
	if err != nil || kind != kindAccess || tokenResource != resource || revokedAt.Valid || a.Status != StatusActive ||
		stop != stopOff || checkExpiry && !s.clock().Before(expires) {
		return Agent{}, ErrUnauthorized
	}
	a.CreatedAt, _ = time.Parse(timeFormat, createdAt)
	return a, nil
}

// refreshRow is a refresh token with its agent.
type refreshRow struct {
	kind, clientID, name, family, resource, expiresAt, status, oauthClient string
	usedAt, revokedAt                                                      sql.NullString
}

// Refresh rotates a refresh token: it can be used once and yields a new pair in the
// same family. It must be presented by the OAuth client the agent was admitted with;
// resource, if not empty, must be the one the family is bound to. Presenting a used
// refresh token revokes the whole family and is logged as auth.rejected
// (ErrRefreshReused). The client is checked first: a token presented by another client
// is logged as auth.rejected but revokes nothing (ErrRefreshWrongClient), so that a
// replay by someone else cannot cut the agent off.
func (s *Store) Refresh(ctx context.Context, token, resource, oauthClient string) (TokenPair, error) {
	if !wellFormed(token, refreshPrefix) {
		return TokenPair{}, ErrInvalidGrant
	}
	var p TokenPair
	var rejected error // ErrRefreshReused or ErrRefreshWrongClient, after the commit
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var r refreshRow
		err := tx.QueryRowContext(ctx, `SELECT t.kind, t.client_id, a.display_name, t.family_id, t.resource, t.expires_at,
				a.status, a.oauth_client, t.used_at, t.revoked_at
			FROM tokens t JOIN agents a ON a.client_id = t.client_id WHERE t.token_hash = ?`, hashToken(token)).
			Scan(&r.kind, &r.clientID, &r.name, &r.family, &r.resource, &r.expiresAt, &r.status, &r.oauthClient, &r.usedAt, &r.revokedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInvalidGrant
		}
		if err != nil {
			return fmt.Errorf("agent: refresh: %w", err)
		}
		if r.kind != kindRefresh {
			return ErrInvalidGrant
		}
		if r.oauthClient != oauthClient {
			rejected = ErrRefreshWrongClient
			return s.rejectRefresh(ctx, tx, r, errRefreshWrongClient)
		}
		if r.usedAt.Valid {
			rejected = ErrRefreshReused
			return s.revokeFamily(ctx, tx, r)
		}
		if err := s.checkRefresh(ctx, tx, r, resource); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `UPDATE tokens SET used_at = ? WHERE token_hash = ? AND used_at IS NULL`,
			s.clock().Format(timeFormat), hashToken(token))
		if err != nil {
			return fmt.Errorf("agent: refresh: %w", err)
		}
		if n, err := res.RowsAffected(); err != nil || n != 1 {
			return ErrInvalidGrant // cannot happen with serialized write transactions
		}
		p, err = s.insertPair(ctx, tx, r.clientID, r.family, r.resource)
		return err
	})
	if err == nil && rejected != nil {
		return TokenPair{}, rejected
	}
	return p, err
}

// checkRefresh rejects an unused refresh token that may still not be rotated.
func (s *Store) checkRefresh(ctx context.Context, tx *sql.Tx, r refreshRow, resource string) error {
	expires, err := time.Parse(timeFormat, r.expiresAt)
	if err != nil || r.revokedAt.Valid || r.status != StatusActive || !s.clock().Before(expires) ||
		resource != "" && resource != r.resource {
		return ErrInvalidGrant
	}
	stopped, err := emergencyStop(ctx, tx)
	if err != nil {
		return err
	}
	if stopped {
		return ErrInvalidGrant
	}
	return nil
}

// revokeFamily revokes every token of a family after a refresh token was reused.
func (s *Store) revokeFamily(ctx context.Context, tx *sql.Tx, r refreshRow) error {
	if _, err := tx.ExecContext(ctx, `UPDATE tokens SET revoked_at = ? WHERE family_id = ? AND revoked_at IS NULL`,
		s.clock().Format(timeFormat), r.family); err != nil {
		return fmt.Errorf("agent: revoke token family: %w", err)
	}
	return s.rejectRefresh(ctx, tx, r, errRefreshReused)
}

// rejectRefresh logs a refused refresh token of r's agent as auth.rejected.
func (s *Store) rejectRefresh(ctx context.Context, tx *sql.Tx, r refreshRow, code string) error {
	_, err := s.log.AppendTx(ctx, tx, audit.Entry{Event: audit.EventAuthRejected,
		Agent:  &audit.Agent{ClientID: r.clientID, DisplayName: r.name},
		Result: &audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByAuthentication, Error: code}})
	return err
}

// EmergencyStopActive reports whether the emergency stop is active.
func (s *Store) EmergencyStopActive(ctx context.Context) (bool, error) {
	var stopped bool
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var err error
		stopped, err = emergencyStop(ctx, tx)
		return err
	})
	return stopped, err
}

// SetEmergencyStop activates or releases the emergency stop and reports whether that
// changed anything. Activating revokes every token at once; releasing does not restore
// them, agents need new tokens.
func (s *Store) SetEmergencyStop(ctx context.Context, on bool, by audit.Actor) (bool, error) {
	changed := false
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		stopped, err := emergencyStop(ctx, tx)
		if err != nil || stopped == on {
			return err
		}
		now := s.clock().Format(timeFormat)
		value, event := stopOff, audit.EventEmergencyStopReleased
		if on {
			value, event = stopOn, audit.EventEmergencyStopActivated
			if _, err := tx.ExecContext(ctx, `UPDATE tokens SET revoked_at = ? WHERE revoked_at IS NULL`, now); err != nil {
				return fmt.Errorf("agent: revoke all tokens: %w", err)
			}
		}
		for key, v := range map[string]string{settingEmergencyStop: value, settingEmergencyStopBy: by.ID} {
			if _, err := tx.ExecContext(ctx, `INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
				ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`, key, v, now); err != nil {
				return fmt.Errorf("agent: write emergency stop: %w", err)
			}
		}
		if _, err := s.log.AppendTx(ctx, tx, audit.Entry{Event: event, Actor: &by}); err != nil {
			return err
		}
		changed = true
		return nil
	})
	return changed, err
}

// StopState is the emergency stop as the UI shows it.
type StopState struct {
	Active bool
	Since  time.Time // when it was switched on; zero while off
	By     string    // Home Assistant user ID (or local-admin) who switched it on
}

// EmergencyStopState returns whether the stop is on, since when and by whom. The time is
// that of its own setting, which outlives the 30 days of the audit log.
func (s *Store) EmergencyStopState(ctx context.Context) (StopState, error) {
	var value, updated string
	err := s.db.QueryRowContext(ctx, `SELECT value, updated_at FROM settings WHERE key = ?`, settingEmergencyStop).Scan(&value, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return StopState{}, nil
	}
	if err != nil {
		return StopState{}, fmt.Errorf("agent: read emergency stop: %w", err)
	}
	if value == stopOff {
		return StopState{}, nil
	}
	st := StopState{Active: true}
	st.Since, _ = time.Parse(timeFormat, updated)
	err = s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, settingEmergencyStopBy).Scan(&st.By)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return StopState{}, fmt.Errorf("agent: read emergency stop: %w", err)
	}
	return st, nil
}

func emergencyStop(ctx context.Context, tx *sql.Tx) (bool, error) {
	var value string
	err := tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, settingEmergencyStop).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("agent: read emergency stop: %w", err)
	}
	return value != stopOff, nil // anything but "off" counts as stopped
}

// PurgeExpiredTokens deletes tokens that expired before cutoff. Used refresh tokens are
// kept until they expire, so that their reuse is still detected.
func (s *Store) PurgeExpiredTokens(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM tokens WHERE expires_at < ?`, cutoff.UTC().Format(timeFormat))
	if err != nil {
		return 0, fmt.Errorf("agent: purge tokens: %w", err)
	}
	return res.RowsAffected()
}

// newToken returns prefix + 256 bits from crypto/rand, base64url without padding.
func newToken(prefix string) string {
	var raw [tokenBytes]byte
	_, _ = rand.Read(raw[:]) // crypto/rand.Read never fails (Go ≥ 1.24)
	return prefix + base64.RawURLEncoding.EncodeToString(raw[:])
}

func newFamilyID() string {
	var raw [16]byte
	_, _ = rand.Read(raw[:])
	return hex.EncodeToString(raw[:])
}

// wellFormed checks prefix, length and alphabet before the database is asked.
func wellFormed(token, prefix string) bool {
	if len(token) != tokenLen || !strings.HasPrefix(token, prefix) {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(token[len(prefix):])
	return err == nil && len(raw) == tokenBytes
}

func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}
