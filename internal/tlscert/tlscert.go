// SPDX-License-Identifier: AGPL-3.0-or-later

// Package tlscert serves the certificate of the MCP listener and takes renewed files over
// without a restart (ARCHITECTURE section 11, decision 1). A renewal is used only if
// certificate and key belong together and the certificate covers the public host;
// otherwise the previous pair stays and the error is logged and reported.
package tlscert

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"
)

const (
	// CheckInterval is how often the files are looked at, at most.
	CheckInterval = time.Minute
	// ExpiryWarning is how long before the end of validity the log and the UI warn.
	ExpiryWarning = 14 * 24 * time.Hour
	// warnEvery bounds the expiry warning in the log.
	warnEvery = 24 * time.Hour
)

// ErrHostNotCovered means the certificate is not valid for the public host.
var ErrHostNotCovered = errors.New("tlscert: certificate does not cover the public host")

// Config configures a Loader.
type Config struct {
	CertFile, KeyFile string
	// Host is the host of the public URL the certificate must cover; empty means no check.
	Host   string
	Now    func() time.Time
	Logger *slog.Logger
}

// Loader holds the current pair. It is safe for concurrent use.
type Loader struct {
	cfg Config

	mu        sync.Mutex
	cert      *tls.Certificate
	notAfter  time.Time
	seen      [2]fileStamp // what the files looked like when last read
	checked   time.Time
	lastErr   error
	lastWarns time.Time
}

// fileStamp tells whether a file changed since it was read: by content, because a
// renewal may keep size and modification time (cp -p, coarse timestamps of a volume).
type fileStamp [sha256.Size]byte

// New loads the pair; it fails if the files cannot be read, do not belong together or the
// certificate does not cover cfg.Host. A missing file is reported as fs.ErrNotExist.
func New(cfg Config) (*Loader, error) {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	l := &Loader{cfg: cfg}
	stamps, err := l.stamps()
	if err != nil {
		return nil, err
	}
	cert, notAfter, err := l.load()
	if err != nil {
		return nil, err
	}
	l.cert, l.notAfter, l.seen, l.checked = cert, notAfter, stamps, cfg.Now()
	return l, nil
}

// GetCertificate is tls.Config.GetCertificate: the current pair, renewed when the files
// changed and the new pair is usable.
func (l *Loader) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	l.refresh()
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.cert, nil
}

// Status is the end of validity of the pair in use and the error of the last attempt to
// take over renewed files, nil if it succeeded or there was none. It looks at the files
// too (at most once per CheckInterval), so the UI is current also when no agent connects.
func (l *Loader) Status() (time.Time, error) {
	l.refresh()
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.notAfter, l.lastErr
}

// Expiring tells whether a certificate valid until notAfter is to be warned about at now.
func Expiring(notAfter, now time.Time) bool {
	return notAfter.Sub(now) < ExpiryWarning
}

// refresh looks at the files at most once per CheckInterval. The files are read outside
// the lock, so handshakes are not held up by the disk; only one caller looks at a time,
// because the time of the look is taken under the lock.
func (l *Loader) refresh() {
	l.mu.Lock()
	now := l.cfg.Now()
	if now.Sub(l.checked) < CheckInterval && !now.Before(l.checked) {
		l.mu.Unlock()
		return
	}
	l.checked = now
	l.warnExpiry(now)
	seen := l.seen
	l.mu.Unlock()

	stamps, err := l.stamps()
	if err == nil && stamps == seen {
		return
	}
	var cert *tls.Certificate
	var notAfter time.Time
	if err == nil {
		cert, notAfter, err = l.load()
	}
	if err == nil {
		err = validNow(cert, now)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err != nil {
		// Certificate and key are often written one after the other: retried at the next look.
		l.fail(err)
		return
	}
	l.cert, l.notAfter, l.seen, l.lastErr = cert, notAfter, stamps, nil
	l.cfg.Logger.Info("renewed TLS certificate taken over", "valid_until", notAfter.UTC().Format(time.RFC3339))
	l.warnExpiry(now)
}

func (l *Loader) fail(err error) {
	if l.lastErr == nil || l.lastErr.Error() != err.Error() {
		l.cfg.Logger.Error("renewed TLS certificate not taken over, the previous one stays", "error", err)
	}
	l.lastErr = err
}

func (l *Loader) warnExpiry(now time.Time) {
	if !Expiring(l.notAfter, now) || (!l.lastWarns.IsZero() && now.Sub(l.lastWarns) < warnEvery && !now.Before(l.lastWarns)) {
		return
	}
	l.lastWarns = now
	l.cfg.Logger.Warn("TLS certificate expires soon", "valid_until", l.notAfter.UTC().Format(time.RFC3339))
}

func (l *Loader) stamps() ([2]fileStamp, error) {
	var out [2]fileStamp
	for i, path := range []string{l.cfg.CertFile, l.cfg.KeyFile} {
		data, err := readLimited(path)
		if err != nil {
			return out, fmt.Errorf("tlscert: %w", err)
		}
		out[i] = sha256.Sum256(data)
	}
	return out, nil
}

// maxFileBytes bounds what is read of a certificate or key file once a minute.
const maxFileBytes = 1 << 20

// validNow refuses a renewal that is not valid yet or any more: the previous pair is the
// better one to keep.
func validNow(cert *tls.Certificate, now time.Time) error {
	if now.Before(cert.Leaf.NotBefore) || now.After(cert.Leaf.NotAfter) {
		return fmt.Errorf("tlscert: renewed certificate is not valid now (valid %s to %s)",
			cert.Leaf.NotBefore.UTC().Format(time.RFC3339), cert.Leaf.NotAfter.UTC().Format(time.RFC3339))
	}
	return nil
}

func readLimited(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxFileBytes {
		return nil, fmt.Errorf("%s is larger than %d bytes", path, maxFileBytes)
	}
	return data, nil
}

func (l *Loader) load() (*tls.Certificate, time.Time, error) {
	cert, err := tls.LoadX509KeyPair(l.cfg.CertFile, l.cfg.KeyFile)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("tlscert: load certificate and key: %w", err)
	}
	leaf := cert.Leaf
	if leaf == nil {
		if leaf, err = x509.ParseCertificate(cert.Certificate[0]); err != nil {
			return nil, time.Time{}, fmt.Errorf("tlscert: parse certificate: %w", err)
		}
		cert.Leaf = leaf
	}
	if l.cfg.Host != "" {
		if err := leaf.VerifyHostname(l.cfg.Host); err != nil {
			return nil, time.Time{}, fmt.Errorf("%w: %w", ErrHostNotCovered, err)
		}
	}
	return &cert, leaf.NotAfter, nil
}
