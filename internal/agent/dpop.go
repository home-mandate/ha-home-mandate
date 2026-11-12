// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/home-mandate/ha-home-mandate/internal/audit"
	"github.com/home-mandate/ha-home-mandate/internal/dpop"
)

// Bindings of an agent (agents.binding, migration 0022).
const (
	// BindingNone: the agent's tokens are Bearer tokens.
	BindingNone = "none"
	// BindingDPoP: the agent got a DPoP-bound token once; all its tokens are bound from
	// then on. There is no way back but removing the agent and pairing anew (issue #63).
	BindingDPoP = "dpop"
)

var (
	// ErrDPoP is the kind of a refusal for the DPoP key (error invalid_dpop_proof).
	ErrDPoP = errors.New("agent: DPoP")
	// ErrDPoPKey: the tokens are bound to another key than the request proved, or the
	// request proved none.
	ErrDPoPKey = fmt.Errorf("%w: proof of another key", ErrDPoP)
	// ErrDPoPRequired: the tokens would be Bearer tokens, but the agent is bound or the
	// request requires binding.
	ErrDPoPRequired = fmt.Errorf("%w: proof required", ErrDPoP)
)

// Audit error codes of the refusals (auth.rejected, denied_by authentication).
const (
	errInvalidDPoPProof = "invalid_dpop_proof"
	errDPoPRequired     = "dpop_required"
)

// Holder is what a token request proved about the client's DPoP key (RFC 9449).
type Holder struct {
	// JKT is the thumbprint of the key of the request's valid proof; empty without one.
	JKT string
	// Bind lets tokens that are not bound yet become bound to JKT: DPoP is offered (mode
	// optional or required). A bound agent binds regardless.
	Bind bool
	// Require refuses tokens that would not be bound: mode required, or a client whose
	// metadata asks for bound tokens.
	Require bool
}

func (h Holder) check() error {
	if h.JKT != "" && !dpop.ValidThumbprint(h.JKT) {
		return errors.New("agent: malformed DPoP thumbprint")
	}
	return nil
}

// dpopRefusal carries a refusal for the key with its audit code out of decideKey.
type dpopRefusal struct {
	err  error
	code string
}

func (r dpopRefusal) Error() string { return r.err.Error() }
func (r dpopRefusal) Unwrap() error { return r.err }

// decideKey is the one rule for the key a family's next tokens are bound to; it returns
// that key, empty for Bearer tokens. handOut is true for a pair just issued after a human's
// approval (Bind), false for a refresh:
//
//   - a bound family keeps its key; a request without a proof of it is refused (ErrDPoPKey);
//   - an unbound family of a bound agent takes a new key only at the hand-out: at a refresh
//     it is a family from before, whose stolen refresh token must not bind it to the thief's
//     key (ErrDPoPRequired);
//   - an unbound family becomes bound to the proved key if binding is offered or the agent
//     is bound already;
//   - otherwise the tokens stay Bearer tokens, unless the agent is bound or the request
//     requires binding (ErrDPoPRequired).
func (s *Store) decideKey(ctx context.Context, tx *sql.Tx, family, binding string, h Holder, handOut bool) (string, error) {
	var bound sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT dpop_jkt FROM tokens WHERE family_id = ? AND dpop_jkt IS NOT NULL LIMIT 1`, family).Scan(&bound)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("agent: read token binding: %w", err)
	}
	switch {
	case bound.Valid && h.JKT != bound.String:
		return "", dpopRefusal{ErrDPoPKey, errInvalidDPoPProof}
	case bound.Valid:
		return bound.String, nil
	case binding == BindingDPoP && !handOut:
		return "", dpopRefusal{ErrDPoPRequired, errDPoPRequired}
	case h.JKT != "" && (h.Bind || binding == BindingDPoP):
		return h.JKT, nil
	case binding == BindingDPoP || h.Require:
		return "", dpopRefusal{ErrDPoPRequired, errDPoPRequired}
	}
	return "", nil
}

// keyFits reports whether a request that proved jkt (empty for Bearer) may use a token
// bound to tokenJKT (empty for an unbound token) of an agent with binding.
func keyFits(binding, tokenJKT, jkt string) bool {
	if tokenJKT != "" {
		return jkt == tokenJKT
	}
	return jkt == "" && binding != BindingDPoP
}

// markBound records that the agent's tokens are bound from now on, and withdraws its
// other unbound families: they could only ever be refused (decideKey, keyFits). The family
// that was just bound keeps its earlier rows, so that a retry within the grace period of a
// refresh (#71) still finds them.
func (s *Store) markBound(ctx context.Context, tx *sql.Tx, clientID, family, jkt string) error {
	if jkt == "" {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE agents SET binding = ? WHERE client_id = ? AND binding <> ?`,
		BindingDPoP, clientID, BindingDPoP); err != nil {
		return fmt.Errorf("agent: bind agent: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE tokens SET revoked_at = ?
		WHERE client_id = ? AND family_id <> ? AND dpop_jkt IS NULL AND revoked_at IS NULL`,
		s.clock().Format(timeFormat), clientID, family); err != nil {
		return fmt.Errorf("agent: withdraw unbound tokens: %w", err)
	}
	return nil
}

// Bind decides the binding of a pair just issued by an admission or a reconnect, at the
// moment it is handed out: the token request that fetches it carries the proof, which for
// a pairing comes only after the human's approval. The pair's family is bound to h.JKT by
// the rule of decideKey. If the pair may not be handed out as Bearer tokens, its family is
// revoked, the refusal is logged as auth.rejected, and the result is ErrDPoPRequired.
func (s *Store) Bind(ctx context.Context, p TokenPair, h Holder) (TokenPair, error) {
	if err := h.check(); err != nil {
		return TokenPair{}, err
	}
	var rejected error
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var r refreshRow
		err := tx.QueryRowContext(ctx, `SELECT t.client_id, a.display_name, t.family_id, a.binding
			FROM tokens t JOIN agents a ON a.client_id = t.client_id WHERE t.token_hash = ? AND t.kind = ?`,
			hashToken(p.RefreshToken), kindRefresh).Scan(&r.clientID, &r.name, &r.family, &r.binding)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInvalidGrant
		}
		if err != nil {
			return fmt.Errorf("agent: bind: %w", err)
		}
		jkt, err := s.decideKey(ctx, tx, r.family, r.binding, h, true)
		var refusal dpopRefusal
		switch {
		case errors.As(err, &refusal) && errors.Is(err, ErrDPoPRequired):
			rejected = ErrDPoPRequired
			return s.revokeRefused(ctx, tx, r)
		case err != nil:
			return err
		}
		if jkt == "" {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE tokens SET dpop_jkt = ? WHERE family_id = ? AND dpop_jkt IS NULL`, jkt, r.family); err != nil {
			return fmt.Errorf("agent: bind tokens: %w", err)
		}
		p.JKT = jkt
		return s.markBound(ctx, tx, r.clientID, r.family, jkt)
	})
	if err == nil && rejected != nil {
		return TokenPair{}, rejected
	}
	if err != nil {
		return TokenPair{}, err
	}
	return p, nil
}

// revokeRefused revokes the family of a pair that may not be handed out and logs why.
func (s *Store) revokeRefused(ctx context.Context, tx *sql.Tx, r refreshRow) error {
	if _, err := tx.ExecContext(ctx, `UPDATE tokens SET revoked_at = ? WHERE family_id = ? AND revoked_at IS NULL`,
		s.clock().Format(timeFormat), r.family); err != nil {
		return fmt.Errorf("agent: revoke refused tokens: %w", err)
	}
	_, err := s.log.AppendTx(ctx, tx, audit.Entry{Event: audit.EventAuthRejected,
		Agent:  &audit.Agent{ClientID: r.clientID, DisplayName: r.name},
		Result: &audit.Result{Status: audit.StatusDenied, DeniedBy: audit.DeniedByAuthentication, Error: errDPoPRequired}})
	return err
}

// nullable is NULL for an empty string.
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
