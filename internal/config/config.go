// SPDX-License-Identifier: AGPL-3.0-or-later

// Package config reads the configuration: in app mode (Home Assistant OS) from the
// Supervisor's /data/options.json and SUPERVISOR_TOKEN, in container mode from HM_*
// environment variables. Everything is validated strictly; a bad value stops the start.
package config

import (
	"bytes"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/home-mandate/home-mandate/internal/ha"
)

// ErrInvalid means the configuration cannot be used.
var ErrInvalid = errors.New("config: invalid configuration")

// Mode is the operating mode (docs/ARCHITECTURE.md section 8).
type Mode string

const (
	ModeApp       Mode = "app"
	ModeContainer Mode = "container"
)

const (
	appDataDir    = "/data"
	appOptions    = "/data/options.json"
	appSSLDir     = "/ssl"
	supervisorWS  = "ws://supervisor/core/websocket"
	mcpPort       = "8765"
	ingressPort   = "8099"
	minApproval   = 30 * time.Second
	maxApproval   = 600 * time.Second
	maxTokenBytes = 4096

	// DefaultApprovalTimeout is the timeout of approval requests unless configured.
	DefaultApprovalTimeout = 2 * time.Minute
)

// Config is the validated configuration.
type Config struct {
	Mode    Mode
	DataDir string
	HAURL   string
	HAToken ha.Secret
	// HARootCAs replaces the system roots for wss:// to Home Assistant (HM_HA_CA_FILE),
	// e.g. for a self-signed certificate. Nil means the system roots.
	HARootCAs *x509.CertPool
	// TLSCert and TLSKey are absolute paths, both set or both empty.
	TLSCert, TLSKey string
	// MCPAddr is the listen address of the MCP endpoint; without TLS it is a loopback
	// address (decision 1: no plaintext on the LAN).
	MCPAddr string
	// PDPAddr, if set, serves the AuthZEN endpoint for other gateways; loopback only.
	PDPAddr string
	// IngressAddr is the listen address of the local UI behind Home Assistant Ingress:
	// always :8099 in app mode, opt-in through HM_INGRESS_ADDR in container mode (decision
	// U3). Whatever the address, only requests from the Supervisor (172.30.32.2) are served.
	IngressAddr string
	// IngressProxy is the one address requests to the UI listener may come from: the
	// Supervisor (172.30.32.2, fixed in Home Assistant OS) in app mode, HM_INGRESS_PROXY in
	// container mode (decision U2). Exactly one IP, never a range.
	IngressProxy netip.Addr
	// PublicURL is the origin agents and browsers reach Home-Mandate at, e.g.
	// https://hm.example.org:8765: OAuth issuer, base of the MCP resource and of the
	// sign-in pages. Empty means OAuth is off. Plaintext only for loopback (decision 1).
	PublicURL string
	// HABrowserURL is the origin of Home Assistant as the human's browser reaches it, for
	// the sign-in redirect; empty in app mode without the option.
	HABrowserURL string
	// HAHTTPURL is the origin of Home Assistant's HTTP API as Home-Mandate reaches it,
	// for exchanging and revoking sign-in codes.
	HAHTTPURL string
	// ApprovalTimeout is the upper limit for waiting for an approval; a mandate may
	// only shorten it.
	ApprovalTimeout time.Duration
	LogLevel        slog.Level
}

// String omits nothing but the token, which Secret redacts.
func (c Config) String() string {
	return fmt.Sprintf("mode=%s data=%s ha=%s token=%s tls=%t mcp=%s ingress=%s public=%s approval=%s log=%s",
		c.Mode, c.DataDir, c.HAURL, c.HAToken, c.TLSCert != "", c.MCPAddr, c.IngressAddr, c.PublicURL, c.ApprovalTimeout, c.LogLevel)
}

// DataDir returns the data directory without reading the rest of the configuration, so
// that the administration commands need no Home Assistant credentials.
func DataDir(getenv func(string) string) (string, error) {
	if getenv("SUPERVISOR_TOKEN") != "" {
		return appDataDir, nil
	}
	dir := getenv("HM_DATA_DIR")
	if dir == "" {
		return appDataDir, nil
	}
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("%w: HM_DATA_DIR must be absolute", ErrInvalid)
	}
	return dir, nil
}

// Load reads the configuration. getenv and readFile are os.Getenv and os.ReadFile in
// production.
func Load(getenv func(string) string, readFile func(string) ([]byte, error)) (Config, error) {
	if token := getenv("SUPERVISOR_TOKEN"); token != "" {
		return loadApp(ha.Secret(token), readFile)
	}
	return loadContainer(getenv, readFile)
}

type appOptionsFile struct {
	TLSCertFile            string `json:"tls_certfile"`
	TLSKeyFile             string `json:"tls_keyfile"`
	ApprovalTimeoutSeconds int    `json:"approval_timeout_seconds"`
	LogLevel               string `json:"log_level"`
	PublicURL              string `json:"public_url"`
	HABrowserURL           string `json:"ha_browser_url"`
}

func loadApp(token ha.Secret, readFile func(string) ([]byte, error)) (Config, error) {
	data, err := readFile(appOptions)
	if err != nil {
		return Config{}, fmt.Errorf("%w: read %s: %w", ErrInvalid, appOptions, err)
	}
	var opts appOptionsFile
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&opts); err != nil {
		return Config{}, fmt.Errorf("%w: %s: %w", ErrInvalid, appOptions, err)
	}
	cfg := Config{Mode: ModeApp, DataDir: appDataDir, HAURL: supervisorWS, HAToken: token}
	if cfg.TLSCert, cfg.TLSKey, err = sslPaths(opts.TLSCertFile, opts.TLSKeyFile); err != nil {
		return Config{}, err
	}
	if cfg.ApprovalTimeout, err = approvalTimeout(opts.ApprovalTimeoutSeconds); err != nil {
		return Config{}, err
	}
	if cfg.LogLevel, err = logLevel(opts.LogLevel); err != nil {
		return Config{}, err
	}
	cfg.IngressAddr = ":" + ingressPort
	cfg.IngressProxy = SupervisorAddr
	cfg.MCPAddr = net.JoinHostPort("127.0.0.1", mcpPort)
	if cfg.TLSCert != "" {
		cfg.MCPAddr = ":" + mcpPort
	}
	if cfg.PublicURL, err = publicURL(opts.PublicURL); err != nil {
		return Config{}, err
	}
	if cfg.HABrowserURL, err = browserURL(opts.HABrowserURL); err != nil {
		return Config{}, err
	}
	if cfg.PublicURL != "" && cfg.HABrowserURL == "" {
		return Config{}, fmt.Errorf("%w: public_url needs ha_browser_url for the sign-in of humans", ErrInvalid)
	}
	cfg.HAHTTPURL = appHAHTTP
	return cfg, nil
}

// sslPaths accepts plain file names in /ssl only: no separators, no "..", no hidden files.
func sslPaths(cert, key string) (string, string, error) {
	if cert == "" && key == "" {
		return "", "", nil
	}
	for _, name := range []string{cert, key} {
		if name == "" || strings.ContainsAny(name, `/\`) || strings.HasPrefix(name, ".") || path.Clean(name) != name {
			return "", "", fmt.Errorf("%w: TLS file names must be plain file names in %s", ErrInvalid, appSSLDir)
		}
	}
	return path.Join(appSSLDir, cert), path.Join(appSSLDir, key), nil
}

func loadContainer(getenv func(string) string, readFile func(string) ([]byte, error)) (Config, error) {
	cfg := Config{Mode: ModeContainer, HAURL: getenv("HM_HA_URL"), DataDir: getenv("HM_DATA_DIR")}
	if !strings.HasPrefix(cfg.HAURL, "ws://") && !strings.HasPrefix(cfg.HAURL, "wss://") {
		return Config{}, fmt.Errorf("%w: HM_HA_URL must be a ws:// or wss:// URL", ErrInvalid)
	}
	token, err := containerToken(getenv, readFile)
	if err != nil {
		return Config{}, err
	}
	cfg.HAToken = token
	if cfg.DataDir == "" {
		cfg.DataDir = appDataDir
	}
	if !filepath.IsAbs(cfg.DataDir) {
		return Config{}, fmt.Errorf("%w: HM_DATA_DIR must be absolute", ErrInvalid)
	}
	if cfg.HARootCAs, err = caPool(getenv("HM_HA_CA_FILE"), readFile); err != nil {
		return Config{}, err
	}
	cfg.TLSCert, cfg.TLSKey = getenv("HM_TLS_CERT"), getenv("HM_TLS_KEY")
	if (cfg.TLSCert == "") != (cfg.TLSKey == "") ||
		cfg.TLSCert != "" && (!filepath.IsAbs(cfg.TLSCert) || !filepath.IsAbs(cfg.TLSKey)) {
		return Config{}, fmt.Errorf("%w: HM_TLS_CERT and HM_TLS_KEY must both be absolute paths or both be unset", ErrInvalid)
	}
	if cfg.MCPAddr, err = mcpAddr(getenv("HM_MCP_ADDR"), cfg.TLSCert != ""); err != nil {
		return Config{}, err
	}
	if cfg.PDPAddr = getenv("HM_PDP_ADDR"); cfg.PDPAddr != "" {
		if _, err := mcpAddr(cfg.PDPAddr, false); err != nil {
			return Config{}, fmt.Errorf("%w: HM_PDP_ADDR must be a loopback address", ErrInvalid)
		}
	}
	if cfg.IngressAddr, err = ingressAddr(getenv("HM_INGRESS_ADDR")); err != nil {
		return Config{}, err
	}
	if cfg.IngressProxy, err = ingressProxy(getenv("HM_INGRESS_PROXY"), cfg.IngressAddr != ""); err != nil {
		return Config{}, err
	}
	cfg.ApprovalTimeout = DefaultApprovalTimeout
	if s := getenv("HM_APPROVAL_TIMEOUT"); s != "" {
		seconds, err := strconv.Atoi(s)
		if err != nil {
			return Config{}, fmt.Errorf("%w: HM_APPROVAL_TIMEOUT must be a number of seconds", ErrInvalid)
		}
		if cfg.ApprovalTimeout, err = approvalTimeout(seconds); err != nil {
			return Config{}, err
		}
	}
	if cfg.LogLevel, err = logLevel(getenv("HM_LOG_LEVEL")); err != nil {
		return Config{}, err
	}
	if cfg.PublicURL, err = publicURL(getenv("HM_PUBLIC_URL")); err != nil {
		return Config{}, err
	}
	cfg.HAHTTPURL = httpOrigin(cfg.HAURL)
	cfg.HABrowserURL = cfg.HAHTTPURL
	if s := getenv("HM_HA_BROWSER_URL"); s != "" {
		if cfg.HABrowserURL, err = browserURL(s); err != nil {
			return Config{}, err
		}
	}
	return cfg, nil
}

func containerToken(getenv func(string) string, readFile func(string) ([]byte, error)) (ha.Secret, error) {
	token, file := getenv("HM_HA_TOKEN"), getenv("HM_HA_TOKEN_FILE")
	switch {
	case token != "" && file != "":
		return "", fmt.Errorf("%w: set HM_HA_TOKEN or HM_HA_TOKEN_FILE, not both", ErrInvalid)
	case file != "":
		data, err := readFile(file)
		if err != nil {
			return "", fmt.Errorf("%w: HM_HA_TOKEN_FILE: %w", ErrInvalid, err)
		}
		token = strings.TrimSpace(string(data))
	}
	if token == "" || len(token) > maxTokenBytes {
		return "", fmt.Errorf("%w: HM_HA_TOKEN or HM_HA_TOKEN_FILE is required", ErrInvalid)
	}
	return ha.Secret(token), nil
}

// caPool reads PEM certificates to trust for Home Assistant; "" means system roots.
func caPool(file string, readFile func(string) ([]byte, error)) (*x509.CertPool, error) {
	if file == "" {
		return nil, nil
	}
	if !filepath.IsAbs(file) {
		return nil, fmt.Errorf("%w: HM_HA_CA_FILE must be absolute", ErrInvalid)
	}
	data, err := readFile(file)
	if err != nil {
		return nil, fmt.Errorf("%w: HM_HA_CA_FILE: %w", ErrInvalid, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(data) {
		return nil, fmt.Errorf("%w: HM_HA_CA_FILE contains no PEM certificate", ErrInvalid)
	}
	return pool, nil
}

// mcpAddr refuses a plaintext listener outside loopback (decision 1).
func mcpAddr(addr string, tls bool) (string, error) {
	if addr == "" {
		if tls {
			return ":" + mcpPort, nil
		}
		return net.JoinHostPort("127.0.0.1", mcpPort), nil
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("%w: HM_MCP_ADDR: %w", ErrInvalid, err)
	}
	if !tls {
		ip := net.ParseIP(host)
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return "", fmt.Errorf("%w: without TLS the MCP endpoint may only listen on loopback", ErrInvalid)
		}
	}
	return addr, nil
}

// SupervisorAddr is the Supervisor's address in Home Assistant OS: the hassio network
// 172.30.32.0/23 and the Supervisor at .2 are constants of the Supervisor.
var SupervisorAddr = netip.MustParseAddr("172.30.32.2")

// ingressProxy reads HM_INGRESS_PROXY: required with HM_INGRESS_ADDR, one IP address (no
// range, zone, unspecified or multicast address), meaningless without it.
func ingressProxy(value string, listening bool) (netip.Addr, error) {
	switch {
	case value == "" && listening:
		return netip.Addr{}, fmt.Errorf("%w: HM_INGRESS_ADDR needs HM_INGRESS_PROXY, the address of the proxy in front of it", ErrInvalid)
	case value == "":
		return netip.Addr{}, nil
	case !listening:
		return netip.Addr{}, fmt.Errorf("%w: HM_INGRESS_PROXY without HM_INGRESS_ADDR", ErrInvalid)
	}
	ip, err := netip.ParseAddr(value)
	if err != nil || ip.Zone() != "" || ip.IsUnspecified() || ip.IsMulticast() {
		return netip.Addr{}, fmt.Errorf("%w: HM_INGRESS_PROXY must be one IP address", ErrInvalid)
	}
	return ip.Unmap(), nil
}

// ingressAddr accepts host:port with a numeric port, or nothing (no UI listener).
func ingressAddr(addr string) (string, error) {
	if addr == "" {
		return "", nil
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("%w: HM_INGRESS_ADDR: %w", ErrInvalid, err)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return "", fmt.Errorf("%w: HM_INGRESS_ADDR needs a port between 1 and 65535", ErrInvalid)
	}
	return addr, nil
}

func approvalTimeout(seconds int) (time.Duration, error) {
	d := time.Duration(seconds) * time.Second
	if d < minApproval || d > maxApproval {
		return 0, fmt.Errorf("%w: approval timeout must be between %s and %s", ErrInvalid, minApproval, maxApproval)
	}
	return d, nil
}

func logLevel(s string) (slog.Level, error) {
	switch s {
	case "debug":
		return slog.LevelDebug, nil
	case "", "info":
		return slog.LevelInfo, nil
	case "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("%w: log level must be debug, info, warning or error", ErrInvalid)
}
