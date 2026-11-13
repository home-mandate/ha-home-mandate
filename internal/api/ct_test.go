// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/ctwatch"
)

// fakeCT is the monitoring as the API sees it.
type fakeCT struct {
	mu        sync.Mutex
	status    ctwatch.Status
	statusErr error
	reqErr    error
	ansErr    error
	requested []string // digest/user
	answered  []string // id/user/approve
}

func (f *fakeCT) Status(context.Context) (ctwatch.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status, f.statusErr
}

func (f *fakeCT) RequestAck(_ context.Context, d ctwatch.Digest, user string) (ctwatch.Pending, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.reqErr != nil {
		return ctwatch.Pending{}, f.reqErr
	}
	f.requested = append(f.requested, d.String()+"/"+user)
	return ctwatch.Pending{ID: "00112233445566778899aabbccddeeff", Digest: d, RequestedBy: user, Expires: testStart.Add(5 * time.Minute)}, nil
}

func (f *fakeCT) AnswerAck(_ context.Context, id, user string, approve bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ansErr != nil {
		return f.ansErr
	}
	f.answered = append(f.answered, id+"/"+user+"/"+map[bool]string{true: "yes", false: "no"}[approve])
	return nil
}

var (
	alarmDigest = ctwatch.Digest(sha256.Sum256([]byte("tbs-x")))
	ackedDigest = ctwatch.Digest(sha256.Sum256([]byte("tbs-y")))
)

func activeCT() *fakeCT {
	cert := func(d ctwatch.Digest, r ctwatch.Result) ctwatch.CertStatus {
		return ctwatch.CertStatus{Result: r, Stored: ctwatch.Stored{FirstSeen: testStart,
			Parsed: ctwatch.Parsed{Issuer: "CN=Other CA", Serial: "0a1b", Cert: ctwatch.Cert{Digest: d, Names: []string{"hm.example.org"},
				NotBefore: testStart, NotAfter: testStart.Add(90 * 24 * time.Hour)}}}}
	}
	acked := cert(ackedDigest, ctwatch.None)
	acked.Ack = &ctwatch.Ack{Digest: ackedDigest, By: annaID, Via: ctwatch.ViaPush, At: testStart}
	return &fakeCT{status: ctwatch.Status{Name: "hm.example.org", Since: testStart.Add(-time.Hour), KeyKnown: true,
		LastSuccess: testStart, Sources: []ctwatch.SourceStatus{{Name: "certspotter", LastSuccess: testStart},
			{Name: "crtsh", LastFailure: testStart, Failure: "crt.sh answered 502"}},
		Certs:   []ctwatch.CertStatus{cert(alarmDigest, ctwatch.Alarm), acked},
		Pending: []ctwatch.Pending{{ID: "00112233445566778899aabbccddeeff", Digest: alarmDigest, RequestedBy: adminID, Expires: testStart.Add(time.Minute), UIUsers: []string{annaID}}}}}
}

func TestSystemShowsWhyCTDoesNotRun(t *testing.T) {
	h := newHarness(t)
	var sys wireSystem
	h.ok(http.MethodGet, "/api/system", nil, &sys)
	if sys.CT.State != ctOff || sys.CT.Reason != nil || sys.CT.Sources == nil || sys.CT.Certificates == nil {
		t.Fatalf("off: %+v", sys.CT)
	}
	h.ctState, h.ctReason = ctNotApplicable, "not_public_name"
	h.build()
	h.ok(http.MethodGet, "/api/system", nil, &sys)
	if sys.CT.State != ctNotApplicable || sys.CT.Reason == nil || *sys.CT.Reason != "not_public_name" {
		t.Fatalf("not applicable: %+v", sys.CT)
	}
}

func TestSystemShowsTheMonitoring(t *testing.T) {
	h := newHarness(t)
	h.ct = activeCT()
	h.build()
	var sys wireSystem
	h.ok(http.MethodGet, "/api/system", nil, &sys)
	ct := sys.CT
	if ct.State != ctActive || *ct.Name != "hm.example.org" || !ct.OwnKeyKnown || ct.KeyError != nil || ct.LastSuccess == nil ||
		len(ct.Sources) != 2 || ct.Sources[1].Failure == nil || ct.Sources[0].Failure != nil || len(ct.Certificates) != 2 {
		t.Fatalf("%+v", ct)
	}
	alarm, acked := ct.Certificates[0], ct.Certificates[1]
	if alarm.Result != "alarm" || alarm.Digest != alarmDigest.String() || alarm.Serial != "0A1B" || alarm.Ack != nil ||
		alarm.Pending == nil || !slices.Equal(alarm.Pending.Answerers, []string{annaID}) || alarm.Pending.RequestedByName == nil {
		t.Fatalf("alarm %+v %+v", alarm, alarm.Pending)
	}
	if acked.Result != "none" || acked.Ack == nil || acked.Ack.Via != "push" || acked.Ack.ByName == nil || acked.Pending != nil {
		t.Fatalf("acknowledged %+v", acked)
	}
}

func TestSystemSurvivesAnUnreadableMonitoring(t *testing.T) {
	h := newHarness(t)
	h.ct = &fakeCT{statusErr: errors.New("db")}
	h.build()
	var sys wireSystem
	h.ok(http.MethodGet, "/api/system", nil, &sys)
	if sys.CT.State != ctUnreadable {
		t.Fatalf("%+v", sys.CT)
	}
}

func TestAcknowledgeCertificate(t *testing.T) {
	h := newHarness(t)
	if r := h.do(http.MethodPost, "/api/ct/acknowledge", map[string]any{"digest": alarmDigest.String()}); r.code != http.StatusNotFound {
		t.Fatalf("monitoring off: %d %s", r.code, r.body)
	}
	ct := activeCT()
	h.ct = ct
	h.build()
	for name, body := range map[string]any{
		"no digest":       map[string]any{},
		"not base64url":   map[string]any{"digest": "not a digest"},
		"padded":          map[string]any{"digest": alarmDigest.String() + "="},
		"too short":       map[string]any{"digest": "abc"},
		"unknown member":  map[string]any{"digest": alarmDigest.String(), "x": 1},
		"digest a number": map[string]any{"digest": 5},
	} {
		if r := h.do(http.MethodPost, "/api/ct/acknowledge", body); r.code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, r.code, r.body)
		}
	}
	var p wireCTPending
	h.ok(http.MethodPost, "/api/ct/acknowledge", map[string]any{"digest": alarmDigest.String()}, &p)
	if p.ID == "" || p.RequestedBy != adminID || p.RequestedByName == nil || p.Answerers == nil ||
		!slices.Equal(ct.requested, []string{alarmDigest.String() + "/" + adminID}) {
		t.Fatalf("%+v %v", p, ct.requested)
	}
	for err, want := range map[error]string{ctwatch.ErrNotAlarming: codeNotFound, ctwatch.ErrNoApprover: codeNoApprovers, errors.New("db"): codeInternal} {
		ct.reqErr = err
		if r := h.do(http.MethodPost, "/api/ct/acknowledge", map[string]any{"digest": alarmDigest.String()}); r.errCode() != want {
			t.Errorf("%v: %d %s", err, r.code, r.body)
		}
	}
}

func TestAnswerCertificate(t *testing.T) {
	h := newHarness(t)
	const id = "00112233445566778899aabbccddeeff"
	path := "/api/ct/confirmations/" + id + "/answer"
	if r := h.do(http.MethodPost, path, map[string]any{"approve": true}); r.code != http.StatusNotFound {
		t.Fatalf("monitoring off: %d", r.code)
	}
	ct := activeCT()
	h.ct = ct
	h.build()
	if r := h.do(http.MethodPost, "/api/ct/confirmations/XYZ/answer", map[string]any{"approve": true}); r.code != http.StatusNotFound {
		t.Fatalf("bad ID: %d", r.code)
	}
	if r := h.do(http.MethodPost, path, map[string]any{}); r.code != http.StatusBadRequest || r.field() != "/approve" {
		t.Fatalf("no answer: %d %s", r.code, r.body)
	}
	if r := h.do(http.MethodPost, path, "["); r.code != http.StatusBadRequest {
		t.Fatalf("broken body: %d", r.code)
	}
	var out wireCT
	h.ok(http.MethodPost, path, map[string]any{"approve": false}, &out, as(annaID))
	if out.State != ctActive || !slices.Equal(ct.answered, []string{id + "/" + annaID + "/no"}) {
		t.Fatalf("%+v %v", out, ct.answered)
	}
	for _, err := range []error{ctwatch.ErrNotPending, ctwatch.ErrNotApprover, ctwatch.ErrChannel} {
		ct.ansErr = err
		if r := h.do(http.MethodPost, path, map[string]any{"approve": true}); r.code != http.StatusNotFound {
			t.Errorf("%v: %d", err, r.code)
		}
	}
	ct.ansErr = errors.New("db")
	if r := h.do(http.MethodPost, path, map[string]any{"approve": true}); r.errCode() != codeInternal {
		t.Errorf("store: %d", r.code)
	}
}

func TestCTRequestsAreLimitedPerPerson(t *testing.T) {
	h := newHarness(t)
	h.ct = activeCT()
	h.build()
	for i := range ctLimit {
		if r := h.do(http.MethodPost, "/api/ct/acknowledge", map[string]any{"digest": alarmDigest.String()}); r.code != http.StatusOK {
			t.Fatalf("request %d: %d", i, r.code)
		}
	}
	if r := h.do(http.MethodPost, "/api/ct/confirmations/00112233445566778899aabbccddeeff/answer", map[string]any{"approve": true}); r.code != http.StatusTooManyRequests {
		t.Fatalf("over the limit: %d", r.code)
	}
	if r := h.do(http.MethodPost, "/api/ct/acknowledge", map[string]any{"digest": alarmDigest.String()}, as(annaID)); r.code != http.StatusOK {
		t.Fatalf("another person: %d", r.code)
	}
}

func TestUserName(t *testing.T) {
	h := newHarness(t)
	if h.srv.UserName(context.Background(), adminID) == "" || h.srv.UserName(context.Background(), "nobody") != "" {
		t.Fatal("names")
	}
}
