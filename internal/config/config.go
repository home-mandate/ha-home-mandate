// SPDX-License-Identifier: AGPL-3.0-or-later

// Package config reads the configuration: in app mode (Home Assistant OS) from the
// Supervisor's /data/options.json and SUPERVISOR_TOKEN, in container mode from HM_*
// environment variables. Everything is validated strictly; a bad value stops the start.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
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
	// TLSCert and TLSKey are absolute paths, both set or both empty.
	TLSCert, TLSKey string
	// MCPAddr is the listen address of the MCP endpoint; without TLS it is a loopback
	// address (decision 1: no plaintext on the LAN).
	MCPAddr         string
	ApprovalTimeout time.Duration
	LogLevel        slog.Level
}

// String omits nothing but the token, which Secret redacts.
func (c Config) String() string {
	return fmt.Sprintf("mode=%s data=%s ha=%s token=%s tls=%t mcp=%s approval=%s log=%s",
		c.Mode, c.DataDir, c.HAURL, c.HAToken, c.TLSCert != "", c.MCPAddr, c.ApprovalTimeout, c.LogLevel)
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
	cfg.MCPAddr = net.JoinHostPort("127.0.0.1", mcpPort)
	if cfg.TLSCert != "" {
		cfg.MCPAddr = ":" + mcpPort
	}
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
	cfg.TLSCert, cfg.TLSKey = getenv("HM_TLS_CERT"), getenv("HM_TLS_KEY")
	if (cfg.TLSCert == "") != (cfg.TLSKey == "") ||
		cfg.TLSCert != "" && (!filepath.IsAbs(cfg.TLSCert) || !filepath.IsAbs(cfg.TLSKey)) {
		return Config{}, fmt.Errorf("%w: HM_TLS_CERT and HM_TLS_KEY must both be absolute paths or both be unset", ErrInvalid)
	}
	if cfg.MCPAddr, err = mcpAddr(getenv("HM_MCP_ADDR"), cfg.TLSCert != ""); err != nil {
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
