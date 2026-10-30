// SPDX-License-Identifier: AGPL-3.0-or-later

package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"time"

	specaudit "github.com/home-mandate/spec/audit"
	"github.com/home-mandate/spec/jcs"
	"github.com/home-mandate/spec/jws"
)

// ErrChainBroken means a log in the earlier format does not form an intact chain, or a
// checkpoint in it does not verify with this installation's key: it is left as it is,
// so that start-up reports it, and nothing is archived that hides it.
var ErrChainBroken = errors.New("audit: the chain of the log is broken")

// endOfTime is a cutoff after every entry.
var endOfTime = time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)

// legacyEntry is what an entry of the earlier format is recognized by: a decision entry
// with the single area of its resource (request.resource.area), which the audit schema no
// longer has since a resource has a set of areas (SPEC-v0 section 3.4), or a checkpoint
// whose signature covers the earlier payload, without the time of the entry (SPEC-v0
// section 9.5).
type legacyEntry struct {
	Seq        int64   `json:"seq"`
	Prev       *string `json:"prev"`
	Event      string  `json:"event"`
	RecordedAt string  `json:"recorded_at"`
	Checkpoint *struct {
		LogID     string `json:"log_id"`
		Signature string `json:"signature"`
	} `json:"checkpoint"`
	Request *struct {
		Resource struct {
			Area  *string   `json:"area"`
			Areas *[]string `json:"areas"`
		} `json:"resource"`
	} `json:"request"`
}

// checkpointType is the type of the statement a checkpoint signs (SPEC-v0 section 9.5).
const checkpointType = "https://home-mandate.org/audit-checkpoint/v0"

// ArchiveLegacy moves a log that an earlier version wrote, and that no longer verifies
// for that reason alone (decision entries with a single area, or checkpoints signed over
// the earlier payload without the entry's time), out of the way: it writes every entry
// unchanged to the archive (JSON Lines, verifiable with the version that wrote it),
// appends a checkpoint, and deletes the entries before it with a log.truncated entry and
// another checkpoint, so that the log verifies and is anchored again; the caller verifies
// it. It does nothing to a valid log, to an invalid one without an entry of the earlier
// format, and to one whose chain is broken (ErrChainBroken). It returns the number of
// archived entries.
func (l *Log) ArchiveLegacy(ctx context.Context, archive func() (io.WriteCloser, error), actor Actor) (int, error) {
	var entries [][]byte
	if err := l.each(ctx, func(entry []byte) error {
		entries = append(entries, entry)
		return nil
	}); err != nil {
		return 0, err
	}
	if r, err := l.verifyEntries(entries); err != nil || r.Valid {
		return 0, err
	}
	signer := l.signer()
	legacy, intact, err := inspect(entries, signer)
	if err != nil || !legacy {
		return 0, err
	}
	if signer == nil {
		return 0, ErrNoSigner
	}
	if !intact {
		return 0, ErrChainBroken
	}
	if err := writeArchive(archive, entries); err != nil {
		return 0, err
	}
	// The newest entry stays when a log is truncated: a checkpoint of this version, also
	// when the last entry is a checkpoint of the earlier version. Checkpoint and truncation
	// commit together: a log with a new checkpoint but the old entries would verify with
	// neither version. The start-up check that follows verifies the result.
	err = l.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := l.checkpointTx(ctx, tx, true); err != nil {
			return err
		}
		_, err := l.truncateTx(ctx, tx, endOfTime, math.MaxInt32, actor)
		return err
	})
	if err != nil {
		return 0, err
	}
	return len(entries), nil
}

// inspect tells whether the log has the earlier format and whether it is intact as that
// format: consecutive sequence numbers, each naming the digest of the one before, every
// checkpoint signed with this installation's key over the entries before it (over the
// earlier or the current payload), and the earlier format only in decision entries
// before every entry of the current one. A checkpoint is checked over the original bytes,
// so an entry changed and chained again by someone without the key is found. Without a
// signer nothing is intact.
func inspect(entries [][]byte, signer *Signer) (legacy, intact bool, err error) {
	intact = signer != nil
	var prevDigest string
	var prevSeq int64
	current := false // an entry of the current format was seen
	for i, data := range entries {
		var e legacyEntry
		if err := json.Unmarshal(data, &e); err != nil {
			return false, false, fmt.Errorf("audit: read entry: %w", err)
		}
		if e.Request != nil && e.Request.Resource.Area != nil {
			legacy = true
			intact = intact && e.Event == EventDecision && !current && e.Request.Resource.Areas == nil
		}
		current = current || e.Request != nil && e.Request.Resource.Areas != nil
		if i > 0 && (e.Seq != prevSeq+1 || e.Prev == nil || *e.Prev != prevDigest) {
			intact = false
		}
		if e.Event == EventLogCheckpoint && intact {
			var earlier bool
			intact, earlier = signedCheckpoint(e, signer)
			legacy = legacy || earlier
		}
		// An entry without a digest breaks the chain at the next one.
		prevDigest, _ = specaudit.Digest(data)
		prevSeq = e.Seq
	}
	return legacy, intact, nil
}

// signedCheckpoint reports whether a log.checkpoint entry carries the signature of signer
// over the log up to the entry before it (SPEC-v0 section 9.5), and whether that
// signature covers the earlier payload, which did not name the time of the entry: such a
// checkpoint no longer verifies and marks a log of an earlier version.
func signedCheckpoint(e legacyEntry, signer *Signer) (signed, earlier bool) {
	if e.Checkpoint == nil || e.Prev == nil || e.Checkpoint.LogID != signer.LogID {
		return false, false
	}
	keys := jws.Keys{signer.KeyID: signer.Key.Public()}
	statement := map[string]any{"type": checkpointType, "log_id": e.Checkpoint.LogID,
		"seq": json.Number(fmt.Sprint(e.Seq - 1)), "digest": *e.Prev, "at": e.RecordedAt}
	if payload, err := jcs.Canonicalize(statement); err == nil {
		if _, err := jws.VerifyDetached(e.Checkpoint.Signature, payload, keys); err == nil {
			return true, false
		}
	}
	delete(statement, "at")
	payload, err := jcs.Canonicalize(statement)
	if err != nil {
		return false, false
	}
	_, err = jws.VerifyDetached(e.Checkpoint.Signature, payload, keys)
	return err == nil, err == nil
}

func writeArchive(archive func() (io.WriteCloser, error), entries [][]byte) error {
	w, err := archive()
	if err != nil {
		return fmt.Errorf("audit: archive: %w", err)
	}
	for _, entry := range entries {
		if _, err := w.Write(append(entry, '\n')); err != nil {
			_ = w.Close()
			return fmt.Errorf("audit: archive: %w", err)
		}
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("audit: archive: %w", err)
	}
	return nil
}
