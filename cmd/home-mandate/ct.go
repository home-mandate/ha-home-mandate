// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"net/url"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/api"
	"github.com/home-mandate/ha-home-mandate/internal/ctwatch"
	"github.com/home-mandate/ha-home-mandate/internal/ha"
	"github.com/home-mandate/ha-home-mandate/internal/netx"
)

const (
	// ctTimeout bounds one request to a CT source; crt.sh can be slow.
	ctTimeout = time.Minute
	// ctHandshakeTimeout bounds the handshake with the reverse proxy for its key.
	ctHandshakeTimeout = 10 * time.Second
)

// ctSetting is the monitoring as the gateway runs it: the watcher, or why there is none.
type ctSetting struct {
	watcher *ctwatch.Watcher
	state   string // off, not_applicable or active
	reason  string
}

// newCTWatch sets up the Certificate Transparency monitoring of the public name (issue
// #66), after the listener, whose certificate tells the gateway's own key. The watcher
// and the API need each other; until the API exists nobody is an administrator.
func (g *gateway) newCTWatch(ctx context.Context, client *ha.Client, uiAPI *atomic.Pointer[api.Server], logger *slog.Logger) (ctSetting, error) {
	cfg := g.state.cfg
	if !cfg.CTWatch {
		return ctSetting{state: "off"}, nil
	}
	name, reason := ctwatch.Applicable(cfg.PublicURL)
	if reason != "" {
		logger.Info("certificate monitoring not applicable", "reason", reason)
		return ctSetting{state: "not_applicable", reason: reason}, nil
	}
	key, err := g.ctKey(name)
	if err != nil {
		return ctSetting{}, err
	}
	if key == nil {
		logger.Warn("certificate monitoring: Home-Mandate cannot see its own certificate (TLS ends elsewhere); every certificate for the name needs an acknowledgement")
	}
	httpClient := netx.NewClient(netx.Config{Timeout: ctTimeout})
	w := ctwatch.New(ctwatch.Config{Name: name, Store: ctwatch.NewStore(g.state.store.DB()),
		Sources: []ctwatch.Source{ctwatch.NewCertSpotter(httpClient, string(cfg.CTKey)), ctwatch.NewCrtSh(httpClient)},
		Key:     key, Approvers: g.state.approvers, ServiceUser: g.serviceUser,
		IsAdmin: func(ctx context.Context, user string) (bool, error) {
			if a := uiAPI.Load(); a != nil {
				return a.IsAdmin(ctx, user)
			}
			return false, errors.New("starting")
		},
		UserName: func(ctx context.Context, user string) string {
			if a := uiAPI.Load(); a != nil {
				return a.UserName(ctx, user)
			}
			return ""
		},
		Notifier: client, Bell: client.Bell(), Language: g.householdLanguage,
		ConfirmTimeout: func() time.Duration { return cfg.ApprovalTimeout },
		Link:           g.uiLink,
		OnChange: func() {
			if a := uiAPI.Load(); a != nil {
				a.SystemChanged()
			}
		},
		Logger: logger})
	if _, err := client.SubscribeEvents(ctx, ha.EventMobileAppNotificationAction, w.HandleEvent); err != nil {
		return ctSetting{}, fmt.Errorf("subscribe %s for certificate monitoring: %w", ha.EventMobileAppNotificationAction, err)
	}
	logger.Info("certificate monitoring on", "name", name, "own_key", key != nil)
	return ctSetting{watcher: w, state: "active"}, nil
}

// ctKey is where the gateway's own key is read: its certificate files, or the reverse
// proxy at its configured address; nil when TLS ends where Home-Mandate cannot look.
func (g *gateway) ctKey(name string) (ctwatch.KeySource, error) {
	cfg := g.state.cfg
	switch {
	case g.certs != nil:
		return ctwatch.FileKey{Loader: g.certs}, nil
	case cfg.Proxy.IsValid():
		port, err := publicPort(cfg.PublicURL)
		if err != nil {
			return nil, err
		}
		return ctwatch.ProxyKey{Addr: netip.AddrPortFrom(cfg.Proxy, port), ServerName: name, Timeout: ctHandshakeTimeout}, nil
	}
	return nil, nil
}

// publicPort is the port of an https public URL, 443 when it names none.
func publicPort(publicURL string) (uint16, error) {
	u, err := url.Parse(publicURL)
	if err != nil {
		return 0, err
	}
	if u.Port() == "" {
		return 443, nil
	}
	port, err := strconv.ParseUint(u.Port(), 10, 16)
	if err != nil || port == 0 {
		return 0, fmt.Errorf("public URL port %q", u.Port())
	}
	return uint16(port), nil
}

// uiLink is what a tap on a CT notification opens: the UI in direct mode, the app's panel
// in the app; "" otherwise.
func (g *gateway) uiLink() string {
	switch {
	case g.direct != nil:
		return g.uiAddress()
	case g.slug != "":
		return ingressPanelPath + g.slug
	default:
		return ""
	}
}
