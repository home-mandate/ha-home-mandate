// SPDX-License-Identifier: AGPL-3.0-or-later

package audit

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"errors"
	"fmt"

	specaudit "github.com/home-mandate/spec/audit"
)

// ErrNoSigner means the log has no key to sign checkpoints with.
var ErrNoSigner = errors.New("audit: no signer for checkpoints")

// Signer signs the checkpoints of the log (SPEC-v0 section 9.5). The key must not be
// used for anything else.
type Signer struct {
	// LogID identifies this audit log, a UUID in lower case chosen once.
	LogID string
	// KeyID names the key in the signature.
	KeyID string
	Key   ed25519.PrivateKey
}

// Position is what a checkpoint covers: the log up to and including the entry Seq with
// the digest Digest. A party outside the log that remembers it can later tell whether the
// log was rewritten.
type Position struct {
	LogID  string
	Seq    int64
	Digest string
}

// checkpoint is the member of a log.checkpoint entry.
type checkpoint struct {
	LogID     string `json:"log_id"`
	Signature string `json:"signature"`
}

// SetSigner sets the key for checkpoints; nil turns them off. With a signer, Verify
// also checks the signatures and reports how far the log is anchored.
func (l *Log) SetSigner(s *Signer) {
	l.signerMu.Lock()
	defer l.signerMu.Unlock()
	l.checkpointSigner = s
}

func (l *Log) signer() *Signer {
	l.signerMu.Lock()
	defer l.signerMu.Unlock()
	return l.checkpointSigner
}

// HasCheckpoints reports whether the log holds a checkpoint: then only the key that
// signed it can verify the log, and a new key must not take its place.
func (l *Log) HasCheckpoints(ctx context.Context) (bool, error) {
	var found bool
	if err := l.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM audit_log WHERE event = ?)`,
		EventLogCheckpoint).Scan(&found); err != nil {
		return false, fmt.Errorf("audit: read: %w", err)
	}
	return found, nil
}

// sign returns the checkpoint over the log up to the entry seq with the digest.
func (l *Log) sign(seq int64, digest string) (*checkpoint, error) {
	s := l.signer()
	if s == nil {
		return nil, ErrNoSigner
	}
	signature, err := specaudit.SignCheckpoint(s.LogID, seq, digest, s.KeyID, s.Key)
	if err != nil {
		return nil, fmt.Errorf("audit: sign checkpoint: %w", err)
	}
	return &checkpoint{LogID: s.LogID, Signature: signature}, nil
}

// Checkpoint appends a signed checkpoint over everything written so far and returns the
// position it covers. It writes nothing and returns the zero Position if the log is
// empty or its last entry is a checkpoint already.
func (l *Log) Checkpoint(ctx context.Context) (Position, error) {
	s := l.signer()
	if s == nil {
		return Position{}, ErrNoSigner
	}
	var pos Position
	err := l.inTx(ctx, func(tx *sql.Tx) error {
		var seq int64
		var digest, event string
		err := tx.QueryRowContext(ctx, `SELECT seq, digest, event FROM audit_log ORDER BY seq DESC LIMIT 1`).Scan(&seq, &digest, &event)
		if errors.Is(err, sql.ErrNoRows) || err == nil && event == EventLogCheckpoint {
			return nil
		}
		if err != nil {
			return fmt.Errorf("audit: read chain head: %w", err)
		}
		if _, err := l.AppendTx(ctx, tx, Entry{Event: EventLogCheckpoint, checkpoint: true}); err != nil {
			return err
		}
		pos = Position{LogID: s.LogID, Seq: seq, Digest: digest}
		return nil
	})
	if err != nil {
		return Position{}, err
	}
	return pos, nil
}
