// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build e2e

package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	specaudit "github.com/home-mandate/spec/audit"
	"github.com/home-mandate/spec/jws"
)

// audit/v1 against the release image (SPEC-v0 sections 9 and 14): a new log is written in
// version 1; a change of the household time zone in Home Assistant is recorded when the
// gateway reads it; every export is recorded with its range; the log verifies with the
// reference verifier of the specification, anchored by the gateway's checkpoint key.

const typeV1 = `"type":"https://home-mandate.org/audit/v1"`

// setTimeZone sets the time zone of Home Assistant and returns the one before.
func setTimeZone(t *testing.T, zone string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := dialHA(ctx, env.haToken)
	if err != nil {
		t.Fatal(err)
	}
	defer c.conn.CloseNow()
	cfg, err := c.command(ctx, map[string]any{"type": "get_config"})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := cfg.(map[string]any)["time_zone"].(string)
	if _, err := c.command(ctx, map[string]any{"type": "config/core/update", "time_zone": zone}); err != nil {
		t.Fatal(err)
	}
	return before
}

func TestAuditVersion1(t *testing.T) {
	// The gateway reads the time zone when it connects to Home Assistant; it starts with a
	// checkpoint.
	before := setTimeZone(t, "Pacific/Auckland")
	t.Cleanup(func() { setTimeZone(t, before) })
	killGateway(t)
	changed := `"config":{"previous_value":"` + before + `","setting":"timezone","value":"Pacific/Auckland"}`
	eventually(t, "time zone change recorded", time.Minute, func() bool { return hasLine(auditLog(t), `"event":"config.changed"`, changed) })

	// Every export is recorded, with the range it contained, by the local administrator.
	first := cli(t, "", "audit", "export")
	second := cli(t, "", "audit", "export")
	if !strings.HasPrefix(second, first) || !hasLine(second[len(first):], `"event":"log.exported"`, `"id":"local-admin"`, `"to_seq":`) {
		t.Errorf("the export is not recorded:\n%s", strings.TrimPrefix(second, first))
	}

	// A log the new version started is written in version 1 alone.
	for i, line := range strings.Split(strings.TrimSpace(second), "\n") {
		if !strings.Contains(line, typeV1) {
			t.Fatalf("entry %d is not of audit/v1: %s", i+1, line)
		}
	}
	// The reference verifier reads it, anchored by the gateway's checkpoints: a start writes
	// one over what the log holds.
	killGateway(t)
	keyOut := cli(t, "", "audit", "key")
	logID, set, _ := strings.Cut(strings.TrimPrefix(keyOut, "log_id="), "\n")
	keys, err := jws.ParseJWKS([]byte(strings.TrimSpace(set)))
	if err != nil {
		t.Fatalf("audit key = %q: %v", keyOut, err)
	}
	log := cli(t, "", "audit", "export")
	r, err := specaudit.VerifyJSONLinesAnchored(strings.NewReader(log), specaudit.Anchor{Keys: keys, LogID: strings.TrimSpace(logID)})
	if err != nil || !r.Valid || r.AnchoredSeq == 0 || r.UnknownVersion.Found() {
		t.Errorf("reference verifier = %+v, %v", r, err)
	}
}
