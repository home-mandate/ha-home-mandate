// SPDX-License-Identifier: AGPL-3.0-or-later

package ctwatch

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ErrStore is the error of the store under the monitoring.
var ErrStore = errors.New("ctwatch: store unavailable")

const (
	timeFormat = "2006-01-02T15:04:05.000Z07:00"
	// maxStoredCerts bounds what is read back of the store per round; a name has a
	// handful of certificates a year, so more means something is wrong upstream.
	maxStoredCerts = 10000
	// maxFailureText bounds the text of a failed query kept for the UI.
	maxFailureText = 256
	maxIssuerText  = 1024
	maxSerialText  = 128
)

// Via says how an acknowledgement was confirmed.
type Via string

const (
	ViaPush Via = "push"
	ViaUI   Via = "ui"
)

// Store keeps the monitoring on the database opened by internal/store.
type Store struct {
	db *sql.DB
}

// NewStore returns the store on db.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

func formatTime(t time.Time) string { return t.UTC().Format(timeFormat) }

func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(timeFormat, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: time %q", ErrStore, s)
	}
	return t, nil
}

func parseNullTime(s sql.NullString) (time.Time, error) {
	if !s.Valid {
		return time.Time{}, nil
	}
	return parseTime(s.String)
}

func storeErr(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %w", ErrStore, err)
}

// WatchSince returns when monitoring of name began, starting it at now if it had not;
// first tells that it began now.
func (s *Store) WatchSince(ctx context.Context, name string, now time.Time) (since time.Time, first bool, err error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO ct_names (name, watch_since) VALUES (?, ?) ON CONFLICT (name) DO NOTHING`,
		name, formatTime(now))
	if err != nil {
		return time.Time{}, false, storeErr(err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return time.Time{}, false, storeErr(err)
	}
	var text string
	if err := s.db.QueryRowContext(ctx, `SELECT watch_since FROM ct_names WHERE name = ?`, name).Scan(&text); err != nil {
		return time.Time{}, false, storeErr(err)
	}
	since, err = parseTime(text)
	return since, n == 1, err
}

// Keys returns the gateway's server keys as they were recorded.
func (s *Store) Keys(ctx context.Context) (Keys, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT public_key, replaced FROM ct_keys ORDER BY id`)
	if err != nil {
		return Keys{}, storeErr(err)
	}
	defer rows.Close()
	var out Keys
	for rows.Next() {
		var key string
		var replaced sql.NullString
		if err := rows.Scan(&key, &replaced); err != nil {
			return Keys{}, storeErr(err)
		}
		d, err := ParseDigest(key)
		if err != nil {
			return Keys{}, storeErr(err)
		}
		if !replaced.Valid {
			out.Current, out.HasCurrent = d, true
			continue
		}
		at, err := parseTime(replaced.String)
		if err != nil {
			return Keys{}, err
		}
		out.Earlier = append(out.Earlier, EarlierKey{PublicKey: d, Replaced: at})
	}
	return out, storeErr(rows.Err())
}

// ObserveKey records key as the current server key at now: if another one was current,
// it is replaced at now. changed tells that the key is new.
func (s *Store) ObserveKey(ctx context.Context, key Digest, now time.Time) (changed bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, storeErr(err)
	}
	defer func() { _ = tx.Rollback() }()
	var current string
	switch err := tx.QueryRowContext(ctx, `SELECT public_key FROM ct_keys WHERE replaced IS NULL`).Scan(&current); {
	case err == nil && current == key.String():
		return false, nil
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return false, storeErr(err)
	}
	at := formatTime(now)
	if _, err := tx.ExecContext(ctx, `UPDATE ct_keys SET replaced = ? WHERE replaced IS NULL`, at); err != nil {
		return false, storeErr(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO ct_keys (public_key, since) VALUES (?, ?)`, key.String(), at); err != nil {
		return false, storeErr(err)
	}
	return true, storeErr(tx.Commit())
}

// Stored is a certificate as the store keeps it.
type Stored struct {
	Parsed
	FirstSeen  time.Time
	NotifiedAt time.Time // zero while the household was not told
}

// PutCert keeps a certificate found at now; a certificate known already keeps what it
// had and takes the earlier time of logging. isNew tells that it was not known.
func (s *Store) PutCert(ctx context.Context, p Parsed, now time.Time) (isNew bool, err error) {
	names, err := json.Marshal(p.Names)
	if err != nil {
		return false, storeErr(err)
	}
	var logged any
	if !p.Logged.IsZero() {
		logged = formatTime(p.Logged)
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO ct_certs
		(digest, names, issuer, serial, not_before, not_after, public_key, logged, first_seen) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (digest) DO NOTHING`,
		p.Digest.String(), string(names), truncate(p.Issuer, maxIssuerText), truncate(p.Serial, maxSerialText),
		formatTime(p.NotBefore), formatTime(p.NotAfter), p.PublicKey.String(), logged, formatTime(now))
	if err != nil {
		return false, storeErr(err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, storeErr(err)
	}
	if n == 1 || logged == nil {
		return n == 1, nil
	}
	_, err = s.db.ExecContext(ctx, `UPDATE ct_certs SET logged = ? WHERE digest = ? AND (logged IS NULL OR logged > ?)`,
		logged, p.Digest.String(), logged)
	return false, storeErr(err)
}

// Certs returns the certificates kept, oldest first.
func (s *Store) Certs(ctx context.Context) ([]Stored, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT digest, names, issuer, serial, not_before, not_after, public_key, logged,
		first_seen, notified_at FROM ct_certs ORDER BY first_seen, digest LIMIT ?`, maxStoredCerts)
	if err != nil {
		return nil, storeErr(err)
	}
	defer rows.Close()
	var out []Stored
	for rows.Next() {
		c, err := scanCert(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, storeErr(rows.Err())
}

func scanCert(rows *sql.Rows) (Stored, error) {
	var digest, names, notBefore, notAfter, key, firstSeen string
	var logged, notified sql.NullString
	var c Stored
	if err := rows.Scan(&digest, &names, &c.Issuer, &c.Serial, &notBefore, &notAfter, &key, &logged, &firstSeen, &notified); err != nil {
		return Stored{}, storeErr(err)
	}
	var err error
	if c.Digest, err = ParseDigest(digest); err != nil {
		return Stored{}, storeErr(err)
	}
	if c.PublicKey, err = ParseDigest(key); err != nil {
		return Stored{}, storeErr(err)
	}
	if err := json.Unmarshal([]byte(names), &c.Names); err != nil {
		return Stored{}, storeErr(err)
	}
	for _, f := range []struct {
		dst *time.Time
		src string
	}{{&c.NotBefore, notBefore}, {&c.NotAfter, notAfter}, {&c.FirstSeen, firstSeen}} {
		if *f.dst, err = parseTime(f.src); err != nil {
			return Stored{}, err
		}
	}
	if c.Logged, err = parseNullTime(logged); err != nil {
		return Stored{}, err
	}
	if c.NotifiedAt, err = parseNullTime(notified); err != nil {
		return Stored{}, err
	}
	return c, nil
}

// MarkNotified records that the household was told of the certificates at now.
func (s *Store) MarkNotified(ctx context.Context, digests []Digest, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return storeErr(err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, d := range digests {
		if _, err := tx.ExecContext(ctx, `UPDATE ct_certs SET notified_at = ? WHERE digest = ? AND notified_at IS NULL`,
			formatTime(now), d.String()); err != nil {
			return storeErr(err)
		}
	}
	return storeErr(tx.Commit())
}

// Ack is an acknowledgement.
type Ack struct {
	Digest Digest
	By     string
	Via    Via
	At     time.Time
}

// Acknowledge records an acknowledgement; one that exists already stays as it was.
func (s *Store) Acknowledge(ctx context.Context, a Ack) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO ct_acks (digest, by, via, at) VALUES (?, ?, ?, ?) ON CONFLICT (digest) DO NOTHING`,
		a.Digest.String(), a.By, string(a.Via), formatTime(a.At))
	return storeErr(err)
}

// Acks returns every acknowledgement.
func (s *Store) Acks(ctx context.Context) ([]Ack, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT digest, by, via, at FROM ct_acks ORDER BY at, digest`)
	if err != nil {
		return nil, storeErr(err)
	}
	defer rows.Close()
	var out []Ack
	for rows.Next() {
		var digest, by, via, at string
		if err := rows.Scan(&digest, &by, &via, &at); err != nil {
			return nil, storeErr(err)
		}
		d, err := ParseDigest(digest)
		if err != nil {
			return nil, storeErr(err)
		}
		t, err := parseTime(at)
		if err != nil {
			return nil, err
		}
		out = append(out, Ack{Digest: d, By: by, Via: Via(via), At: t})
	}
	return out, storeErr(rows.Err())
}

// SourceState is the state of one source for one name.
type SourceState struct {
	Cursor      string
	LastSuccess time.Time
	LastFailure time.Time
	Failure     string
}

// Source returns the state of source for name; zero when it was never asked.
func (s *Store) Source(ctx context.Context, source, name string) (SourceState, error) {
	var st SourceState
	var success, failure sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT cursor, last_success, last_failure, failure FROM ct_sources WHERE source = ? AND name = ?`,
		source, name).Scan(&st.Cursor, &success, &failure, &st.Failure)
	if errors.Is(err, sql.ErrNoRows) {
		return SourceState{}, nil
	}
	if err != nil {
		return SourceState{}, storeErr(err)
	}
	if st.LastSuccess, err = parseNullTime(success); err != nil {
		return SourceState{}, err
	}
	if st.LastFailure, err = parseNullTime(failure); err != nil {
		return SourceState{}, err
	}
	return st, nil
}

// Succeeded records a successful query of source for name at now and the cursor of the
// next one.
func (s *Store) Succeeded(ctx context.Context, source, name, cursor string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO ct_sources (source, name, cursor, last_success) VALUES (?, ?, ?, ?)
		ON CONFLICT (source, name) DO UPDATE SET cursor = excluded.cursor, last_success = excluded.last_success, failure = ''`,
		source, name, cursor, formatTime(now))
	return storeErr(err)
}

// Failed records a failed query of source for name at now; the cursor stays.
func (s *Store) Failed(ctx context.Context, source, name string, failure error, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO ct_sources (source, name, last_failure, failure) VALUES (?, ?, ?, ?)
		ON CONFLICT (source, name) DO UPDATE SET last_failure = excluded.last_failure, failure = excluded.failure`,
		source, name, formatTime(now), truncate(failure.Error(), maxFailureText))
	return storeErr(err)
}

// truncate cuts s to at most n bytes at a rune boundary.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n]
}
