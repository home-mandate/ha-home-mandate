// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"

	"github.com/home-mandate/ha-home-mandate/internal/audit"
	"github.com/home-mandate/ha-home-mandate/internal/config"
	"github.com/home-mandate/ha-home-mandate/internal/dpop"
	"github.com/home-mandate/ha-home-mandate/internal/store"
)

// The settings of config.changed that come from Home-Mandate's own configuration are
// compared with those of the last start (SPEC-v0 section 9.2, audit/v1): the first start
// with them records nothing, a changed one is recorded once.
func TestSettingsOfTheConfigurationAreRecordedWhenTheyChange(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "hm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	log := audit.New(st.DB(), "household:t")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	signer := &audit.Signer{LogID: "0198f1c2-7c3a-7000-8000-0000000000aa", KeyID: "log-0011223344556677"}

	first := config.Config{PublicURL: "https://hm.example.org", DPoP: dpop.ModeOptional, Proxy: netip.MustParseAddr("192.0.2.10")}
	recordSettings(ctx, log, first, signer, logger)
	recordSettings(ctx, log, first, signer, logger)
	if n, _ := log.LastSeq(ctx); n != 0 {
		t.Fatalf("%d entries after starts without a change", n)
	}
	second := config.Config{PublicURL: "https://hm.example.net", DPoP: dpop.ModeRequired}
	recordSettings(ctx, log, second, nil, logger)
	var buf bytes.Buffer
	if err := log.Export(ctx, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		`"config":{"previous_value":"log-0011223344556677","setting":"checkpoint_key"}`,
		`"config":{"previous_value":"https://hm.example.org","setting":"public_origin","value":"https://hm.example.net"}`,
		`"config":{"previous_value":"allowed","setting":"unbound_tokens","value":"refused"}`,
		`"config":{"previous_value":"192.0.2.10","setting":"trusted_proxy"}`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in\n%s", want, out)
		}
	}
	if strings.Contains(out, `"setting":"remote_access"`) {
		t.Errorf("remote access recorded although it stayed on:\n%s", out)
	}
	third := config.Config{DPoP: dpop.ModeRequired}
	recordSettings(ctx, log, third, nil, logger)
	buf.Reset()
	_ = log.Export(ctx, &buf)
	if !strings.Contains(buf.String(), `"config":{"previous_value":"on","setting":"remote_access","value":"off"}`) ||
		!strings.Contains(buf.String(), `"config":{"previous_value":"https://hm.example.net","setting":"public_origin"}`) {
		t.Errorf("without a public URL:\n%s", buf.String())
	}
}
