// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/approval"
	"github.com/home-mandate/ha-home-mandate/internal/audit"
	"github.com/home-mandate/ha-home-mandate/internal/config"
)

// SPEC-v0 section 11.1 item 9 at the level of serve: what a previous process left in the
// approval journal is recorded before the gateway exists, and once Home Assistant is
// connected the approvers' notifications are replaced.
func TestRequestsEndedByARestartAreRecordedAndAnnounced(t *testing.T) {
	ctx := context.Background()
	fake := &fakeHomeAssistant{t: t}
	haSrv := httptest.NewServer(http.HandlerFunc(fake.serve))
	defer haSrv.Close()
	c := newCLI(t)
	s, err := openStore(ctx, c.envVars["HM_DATA_DIR"])
	if err != nil {
		t.Fatal(err)
	}
	defer s.store.Close()
	now := time.Now()
	req := approval.Request{ClientID: "hm-client:voice", Agent: "Voice assistant", EntityID: "lock.front_door", Device: "Front door", Action: "unlock",
		Record: &approval.Record{ParamsDigest: "sha256:" + strings.Repeat("b", 64), Entry: audit.Entry{
			Agent:      &audit.Agent{ClientID: "hm-client:voice", DisplayName: "Voice assistant"},
			Request:    &audit.Request{Time: now, Timezone: "Europe/Berlin", Resource: audit.Resource{EntityID: "lock.front_door", Category: "lock"}, Action: "unlock"},
			Mandate:    &audit.Mandate{ID: "m-voice", Digest: "sha256:" + strings.Repeat("a", 64)},
			Evaluation: &audit.Evaluation{Decision: "ask", Reason: "rule", RuleID: ptr("r-locks")},
		}}}
	if err := s.journal.Open(ctx, approval.Opened{ID: strings.Repeat("1", 32), Tag: "hm_request_1", Request: req, Created: now, Expires: now.Add(time.Minute),
		Notified: []approval.Notified{{UserID: ownerID, Service: "mobile_app_markus", Lang: "de"}}}); err != nil {
		t.Fatal(err)
	}

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	if err := recoverApprovals(ctx, approval.NewJournal(s.store.DB(), logger), s.log, logger); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "approval requests ended by the restart") {
		t.Errorf("log = %s", logs.String())
	}
	var buf bytes.Buffer
	if err := s.log.Export(ctx, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"cause":"interrupted"`) {
		t.Errorf("audit log = %s", buf.String())
	}

	s.cfg = config.Config{Mode: config.ModeContainer, HAURL: "ws" + strings.TrimPrefix(haSrv.URL, "http") + "/api/websocket", HAToken: "t",
		MCPAddr: "127.0.0.1:0", ApprovalTimeout: 2 * time.Minute}
	runCtx, cancel := context.WithCancel(ctx)
	g, err := newGateway(runCtx, s, slog.New(slog.DiscardHandler))
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	done := make(chan int, 1)
	go func() { done <- g.run(runCtx) }()
	defer func() {
		cancel()
		<-done
	}()
	deadline := time.Now().Add(10 * time.Second)
	for !slices.Contains(fake.received(), "notify.mobile_app_markus") {
		if time.Now().After(deadline) {
			t.Fatalf("calls %v", fake.received())
		}
		time.Sleep(10 * time.Millisecond)
	}
	var notice string
	waitUntil(t, func() bool {
		_ = s.store.DB().QueryRow(`SELECT notice FROM approval_journal`).Scan(&notice)
		return notice == ""
	})
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func ptr[T any](v T) *T { return &v }
