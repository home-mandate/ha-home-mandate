// SPDX-License-Identifier: AGPL-3.0-or-later

package ctwatch

import (
	"cmp"
	"context"
	"crypto/sha256"
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
	// Unread counts entries whose certificate could not be read or did not fit what the
	// source said of it. They are kept from the source's listing, with no key (never the
	// gateway's own), so that they raise an alarm instead of vanishing (fail closed); the
	// cursor moves past them, so that one broken entry cannot stop the monitoring.
	Unread int
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

// errTooLarge is an answer beyond its limit; it is an ErrSource.
var errTooLarge = fmt.Errorf("%w: too large", ErrSource)

// statusError is an answer with a status other than 200; it is an ErrSource.
type statusError struct {
	source string
	code   int
}

func (e *statusError) Error() string { return fmt.Sprintf("ctwatch: %s answered %d", e.source, e.code) }
func (e *statusError) Unwrap() error { return ErrSource }

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
	// certSpotterPage is the size of a full page of Cert Spotter (measured 2026-10-19); a
	// shorter page is the last one.
	certSpotterPage = 100
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
		for _, is := range page {
			if !certSpotterID.MatchString(is.ID) {
				return Batch{}, fmt.Errorf("%w: Cert Spotter issuance ID", ErrSource)
			}
			p, read := is.parsed(name)
			if !read {
				out.Unread++
			}
			out.Certs = append(out.Certs, p)
			out.Cursor = is.ID
		}
		if len(page) < certSpotterPage {
			break // the last page: no query for an empty one, every query counts against the limit
		}
	}
	return out, nil
}

// farFuture stands for an end of validity no source told: the certificate counts as
// valid, so that it raises an alarm.
var farFuture = time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)

// parsed reads the certificate itself when it can, and read tells that it could and that
// its digests are the ones Cert Spotter computed. Without a readable one, the issuance is kept from
// Cert Spotter's fields: with its key only when there was no certificate to contradict
// it, never with a time of logging (which counts as late), and found by name for the
// query it answered.
func (is issuance) parsed(query string) (p Parsed, read bool) {
	tbs, tbsOK := hexDigestOf(is.TBS)
	key, keyOK := hexDigestOf(is.PublicKey)
	der, derErr := base64.StdEncoding.Strict().DecodeString(is.DER)
	if derErr == nil && len(der) > 0 {
		if c, err := ParseCert(der); err == nil {
			// The certificate itself decides; a difference from Cert Spotter's digests is
			// reported (read false), never a reason to drop it.
			return c, tbsOK && keyOK && c.Digest == tbs && c.PublicKey == key
		}
		keyOK = false // a certificate that cannot be read: its key is not taken on trust
	}
	p = Parsed{Issuer: is.Issuer.Name, Cert: Cert{Names: listedNames(is.Names, query), NotAfter: farFuture}}
	if tbsOK {
		p.Digest = tbs
	} else {
		p.Digest = sha256.Sum256([]byte(SourceCertSpotter + ":" + is.ID))
	}
	if keyOK {
		p.PublicKey = key
	}
	if t, err := time.Parse(time.RFC3339, is.NotBefore); err == nil {
		p.NotBefore = t.UTC()
	}
	if t, err := time.Parse(time.RFC3339, is.NotAfter); err == nil {
		p.NotAfter = t.UTC()
	}
	return p, false
}

func hexDigestOf(s string) (Digest, bool) {
	var d Digest
	if !hexDigest.MatchString(s) {
		return d, false
	}
	_, _ = hex.Decode(d[:], []byte(s))
	return d, true
}

// listedNames are the names a source listed, lower case and bounded; none means the name
// queried, which the source answered with this entry.
func listedNames(names []string, query string) []string {
	var out []string
	for _, n := range names {
		if n = strings.ToLower(strings.TrimSpace(n)); n != "" && len(n) <= 253 && len(out) < maxNames {
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		return []string{strings.ToLower(query)}
	}
	return out
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
	ID        int64  `json:"id"`
	NameValue string `json:"name_value"`
	Issuer    string `json:"issuer_name"`
	NotBefore string `json:"not_before"`
	NotAfter  string `json:"not_after"`
}

// crtshTime is the form of crt.sh's times, UTC without a zone.
const crtshTime = "2006-01-02T15:04:05"

// listed keeps an entry whose certificate could not be read from crt.sh's listing, with
// no key; its identity is crt.sh's ID, as no digest of it is known.
func (e crtshEntry) listed(query string) Parsed {
	p := Parsed{Issuer: e.Issuer, Cert: Cert{Digest: sha256.Sum256([]byte(SourceCrtSh + ":" + strconv.FormatInt(e.ID, 10))),
		Names: listedNames(strings.Split(e.NameValue, "\n"), query), NotAfter: farFuture}}
	if t, err := time.Parse(crtshTime, e.NotBefore); err == nil {
		p.NotBefore = t.UTC()
	}
	if t, err := time.Parse(crtshTime, e.NotAfter); err == nil {
		p.NotAfter = t.UTC()
	}
	return p
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
	entries, err := c.list(ctx, name, after)
	if err != nil {
		return Batch{}, err
	}
	out := Batch{Cursor: cursor}
	for i, e := range entries {
		if i == maxDownloads {
			break
		}
		p, err := c.download(ctx, e.ID)
		switch {
		case errors.Is(err, ErrCert):
			// Unreadable, too large or gone at crt.sh: kept from the listing.
			out.Unread++
			p = e.listed(name)
		case err != nil && len(out.Certs) == 0:
			return Batch{}, err
		case err != nil:
			return out, nil // what was read counts; the next round continues after it
		}
		out.Certs = append(out.Certs, p)
		out.Cursor = strconv.FormatInt(e.ID, 10)
	}
	return out, nil
}

// list returns the entries after after of both queries, ascending and each once.
func (c *CrtSh) list(ctx context.Context, name string, after int64) ([]crtshEntry, error) {
	queries := []string{name}
	if _, parent, ok := strings.Cut(name, "."); ok && strings.Contains(parent, ".") {
		queries = append(queries, "*."+parent)
	}
	var out []crtshEntry
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
				out = append(out, e)
			}
		}
	}
	slices.SortFunc(out, func(a, b crtshEntry) int { return cmp.Compare(a.ID, b.ID) })
	return slices.CompactFunc(out, func(a, b crtshEntry) bool { return a.ID == b.ID }), nil
}

func (c *CrtSh) download(ctx context.Context, id int64) (Parsed, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/?d="+strconv.FormatInt(id, 10), nil)
	if err != nil {
		return Parsed{}, err
	}
	body, err := get(c.client, req, SourceCrtSh, 2*MaxCertBytes)
	var status *statusError
	switch {
	case errors.As(err, &status) && status.code == http.StatusNotFound, errors.Is(err, errTooLarge):
		return Parsed{}, fmt.Errorf("%w: crt.sh certificate %d: %w", ErrCert, id, err)
	case err != nil:
		return Parsed{}, err
	}
	block, _ := pem.Decode(body)
	if block == nil || block.Type != "CERTIFICATE" {
		// An error page or a cut answer: asked again next round, not taken as unreadable.
		return Parsed{}, fmt.Errorf("%w: crt.sh certificate %d is not PEM", ErrSource, id)
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
		return nil, &statusError{source: source, code: resp.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("ctwatch: %s: %w", source, err)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("%w: %s answered more than %d bytes", errTooLarge, source, limit)
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
