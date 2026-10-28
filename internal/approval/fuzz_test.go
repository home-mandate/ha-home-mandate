// SPDX-License-Identifier: AGPL-3.0-or-later

package approval

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/ha"
)

// lastNotice keeps the last notification with buttons; it never blocks.
type lastNotice struct {
	mu sync.Mutex
	n  ha.Notification
}

func (l *lastNotice) Notify(_ context.Context, _ string, n ha.Notification) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(n.Actions) > 0 {
		l.n = n
	}
	return nil
}

func (l *lastNotice) get() ha.Notification {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.n
}

// FuzzHandleEvent: a mobile_app_notification_action or _cleared event can carry anything
// in its data and user. A request ends only on the exact action of its own nonce, it is
// approved only by an approver pressing approve, and nothing panics. In the inputs,
// NONCE stands for the nonce of the open request and TAG for its tag, so that the fuzzer
// reaches the paths behind them.
func FuzzHandleEvent(f *testing.F) {
	n := &lastNotice{}
	svc, _ := newService(f, time.Hour, n)
	open := func(t testing.TB) (Pending, ha.Notification) {
		req := request()
		req.Timeout = time.Hour
		p, err := svc.Start(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		return p, n.get()
	}
	p, notice := open(f)
	for _, seed := range []struct{ data, user string }{
		{`{"action":"HM_APPROVE_NONCE"}`, u1},
		{`{"action":"HM_DENY_NONCE"}`, u2},
		{`{"action":"HM_APPROVE_NONCE"}`, u3},
		{`{"action":"HM_APPROVE_NONCE"}`, ""},
		{`{"action":"HM_APPROVE_NONCE","action":"OPEN_GARAGE"}`, u1},
		{`{"ACTION":"HM_DENY_NONCE"}`, u1},
		{`{"action":"HM_APPROVE_00000000000000000000000000000000"}`, u1},
		{`{"action":"HM_APPROVE_NONCEx"}`, u1},
		{`{"action":"OPEN_GARAGE"}`, u1},
		{`{"tag":"TAG"}`, u1},
		{`{"tag":"hm_request_"}`, u2},
		{`[1]`, u1},
		{`{"action":1}`, "<script>"},
		{``, ""},
	} {
		f.Add([]byte(seed.data), seed.user)
	}
	f.Fuzz(func(t *testing.T, data []byte, user string) {
		nonce := strings.TrimPrefix(notice.Actions[0].Action, approvePrefix)
		data = bytes.ReplaceAll(bytes.ReplaceAll(data, []byte("NONCE"), []byte(nonce)), []byte("TAG"), []byte(notice.Tag))
		svc.HandleCleared(ha.Event{EventType: ha.EventMobileAppNotificationCleared, Data: data, Context: ha.EventContext{UserID: user}})
		svc.HandleEvent(ha.Event{EventType: ha.EventMobileAppNotificationAction, Data: data, Context: ha.EventContext{UserID: user}})

		var answer struct {
			Action string `json:"action"`
		}
		_ = json.Unmarshal(data, &answer)
		approve, deny := answer.Action == approvePrefix+nonce, answer.Action == denyPrefix+nonce
		if svc.IsOpen(p.ID) {
			if approve || deny {
				t.Fatalf("the answer %q did not end the request", data)
			}
			return
		}
		if !approve && !deny {
			t.Fatalf("the request ended on %q", data)
		}
		var res Result
		select {
		case res = <-p.Done:
		case <-time.After(5 * time.Second):
			t.Fatal("no result")
		}
		approver := user == u1 || user == u2
		want := OutcomeInvalidResponse
		switch {
		case approver && approve:
			want = OutcomeApproved
		case approver:
			want = OutcomeRejected
		}
		if res.Outcome != want {
			t.Fatalf("outcome %q for %q by %q, want %q", res.Outcome, data, user, want)
		}
		p, notice = open(t)
	})
}
