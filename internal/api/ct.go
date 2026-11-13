// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"

	"github.com/home-mandate/ha-home-mandate/internal/ctwatch"
)

// CT is the Certificate Transparency monitoring (ctwatch.Watcher).
type CT interface {
	Status(ctx context.Context) (ctwatch.Status, error)
	RequestAck(ctx context.Context, digest ctwatch.Digest, user string) (ctwatch.Pending, error)
	AnswerAck(ctx context.Context, id, user string, approve bool) error
}

// States of the monitoring in the system status.
const (
	ctOff           = "off"
	ctNotApplicable = "not_applicable"
	ctActive        = "active"
	// ctUnreadable: the monitoring runs, but its state could not be read.
	ctUnreadable = "unreadable"
)

var ctConfirmationID = regexp.MustCompile(`^[0-9a-f]{32}$`)

type wireCTSource struct {
	Name        string  `json:"name"`
	LastSuccess *string `json:"last_success"`
	LastFailure *string `json:"last_failure"`
	Failure     *string `json:"failure"`
}

type wireCTAck struct {
	By     string  `json:"by"`
	ByName *string `json:"by_name"`
	Via    string  `json:"via"`
	At     string  `json:"at"`
}

type wireCTPending struct {
	ID              string   `json:"id"`
	Expires         string   `json:"expires"`
	RequestedBy     string   `json:"requested_by"`
	RequestedByName *string  `json:"requested_by_name"`
	Answerers       []string `json:"answerers"` // who may answer in the UI
}

type wireCTCert struct {
	Digest    string         `json:"digest"`
	Names     []string       `json:"names"`
	Issuer    string         `json:"issuer"`
	Serial    string         `json:"serial"`
	NotBefore string         `json:"not_before"`
	NotAfter  string         `json:"not_after"`
	FirstSeen string         `json:"first_seen"`
	Result    string         `json:"result"` // alarm, past-exposure or none (acknowledged)
	Ack       *wireCTAck     `json:"acknowledged"`
	Pending   *wireCTPending `json:"pending"`
}

type wireCT struct {
	State        string         `json:"state"`
	Reason       *string        `json:"reason"`
	Name         *string        `json:"name"`
	Since        *string        `json:"since"`
	OwnKeyKnown  bool           `json:"own_key_known"`
	KeyError     *string        `json:"key_error"`
	LastSuccess  *string        `json:"last_success"`
	Unavailable  bool           `json:"unavailable"`
	Sources      []wireCTSource `json:"sources"`
	Certificates []wireCTCert   `json:"certificates"`
}

// ct is the monitoring's part of the system status. A failure to read it does not fail
// the status; it is shown as unreadable.
func (s *Server) ct(ctx context.Context) wireCT {
	if s.cfg.CT == nil {
		state := s.cfg.CTState
		if state == "" {
			state = ctOff
		}
		return wireCT{State: state, Reason: optional(s.cfg.CTReason), Sources: []wireCTSource{}, Certificates: []wireCTCert{}}
	}
	st, err := s.cfg.CT.Status(ctx)
	if err != nil {
		s.cfg.Logger.Error("certificate monitoring status unreadable", "error", err)
		return wireCT{State: ctUnreadable, Sources: []wireCTSource{}, Certificates: []wireCTCert{}}
	}
	out := wireCT{State: ctActive, Name: &st.Name, Since: formatTime(st.Since), OwnKeyKnown: st.KeyKnown,
		KeyError: optional(st.KeyError), LastSuccess: formatTime(st.LastSuccess), Unavailable: st.Unavailable,
		Sources: []wireCTSource{}, Certificates: []wireCTCert{}}
	for _, src := range st.Sources {
		out.Sources = append(out.Sources, wireCTSource{Name: src.Name, LastSuccess: formatTime(src.LastSuccess),
			LastFailure: formatTime(src.LastFailure), Failure: optional(src.Failure)})
	}
	for _, c := range st.Certs {
		out.Certificates = append(out.Certificates, s.ctCert(ctx, c, st.Pending))
	}
	return out
}

func (s *Server) ctCert(ctx context.Context, c ctwatch.CertStatus, pending []ctwatch.Pending) wireCTCert {
	w := wireCTCert{Digest: c.Digest.String(), Names: slices.Clone(c.Names), Issuer: c.Issuer, Serial: strings.ToUpper(c.Serial),
		NotBefore: *formatTime(c.NotBefore), NotAfter: *formatTime(c.NotAfter), FirstSeen: *formatTime(c.FirstSeen),
		Result: c.Result.String()}
	if w.Names == nil {
		w.Names = []string{}
	}
	if c.Ack != nil {
		w.Ack = &wireCTAck{By: c.Ack.By, ByName: s.users.name(ctx, c.Ack.By), Via: string(c.Ack.Via), At: *formatTime(c.Ack.At)}
	}
	for _, p := range pending {
		if p.Digest == c.Digest {
			answerers := slices.Clone(p.UIUsers)
			if answerers == nil {
				answerers = []string{}
			}
			w.Pending = &wireCTPending{ID: p.ID, Expires: *formatTime(p.Expires), RequestedBy: p.RequestedBy,
				RequestedByName: s.users.name(ctx, p.RequestedBy), Answerers: answerers}
		}
	}
	return w
}

// ctLimit bounds requests and answers per person, as for approvals.
const ctLimit = answerLimit

// acknowledgeCertificate asks the approvers to confirm that a certificate is the
// household's ("This is mine").
func (s *Server) acknowledgeCertificate(r *request) (any, error) {
	if s.cfg.CT == nil {
		return nil, fail(codeNotFound)
	}
	var in struct {
		Digest string `json:"digest"`
	}
	if err := r.decode(&in); err != nil {
		return nil, err
	}
	digest, err := ctwatch.ParseDigest(in.Digest)
	if err != nil {
		return nil, failField(codeInvalidInput, "/digest")
	}
	if ok, wait := s.limits.allow("ct:"+r.user, ctLimit, answerPeriod); !ok {
		return nil, failRetry(codeRateLimited, wait)
	}
	p, err := s.cfg.CT.RequestAck(r.Context(), digest, r.user)
	switch {
	case errors.Is(err, ctwatch.ErrNotAlarming):
		return nil, fail(codeNotFound)
	case errors.Is(err, ctwatch.ErrNoApprover):
		return nil, fail(codeNoApprovers)
	case err != nil:
		return nil, err
	}
	s.cfg.Logger.Info("certificate acknowledgement asked for in the UI", "by", r.user, "digest", digest.String())
	answerers := slices.Clone(p.UIUsers)
	if answerers == nil {
		answerers = []string{}
	}
	return wireCTPending{ID: p.ID, Expires: *formatTime(p.Expires), RequestedBy: p.RequestedBy,
		RequestedByName: s.users.name(r.Context(), p.RequestedBy), Answerers: answerers}, nil
}

// answerCertificate takes an approver's answer in the UI.
func (s *Server) answerCertificate(r *request) (any, error) {
	if s.cfg.CT == nil {
		return nil, fail(codeNotFound)
	}
	id := r.PathValue("id")
	if !ctConfirmationID.MatchString(id) {
		return nil, fail(codeNotFound)
	}
	var in struct {
		Approve *bool `json:"approve"`
	}
	if err := r.decode(&in); err != nil {
		return nil, err
	}
	if in.Approve == nil {
		return nil, failField(codeInvalidInput, "/approve")
	}
	if ok, wait := s.limits.allow("ct:"+r.user, ctLimit, answerPeriod); !ok {
		return nil, failRetry(codeRateLimited, wait)
	}
	err := s.cfg.CT.AnswerAck(r.Context(), id, r.user, *in.Approve)
	switch {
	case errors.Is(err, ctwatch.ErrNotPending), errors.Is(err, ctwatch.ErrNotApprover), errors.Is(err, ctwatch.ErrChannel):
		return nil, fail(codeNotFound)
	case err != nil:
		return nil, err
	}
	return s.ct(r.Context()), nil
}

// UserName is the display name of a Home Assistant user, "" if unknown.
func (s *Server) UserName(ctx context.Context, id string) string {
	if name := s.users.name(ctx, id); name != nil {
		return *name
	}
	return ""
}
