// SPDX-License-Identifier: AGPL-3.0-or-later

package oauth

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"
)

// ErrInvalidClient means a client ID is neither a usable Client ID Metadata Document
// URL nor a free identifier, or its document cannot be fetched or is invalid.
var ErrInvalidClient = errors.New("oauth: invalid client")

const (
	cimdTimeout     = 5 * time.Second
	cimdMaxBytes    = 5 << 10
	cimdCacheTTL    = time.Hour
	cimdCacheSize   = 100
	cimdFailTTL     = time.Minute // a failed fetch is not repeated before
	cimdFailSize    = 100
	cimdParallel    = 4 // concurrent fetches; more are refused, not queued
	maxClientIDLen  = 255
	maxClientName   = 80
	maxRedirectURIs = 10
)

// errFetchBusy refuses a fetch while cimdParallel others are in progress.
var errFetchBusy = fmt.Errorf("%w: too many metadata fetches in progress", ErrInvalidClient)

// freeIdentifier is a client ID without metadata, accepted only for pairing codes and
// shown to the human as unverified (decision W7).
var freeIdentifier = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// Client is an OAuth client as the human sees it before admitting it.
type Client struct {
	ID           string
	Name         string // claimed by the client, never trusted as a fact
	RedirectURIs []string
	Verified     bool // the metadata document was fetched from ID
}

// ClientResolver turns a client ID into a Client.
type ClientResolver interface {
	Resolve(ctx context.Context, clientID string) (Client, error)
}

// CIMDResolver fetches Client ID Metadata Documents. Only https on port 443 to public
// addresses is used: the address is checked when the connection is made, after DNS
// resolution, so a name cannot be pointed at the local network (SSRF, decision W6).
type CIMDResolver struct {
	client *http.Client
	now    func() time.Time

	// allowAddr and anyPort relax the network rules for tests only.
	allowAddr func(netip.Addr) bool
	anyPort   bool

	fetching chan struct{} // semaphore of cimdParallel

	mu     sync.Mutex
	cache  map[string]cachedClient
	failed map[string]time.Time // client ID → time of the failed fetch
}

type cachedClient struct {
	client  Client
	fetched time.Time
}

// NewCIMDResolver returns a resolver; roots nil means the system roots.
func NewCIMDResolver(roots *x509.CertPool) *CIMDResolver {
	r := &CIMDResolver{now: time.Now, allowAddr: publicAddr, cache: map[string]cachedClient{},
		failed: map[string]time.Time{}, fetching: make(chan struct{}, cimdParallel)}
	dialer := &net.Dialer{Timeout: cimdTimeout, Control: r.control}
	r.client = &http.Client{
		Timeout: cimdTimeout,
		Transport: &http.Transport{
			Proxy:                  nil,
			DialContext:            dialer.DialContext,
			TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots},
			TLSHandshakeTimeout:    cimdTimeout,
			ResponseHeaderTimeout:  cimdTimeout,
			DisableKeepAlives:      true,
			MaxResponseHeaderBytes: 16 << 10,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return r
}

// control runs for every connection with the resolved address.
func (r *CIMDResolver) control(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || !r.allowAddr(ip.Unmap()) {
		return fmt.Errorf("%w: address not public", ErrInvalidClient)
	}
	return nil
}

// nonPublic lists special-purpose ranges beyond what netip classifies. 192.88.99.0/24,
// the deprecated 6to4 relay anycast, stays allowed: the E2E suite uses it as a public
// stand-in.
var nonPublic = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::/96"), // IPv4-compatible, deprecated
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/32"), // Teredo
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("fec0::/10"), // site-local, deprecated
}

func publicAddr(ip netip.Addr) bool {
	if !ip.IsValid() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	for _, p := range nonPublic {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

// Resolve returns the client for a Client ID Metadata Document URL (fetched, cached for
// an hour) or for a free identifier (unverified, no redirect URIs). A failed fetch is
// remembered for cimdFailTTL, so that a slow or broken document cannot tie up the fetch
// slots.
func (r *CIMDResolver) Resolve(ctx context.Context, clientID string) (Client, error) {
	if freeIdentifier.MatchString(clientID) {
		return Client{ID: clientID, Name: clientID}, nil
	}
	u, ok := r.metadataURL(clientID)
	if !ok {
		return Client{}, ErrInvalidClient
	}
	r.mu.Lock()
	c, hit := r.cache[clientID]
	failedAt, failed := r.failed[clientID]
	r.mu.Unlock()
	if hit && r.now().Sub(c.fetched) < cimdCacheTTL {
		return c.client, nil
	}
	if failed && r.now().Sub(failedAt) < cimdFailTTL {
		return Client{}, fmt.Errorf("%w: metadata failed recently", ErrInvalidClient)
	}
	client, err := r.fetch(ctx, clientID, u)
	switch {
	case err == nil:
		r.remember(clientID, client)
	case ctx.Err() == nil && !errors.Is(err, errFetchBusy):
		r.rememberFailure(clientID) // the document's failure, not the caller's or ours
	}
	return client, err
}

func (r *CIMDResolver) remember(clientID string, client Client) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.cache) >= cimdCacheSize {
		dropOldest(r.cache, func(c cachedClient) time.Time { return c.fetched })
	}
	delete(r.failed, clientID)
	r.cache[clientID] = cachedClient{client: client, fetched: r.now()}
}

func (r *CIMDResolver) rememberFailure(clientID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.failed) >= cimdFailSize {
		dropOldest(r.failed, func(t time.Time) time.Time { return t })
	}
	r.failed[clientID] = r.now()
}

// dropOldest removes the entry of m with the earliest time.
func dropOldest[V any](m map[string]V, at func(V) time.Time) {
	oldest, first := "", true
	for k, v := range m {
		if first || at(v).Before(at(m[oldest])) {
			oldest, first = k, false
		}
	}
	delete(m, oldest)
}

// metadataURL checks the rules for a Client ID Metadata Document URL: https, port 443,
// a path, no dot segments, no user info, query or fragment.
func (r *CIMDResolver) metadataURL(clientID string) (*url.URL, bool) {
	if len(clientID) > maxClientIDLen {
		return nil, false
	}
	u, err := url.Parse(clientID)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery ||
		u.Fragment != "" || strings.Contains(clientID, "#") || u.Path == "" || u.Path == "/" || u.String() != clientID {
		return nil, false
	}
	if !r.anyPort && u.Port() != "" && u.Port() != "443" {
		return nil, false
	}
	for _, seg := range strings.Split(u.Path, "/") {
		if seg == "." || seg == ".." {
			return nil, false
		}
	}
	return u, true
}

// metadata is the part of a Client ID Metadata Document Home-Mandate uses.
type metadata struct {
	ClientID     string   `json:"client_id"`
	ClientName   string   `json:"client_name"`
	RedirectURIs []string `json:"redirect_uris"`
	AuthMethod   *string  `json:"token_endpoint_auth_method"`
}

func (r *CIMDResolver) fetch(ctx context.Context, clientID string, u *url.URL) (Client, error) {
	select {
	case r.fetching <- struct{}{}:
		defer func() { <-r.fetching }()
	default:
		return Client{}, errFetchBusy
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, clientID, nil)
	if err != nil {
		return Client{}, ErrInvalidClient
	}
	req.Header.Set("Accept", "application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		return Client{}, fmt.Errorf("%w: metadata unreachable", ErrInvalidClient)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, cimdMaxBytes+1))
	if err != nil || resp.StatusCode != http.StatusOK || len(body) > cimdMaxBytes || !isJSON(resp.Header.Get("Content-Type")) {
		return Client{}, fmt.Errorf("%w: metadata not usable", ErrInvalidClient)
	}
	if err := uniqueTopLevelKeys(body); err != nil {
		return Client{}, fmt.Errorf("%w: %w", ErrInvalidClient, err)
	}
	var md metadata
	if err := json.Unmarshal(body, &md); err != nil {
		return Client{}, fmt.Errorf("%w: metadata is not a JSON object", ErrInvalidClient)
	}
	if md.ClientID != clientID || md.AuthMethod != nil && *md.AuthMethod != "none" || len(md.RedirectURIs) > maxRedirectURIs {
		return Client{}, fmt.Errorf("%w: metadata does not match", ErrInvalidClient)
	}
	for _, uri := range md.RedirectURIs {
		if !validRedirectURI(uri) {
			return Client{}, fmt.Errorf("%w: redirect URI not allowed", ErrInvalidClient)
		}
	}
	name := md.ClientName
	if !displayable(name) {
		name = u.Hostname()
	}
	return Client{ID: clientID, Name: name, RedirectURIs: md.RedirectURIs, Verified: true}, nil
}

func isJSON(contentType string) bool {
	mt, _, err := mime.ParseMediaType(contentType)
	return err == nil && (mt == "application/json" || strings.HasSuffix(mt, "+json"))
}

// uniqueTopLevelKeys rejects an object that names a key twice, which encoding/json
// would silently resolve to the last one.
func uniqueTopLevelKeys(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return errors.New("metadata is not a JSON object")
	}
	seen := map[string]bool{}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return errors.New("metadata is not valid JSON")
		}
		key, _ := t.(string)
		if seen[key] {
			return errors.New("metadata repeats a key")
		}
		seen[key] = true
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return errors.New("metadata is not valid JSON")
		}
	}
	return nil
}

// hostPattern is a host name or IP literal with an optional port: nothing that could
// end a Content-Security-Policy directive (the redirect origin goes into form-action).
var hostPattern = regexp.MustCompile(`^([A-Za-z0-9.-]+|\[[0-9A-Fa-f:.]+\])(:[0-9]{1,5})?$`)

// validRedirectURI accepts https URIs and http URIs on loopback (RFC 8252), without
// fragment or user info.
func validRedirectURI(uri string) bool {
	u, err := url.Parse(uri)
	if err != nil || !hostPattern.MatchString(u.Host) || u.User != nil || u.Fragment != "" || strings.Contains(uri, "#") {
		return false
	}
	return u.Scheme == "https" || u.Scheme == "http" && loopbackHost(u.Hostname())
}

func loopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}

// displayable rejects names that could mislead a human (as agent display names, SPEC-v0
// section 3.1 item 8).
func displayable(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > maxClientName {
		return false
	}
	return !strings.ContainsFunc(name, func(r rune) bool { return unicode.In(r, unicode.Cc, unicode.Cf, unicode.Zl, unicode.Zp) })
}

// redirectAllowed compares a requested redirect URI with the registered ones exactly,
// except that the port of a loopback redirect URI may differ (RFC 8252 section 7.3).
func redirectAllowed(registered []string, requested string) bool {
	if requested == "" {
		return false
	}
	if slices.Contains(registered, requested) {
		return true
	}
	req, err := url.Parse(requested)
	if err != nil || req.Scheme != "http" || !loopbackHost(req.Hostname()) || req.User != nil || req.Fragment != "" {
		return false
	}
	for _, r := range registered {
		reg, err := url.Parse(r)
		if err == nil && reg.Scheme == "http" && loopbackHost(reg.Hostname()) && reg.Hostname() == req.Hostname() &&
			reg.EscapedPath() == req.EscapedPath() && reg.RawQuery == req.RawQuery && !req.ForceQuery {
			return true
		}
	}
	return false
}
