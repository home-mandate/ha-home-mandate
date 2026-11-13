// SPDX-License-Identifier: AGPL-3.0-or-later

package ctwatch

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// A Source finds the certificates for a name in Certificate Transparency. Fetch starts
// after cursor ("" for the beginning) and returns what it found with the cursor of the
// next query; a failed query returns an error and nothing, so that the cursor stays and
// the next round asks again.
type Source interface {
	Name() string
	Fetch(ctx context.Context, name, cursor string) (Batch, error)
}

// Batch is what one query found.
type Batch struct {
	Certs  []Parsed
	Cursor string
	// Skipped counts entries the source gave that could not be read; they are logged and
	// the cursor moves past them, so that one broken entry cannot stop the monitoring.
	Skipped int
}

// RateLimitedError means the source refused the query for its rate limit; the next
// query waits RetryAfter (zero when it gave none).
type RateLimitedError struct {
	Source     string
	RetryAfter time.Duration
}

func (e *RateLimitedError) Error() string {
	return fmt.Sprintf("ctwatch: %s rate limit reached", e.Source)
}

// ErrSource means a source answered with something this package does not accept.
var ErrSource = errors.New("ctwatch: unusable answer")

const (
	// SourceCertSpotter and SourceCrtSh name the sources in the store.
	SourceCertSpotter = "certspotter"
	SourceCrtSh       = "crtsh"

	userAgent = "Home-Mandate CT monitoring (+https://home-mandate.org)"
	// maxListBytes bounds a list answer; a page of Cert Spotter holds 100 issuances.
	maxListBytes = 8 << 20
	// maxPages bounds the pages of Cert Spotter read per round: every page counts
	// against the hourly limit, and the next round continues at the cursor.
	maxPages = 4
	// maxDownloads bounds the certificates downloaded from crt.sh per round.
	maxDownloads = 20
	// maxRetryAfter caps what a source may ask the gateway to wait.
	maxRetryAfter = 6 * time.Hour
)

var (
	certSpotterID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	hexDigest     = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// CertSpotter queries the Cert Spotter API of SSLMate: one query per name with
// match_wildcards, so that a wildcard covering the name is found too. Without a key it
// is limited per IP address (100 queries an hour as of 2026); SSLMate agreed to this use.
type CertSpotter struct {
	client  *http.Client
	key     string
	baseURL string
}

// NewCertSpotter returns the source; key may be empty.
func NewCertSpotter(client *http.Client, key string) *CertSpotter {
	return &CertSpotter{client: client, key: key, baseURL: "https://api.certspotter.com"}
}

// Name is the source's name in the store.
func (*CertSpotter) Name() string { return SourceCertSpotter }

type issuance struct {
	ID        string   `json:"id"`
	TBS       string   `json:"tbs_sha256"`
	PublicKey string   `json:"pubkey_sha256"`
	Names     []string `json:"dns_names"`
	NotBefore string   `json:"not_before"`
	NotAfter  string   `json:"not_after"`
	DER       string   `json:"cert_der"`
	Issuer    struct {
		Name string `json:"name"`
	} `json:"issuer"`
}

// Fetch reads up to maxPages pages after cursor.
func (c *CertSpotter) Fetch(ctx context.Context, name, cursor string) (Batch, error) {
	out := Batch{Cursor: cursor}
	for range maxPages {
		q := url.Values{"domain": {name}, "match_wildcards": {"true"}, "expand": {"dns_names", "cert_der", "issuer"}}
		if out.Cursor != "" {
			q.Set("after", out.Cursor)
		}
		var page []issuance
		if err := c.get(ctx, c.baseURL+"/v1/issuances?"+q.Encode(), &page); err != nil {
			return Batch{}, err
		}
		if len(page) == 0 {
			break
		}
		for _, is := range page {
			if !certSpotterID.MatchString(is.ID) {
				return Batch{}, fmt.Errorf("%w: Cert Spotter issuance ID", ErrSource)
			}
			if p, err := is.parsed(); err != nil {
				out.Skipped++
			} else {
				out.Certs = append(out.Certs, p)
			}
			out.Cursor = is.ID
		}
	}
	return out, nil
}

// parsed reads the certificate itself when it can; its digests must then be the ones
// Cert Spotter computed. Without a readable certificate, Cert Spotter's fields are used,
// with no time of logging (which counts as late, never as early).
func (is issuance) parsed() (Parsed, error) {
	if !hexDigest.MatchString(is.TBS) || !hexDigest.MatchString(is.PublicKey) {
		return Parsed{}, fmt.Errorf("%w: Cert Spotter digests", ErrSource)
	}
	var tbs, key Digest
	_, _ = hex.Decode(tbs[:], []byte(is.TBS))
	_, _ = hex.Decode(key[:], []byte(is.PublicKey))
	if der, err := base64.StdEncoding.Strict().DecodeString(is.DER); err == nil {
		if p, err := ParseCert(der); err == nil {
			if p.Digest != tbs || p.PublicKey != key {
				return Parsed{}, fmt.Errorf("%w: Cert Spotter digests differ from the certificate", ErrSource)
			}
			return p, nil
		}
	}
	notBefore, err1 := time.Parse(time.RFC3339, is.NotBefore)
	notAfter, err2 := time.Parse(time.RFC3339, is.NotAfter)
	if err1 != nil || err2 != nil || len(is.Names) == 0 || len(is.Names) > maxNames {
		return Parsed{}, fmt.Errorf("%w: Cert Spotter issuance", ErrSource)
	}
	names := make([]string, len(is.Names))
	for i, n := range is.Names {
		names[i] = strings.ToLower(n)
	}
	return Parsed{Cert: Cert{Digest: tbs, Names: names, NotBefore: notBefore.UTC(), NotAfter: notAfter.UTC(), PublicKey: key},
		Issuer: is.Issuer.Name}, nil
}

func (c *CertSpotter) get(ctx context.Context, target string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	if c.key != "" {
		req.Header.Set("Authorization", "Bearer "+c.key)
	}
	return getJSON(c.client, req, SourceCertSpotter, into)
}

// CrtSh queries crt.sh, a second source independent of Cert Spotter: the name itself and
// the wildcard of its parent, which crt.sh does not find by the name. The list gives no
// key, so each certificate after the cursor is downloaded and read, in the order of
// crt.sh's IDs, at most maxDownloads per round.
type CrtSh struct {
	client  *http.Client
	baseURL string
}

// NewCrtSh returns the source.
func NewCrtSh(client *http.Client) *CrtSh {
	return &CrtSh{client: client, baseURL: "https://crt.sh"}
}

// Name is the source's name in the store.
func (*CrtSh) Name() string { return SourceCrtSh }

type crtshEntry struct {
	ID int64 `json:"id"`
}

// Fetch lists both queries and downloads the certificates after cursor.
func (c *CrtSh) Fetch(ctx context.Context, name, cursor string) (Batch, error) {
	var after int64
	if cursor != "" {
		var err error
		if after, err = strconv.ParseInt(cursor, 10, 64); err != nil || after < 0 {
			return Batch{}, fmt.Errorf("%w: crt.sh cursor %q", ErrSource, cursor)
		}
	}
	ids, err := c.list(ctx, name, after)
	if err != nil {
		return Batch{}, err
	}
	out := Batch{Cursor: cursor}
	for i, id := range ids {
		if i == maxDownloads {
			break
		}
		p, err := c.download(ctx, id)
		switch {
		case errors.Is(err, ErrCert):
			out.Skipped++
		case err != nil && len(out.Certs) == 0 && out.Skipped == 0:
			return Batch{}, err
		case err != nil:
			return out, nil // what was read counts; the next round continues after it
		default:
			out.Certs = append(out.Certs, p)
		}
		out.Cursor = strconv.FormatInt(id, 10)
	}
	return out, nil
}

// list returns the IDs after after of both queries, ascending and each once.
func (c *CrtSh) list(ctx context.Context, name string, after int64) ([]int64, error) {
	queries := []string{name}
	if _, parent, ok := strings.Cut(name, "."); ok && strings.Contains(parent, ".") {
		queries = append(queries, "*."+parent)
	}
	var ids []int64
	for _, q := range queries {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet,
			c.baseURL+"/?"+url.Values{"q": {q}, "output": {"json"}, "exclude": {"expired"}}.Encode(), nil)
		if err != nil {
			return nil, err
		}
		var entries []crtshEntry
		if err := getJSON(c.client, req, SourceCrtSh, &entries); err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.ID <= 0 {
				return nil, fmt.Errorf("%w: crt.sh ID", ErrSource)
			}
			if e.ID > after {
				ids = append(ids, e.ID)
			}
		}
	}
	slices.Sort(ids)
	return slices.Compact(ids), nil
}

func (c *CrtSh) download(ctx context.Context, id int64) (Parsed, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/?d="+strconv.FormatInt(id, 10), nil)
	if err != nil {
		return Parsed{}, err
	}
	body, err := get(c.client, req, SourceCrtSh, 2*MaxCertBytes)
	if err != nil {
		return Parsed{}, err
	}
	block, _ := pem.Decode(body)
	if block == nil || block.Type != "CERTIFICATE" {
		return Parsed{}, fmt.Errorf("%w: crt.sh certificate %d is not PEM", ErrCert, id)
	}
	return ParseCert(block.Bytes)
}

func getJSON(client *http.Client, req *http.Request, source string, into any) error {
	body, err := get(client, req, source, maxListBytes)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("%w: %s: %w", ErrSource, source, err)
	}
	return nil
}

// get sends req and reads at most limit bytes of a 200 answer. 429, and 403 or 503 with
// Retry-After, are the source's rate limit.
func get(client *http.Client, req *http.Request, source string, limit int64) ([]byte, error) {
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ctwatch: %s: %w", source, err)
	}
	defer resp.Body.Close()
	if limited(resp) {
		return nil, &RateLimitedError{Source: source, RetryAfter: retryAfter(resp.Header.Get("Retry-After"))}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: %s answered %d", ErrSource, source, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("ctwatch: %s: %w", source, err)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("%w: %s answered more than %d bytes", ErrSource, source, limit)
	}
	return body, nil
}

func limited(resp *http.Response) bool {
	switch resp.StatusCode {
	case http.StatusTooManyRequests:
		return true
	case http.StatusForbidden, http.StatusServiceUnavailable:
		return resp.Header.Get("Retry-After") != ""
	}
	return false
}

// retryAfter reads Retry-After in seconds, capped at maxRetryAfter; anything else is zero.
func retryAfter(v string) time.Duration {
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0
	}
	return min(time.Duration(n)*time.Second, maxRetryAfter)
}
