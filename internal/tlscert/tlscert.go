// SPDX-License-Identifier: AGPL-3.0-or-later

// Package tlscert serves the certificate of the MCP listener and takes renewed files over
// without a restart (ARCHITECTURE section 11, decision 1). A renewal is used only if
// certificate and key belong together and the certificate covers the public host;
// otherwise the previous pair stays and the error is logged and reported.
package tlscert

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
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

// fileStamp tells whether a file changed since it was read.
type fileStamp struct {
	mod  time.Time
	size int64
}

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
	l.mu.Lock()
	defer l.mu.Unlock()
	l.refresh()
	return l.cert, nil
}

// Status is the end of validity of the pair in use and the error of the last attempt to
// take over renewed files, nil if it succeeded or there was none.
func (l *Loader) Status() (time.Time, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.notAfter, l.lastErr
}

// Expiring tells whether a certificate valid until notAfter is to be warned about at now.
func Expiring(notAfter, now time.Time) bool {
	return notAfter.Sub(now) < ExpiryWarning
}

// refresh looks at the files at most once per CheckInterval; called with mu held.
func (l *Loader) refresh() {
	now := l.cfg.Now()
	if now.Sub(l.checked) < CheckInterval && !now.Before(l.checked) {
		return
	}
	l.checked = now
	l.warnExpiry(now)
	stamps, err := l.stamps()
	if err != nil {
		l.fail(err)
		return
	}
	if stamps == l.seen {
		return
	}
	cert, notAfter, err := l.load()
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
		info, err := os.Stat(path)
		if err != nil {
			return out, fmt.Errorf("tlscert: %w", err)
		}
		out[i] = fileStamp{mod: info.ModTime(), size: info.Size()}
	}
	return out, nil
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
	}
	if l.cfg.Host != "" {
		if err := leaf.VerifyHostname(l.cfg.Host); err != nil {
			return nil, time.Time{}, fmt.Errorf("%w (%s)", ErrHostNotCovered, l.cfg.Host)
		}
	}
	return &cert, leaf.NotAfter, nil
}
