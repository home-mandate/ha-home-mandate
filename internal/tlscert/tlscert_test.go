// SPDX-License-Identifier: AGPL-3.0-or-later

package tlscert

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io/fs"
	"log/slog"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var start = time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

type pair struct {
	cert, key []byte
	serial    int64
}

// newPair returns a self-signed certificate for names (DNS names or IP addresses).
func newPair(t *testing.T, serial int64, notAfter time.Time, names ...string) pair {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "tlscert test"},
		NotBefore: start.Add(-time.Hour), NotAfter: notAfter}
	for _, n := range names {
		if ip := net.ParseIP(n); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, n)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pair{cert: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		key: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), serial: serial}
}

type files struct {
	t          *testing.T
	cert, key  string
	generation int
}

func newFiles(t *testing.T) *files {
	dir := t.TempDir()
	return &files{t: t, cert: filepath.Join(dir, "fullchain.pem"), key: filepath.Join(dir, "privkey.pem")}
}

// write stores cert and key with a modification time of its own, as a renewal would.
func (f *files) write(cert, key []byte) {
	f.t.Helper()
	f.generation++
	mtime := start.Add(time.Duration(f.generation) * time.Second)
	for path, data := range map[string][]byte{f.cert: cert, f.key: key} {
		if data == nil {
			continue
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			f.t.Fatal(err)
		}
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			f.t.Fatal(err)
		}
	}
}

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *logBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

func newLoader(t *testing.T, f *files, host string) (*Loader, *clock, *logBuffer) {
	t.Helper()
	c := &clock{now: start}
	logs := &logBuffer{}
	l, err := New(Config{CertFile: f.cert, KeyFile: f.key, Host: host, Now: c.Now,
		Logger: slog.New(slog.NewTextHandler(logs, nil))})
	if err != nil {
		t.Fatal(err)
	}
	return l, c, logs
}

func serving(t *testing.T, l *Loader) int64 {
	t.Helper()
	cert, err := l.GetCertificate(&tls.ClientHelloInfo{})
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	return leaf.SerialNumber.Int64()
}

func TestNewLoadsThePair(t *testing.T) {
	f := newFiles(t)
	p := newPair(t, 1, start.Add(90*24*time.Hour), "hm.example.org")
	f.write(p.cert, p.key)
	l, _, _ := newLoader(t, f, "hm.example.org")
	if got := serving(t, l); got != 1 {
		t.Errorf("serial = %d, want 1", got)
	}
	until, err := l.Status()
	if !until.Equal(start.Add(90*24*time.Hour)) || err != nil {
		t.Errorf("status = %v, %v", until, err)
	}
}

func TestNewChecksTheHost(t *testing.T) {
	for _, tc := range []struct {
		name  string
		names []string
		host  string
		ok    bool
	}{
		{"exact name", []string{"hm.example.org"}, "hm.example.org", true},
		{"wildcard", []string{"*.example.org"}, "hm.example.org", true},
		{"wildcard does not cover two labels", []string{"*.example.org"}, "a.hm.example.org", false},
		{"wildcard does not cover the apex", []string{"*.example.org"}, "example.org", false},
		{"other name", []string{"other.example.org"}, "hm.example.org", false},
		{"case-insensitive", []string{"HM.Example.org"}, "hm.example.org", true},
		{"IP address", []string{"192.0.2.10"}, "192.0.2.10", true},
		{"IP address not in the certificate", []string{"192.0.2.10"}, "192.0.2.11", false},
		{"no public URL: no check", []string{"other.example.org"}, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFiles(t)
			p := newPair(t, 1, start.Add(90*24*time.Hour), tc.names...)
			f.write(p.cert, p.key)
			_, err := New(Config{CertFile: f.cert, KeyFile: f.key, Host: tc.host, Now: func() time.Time { return start }})
			if (err == nil) != tc.ok {
				t.Errorf("err = %v, want ok=%v", err, tc.ok)
			}
			if err != nil && !errors.Is(err, ErrHostNotCovered) {
				t.Errorf("err = %v, want ErrHostNotCovered", err)
			}
		})
	}
}

func TestNewRefusesMissingAndBrokenFiles(t *testing.T) {
	f := newFiles(t)
	if _, err := New(Config{CertFile: f.cert, KeyFile: f.key}); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing files: err = %v, want fs.ErrNotExist", err)
	}
	a, b := newPair(t, 1, start.Add(time.Hour), "hm.example.org"), newPair(t, 2, start.Add(time.Hour), "hm.example.org")
	for name, w := range map[string][2][]byte{
		"broken certificate":         {[]byte("not a certificate"), a.key},
		"key of another certificate": {a.cert, b.key},
		"empty key":                  {a.cert, []byte{}},
	} {
		f.write(w[0], w[1])
		if _, err := New(Config{CertFile: f.cert, KeyFile: f.key}); err == nil {
			t.Errorf("%s: loaded", name)
		}
	}
}

func TestRenewalIsTakenOverWithoutARestart(t *testing.T) {
	f := newFiles(t)
	first := newPair(t, 1, start.Add(30*24*time.Hour), "hm.example.org")
	f.write(first.cert, first.key)
	l, c, _ := newLoader(t, f, "hm.example.org")

	second := newPair(t, 2, start.Add(90*24*time.Hour), "hm.example.org")
	f.write(second.cert, second.key)
	if got := serving(t, l); got != 1 {
		t.Errorf("before a minute passed: serial %d, want the files looked at only once a minute", got)
	}
	c.advance(CheckInterval)
	if got := serving(t, l); got != 2 {
		t.Errorf("after a minute: serial %d, want 2", got)
	}
	if until, err := l.Status(); !until.Equal(start.Add(90*24*time.Hour)) || err != nil {
		t.Errorf("status = %v, %v", until, err)
	}
}

func TestABadRenewalKeepsThePreviousPair(t *testing.T) {
	good := newPair(t, 1, start.Add(30*24*time.Hour), "hm.example.org")
	other := newPair(t, 2, start.Add(90*24*time.Hour), "hm.example.org")
	foreign := newPair(t, 3, start.Add(90*24*time.Hour), "other.example.org")
	for _, tc := range []struct {
		name      string
		cert, key []byte
	}{
		{"certificate renewed, key not yet", other.cert, nil},
		{"key renewed, certificate not yet", nil, other.key},
		{"half-written certificate", other.cert[:len(other.cert)/2], other.key},
		{"not a certificate", []byte("garbage"), other.key},
		{"does not cover the public host", foreign.cert, foreign.key},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFiles(t)
			f.write(good.cert, good.key)
			l, c, logs := newLoader(t, f, "hm.example.org")
			f.write(tc.cert, tc.key)
			c.advance(CheckInterval)
			if got := serving(t, l); got != 1 {
				t.Errorf("serial %d, want the previous pair", got)
			}
			if _, err := l.Status(); err == nil {
				t.Error("status reports no error")
			}
			if !strings.Contains(logs.String(), "level=ERROR") {
				t.Errorf("not logged as an error: %s", logs.String())
			}
			// Repaired by the next renewal step: taken over at the next look, error gone.
			f.write(other.cert, other.key)
			c.advance(CheckInterval)
			if got := serving(t, l); got != 2 {
				t.Errorf("after the repair: serial %d, want 2", got)
			}
			if _, err := l.Status(); err != nil {
				t.Errorf("status error after the repair: %v", err)
			}
		})
	}
}

func TestVanishedFilesKeepThePreviousPair(t *testing.T) {
	f := newFiles(t)
	p := newPair(t, 1, start.Add(30*24*time.Hour), "hm.example.org")
	f.write(p.cert, p.key)
	l, c, _ := newLoader(t, f, "hm.example.org")
	if err := os.Remove(f.cert); err != nil {
		t.Fatal(err)
	}
	c.advance(CheckInterval)
	if got := serving(t, l); got != 1 {
		t.Errorf("serial %d, want the previous pair", got)
	}
	if _, err := l.Status(); err == nil {
		t.Error("status reports no error for a vanished certificate")
	}
}

func TestExpiryIsWarnedAboutOnceADay(t *testing.T) {
	f := newFiles(t)
	p := newPair(t, 1, start.Add(15*24*time.Hour), "hm.example.org")
	f.write(p.cert, p.key)
	l, c, logs := newLoader(t, f, "hm.example.org")
	_ = serving(t, l)
	if strings.Contains(logs.String(), "expires") {
		t.Fatalf("warned 15 days before the end: %s", logs.String())
	}
	c.advance(24*time.Hour + CheckInterval) // 13 days 23 h left
	_ = serving(t, l)
	c.advance(CheckInterval)
	_ = serving(t, l)
	if n := strings.Count(logs.String(), "expires"); n != 1 {
		t.Errorf("%d warnings within a day, want 1: %s", n, logs.String())
	}
	c.advance(24 * time.Hour)
	_ = serving(t, l)
	if n := strings.Count(logs.String(), "expires"); n != 2 {
		t.Errorf("%d warnings after another day, want 2", n)
	}
	if !Expiring(start.Add(15*24*time.Hour), c.Now()) || Expiring(start.Add(15*24*time.Hour), start) {
		t.Error("Expiring does not match the 14-day rule")
	}
}

func TestConcurrentHandshakesDuringARenewal(t *testing.T) {
	f := newFiles(t)
	p := newPair(t, 1, start.Add(30*24*time.Hour), "hm.example.org")
	f.write(p.cert, p.key)
	l, c, _ := newLoader(t, f, "hm.example.org")
	q := newPair(t, 2, start.Add(90*24*time.Hour), "hm.example.org")
	f.write(q.cert, q.key)
	c.advance(CheckInterval)
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if _, err := l.GetCertificate(&tls.ClientHelloInfo{}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if got := serving(t, l); got != 2 {
		t.Errorf("serial %d, want 2", got)
	}
}
