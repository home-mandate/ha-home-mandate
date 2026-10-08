// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/approval"
	"github.com/home-mandate/ha-home-mandate/internal/ha"
	"github.com/home-mandate/ha-home-mandate/internal/i18n"
	"github.com/home-mandate/ha-home-mandate/internal/untrusted"
)

const (
	// Test notifications: per approver one in 30 seconds, ten a minute overall.
	testLimitPerApprover = 1
	testPeriodApprover   = 30 * time.Second
	testLimitOverall     = 10
	testPeriodOverall    = time.Minute
	candidatesTimeout    = 10 * time.Second
)

type wireApproverDevice struct {
	Service  string `json:"service"`
	Critical bool   `json:"critical"`
}

type wireReach struct {
	Normal   string `json:"normal"`
	Critical string `json:"critical"`
}

type wireApprover struct {
	UserID     string               `json:"user_id"`
	Name       string               `json:"name"`
	Devices    []wireApproverDevice `json:"devices"`
	UI         bool                 `json:"ui"`
	UICritical bool                 `json:"ui_critical"`
	Language   *string              `json:"language"`
	Reach      wireReach            `json:"reach"`
}

type wirePerson struct {
	UserID  string `json:"user_id"`
	Name    string `json:"name"`
	IsAdmin bool   `json:"is_admin"`
}

type wireCandidateDevice struct {
	Service         string  `json:"service"`
	Name            string  `json:"name"`
	SuggestCritical bool    `json:"suggest_critical"`
	OwnerUserID     *string `json:"owner_user_id"`
}

type wireCandidates struct {
	People  []wirePerson          `json:"people"`
	Devices []wireCandidateDevice `json:"devices"`
}

type wireApproverList struct {
	Approvers  []wireApprover `json:"approvers"`
	Candidates wireCandidates `json:"candidates"`
	// Version of the approvers; a change names the version it is based on (the first
	// change wins, a later one on an older version is a conflict).
	Version string `json:"version"`
}

// approversVersionPattern is the form of an approvers version.
var approversVersionPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// candidates are the people (person.* with a Home Assistant user, not Home-Mandate's own)
// and the devices with the Companion App. A device's notify service is
// mobile_app_<slug of its registered name>; its owner is the user of the person whose
// device tracker it has; critical requests are suggested only for iOS (decision S11).
func (s *Server) candidates(ctx context.Context) (wireCandidates, error) {
	ctx, cancel := context.WithTimeout(ctx, candidatesTimeout)
	defer cancel()
	out := wireCandidates{}
	users, err := s.users.all(ctx)
	if err != nil {
		return wireCandidates{}, err
	}
	states, err := s.cfg.HA.GetStates(ctx)
	if err != nil {
		return wireCandidates{}, errors.Join(errHAUnavailable, err)
	}
	services, err := s.cfg.HA.NotifyServices(ctx)
	if err != nil {
		return wireCandidates{}, errors.Join(errHAUnavailable, err)
	}
	entities, err := s.cfg.HA.ListEntities(ctx)
	if err != nil {
		return wireCandidates{}, errors.Join(errHAUnavailable, err)
	}
	devices, err := s.cfg.HA.ListDevices(ctx)
	if err != nil {
		return wireCandidates{}, errors.Join(errHAUnavailable, err)
	}
	people, owner := s.people(states, entities, users)
	out.People = people
	out.Devices = appDevices(services, devices, owner)
	return out, nil
}

// people are the persons with an active Home Assistant user (not Home-Mandate's own, no
// system user), sorted by user ID, and the owner of each device they track.
func (s *Server) people(states []ha.State, entities []ha.EntityEntry, users map[string]ha.AuthUser) ([]wirePerson, map[string]string) {
	self := s.cfg.Status().ServiceUser
	deviceOfEntity := map[string]string{}
	for _, e := range entities {
		deviceOfEntity[e.EntityID] = e.DeviceID
	}
	people, owner := []wirePerson{}, map[string]string{} // device ID → user ID
	for _, st := range states {
		if !strings.HasPrefix(st.EntityID, "person.") {
			continue
		}
		user, _ := st.Attributes["user_id"].(string)
		u, ok := users[user]
		if !ok || user == self || u.SystemGenerated || !u.IsActive {
			continue
		}
		name, _ := st.Attributes["friendly_name"].(string)
		if name == "" {
			name = u.Name
		}
		people = append(people, wirePerson{UserID: user, Name: name, IsAdmin: u.IsAdmin()})
		trackers, _ := st.Attributes["device_trackers"].([]any)
		for _, t := range trackers {
			if id, ok := t.(string); ok && deviceOfEntity[id] != "" {
				owner[deviceOfEntity[id]] = user
			}
		}
	}
	slices.SortFunc(people, func(a, b wirePerson) int { return strings.Compare(a.UserID, b.UserID) })
	return people, owner
}

// appDevices are the notify services of the Companion App with the device each belongs
// to (mobile_app_<slug of its registered name>); unknown ones keep the service as name.
func appDevices(services []string, devices []ha.Device, owner map[string]string) []wireCandidateDevice {
	byService := map[string]ha.Device{}
	for _, d := range devices {
		if d.Name != "" {
			byService["mobile_app_"+slugify(d.Name)] = d
		}
	}
	out := []wireCandidateDevice{}
	for _, service := range services {
		c := wireCandidateDevice{Service: service, Name: service}
		if d, ok := byService[service]; ok {
			c.Name = d.Name
			if d.NameByUser != "" {
				c.Name = d.NameByUser
			}
			c.SuggestCritical = isIOS(d)
			c.OwnerUserID = optional(owner[d.ID])
		}
		out = append(out, c)
	}
	return out
}

// isIOS tells an iPhone or iPad: their app asks for unlocking before a notification
// button counts. The Mac app, Android and anything unknown do not.
func isIOS(d ha.Device) bool {
	return d.Manufacturer == "Apple" && (strings.HasPrefix(d.Model, "iPhone") || strings.HasPrefix(d.Model, "iPad"))
}

// slugify is Home Assistant's slugify with "_" for the notify service of a Companion
// App: letters without accents in lower case, everything else joined into "_".
func slugify(name string) string {
	var b strings.Builder
	sep := false
	for _, r := range untrusted.Fold(name) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			if sep && b.Len() > 0 {
				b.WriteByte('_')
			}
			sep = false
			b.WriteRune(r)
			continue
		}
		sep = true
	}
	return b.String()
}

func (s *Server) approverList(ctx context.Context) (wireApproverList, error) {
	cand, err := s.candidates(ctx)
	if err != nil {
		return wireApproverList{}, err
	}
	list, err := s.cfg.Approvers.List(ctx)
	if err != nil {
		return wireApproverList{}, err
	}
	version, err := s.cfg.Approvers.Version(ctx)
	if err != nil {
		return wireApproverList{}, err
	}
	out := wireApproverList{Approvers: make([]wireApprover, 0, len(list)), Candidates: cand, Version: version}
	for _, ap := range list {
		admin, err := s.users.IsAdmin(ctx, ap.UserID)
		if err != nil {
			return wireApproverList{}, err
		}
		normal, critical := ap.ReachBy(admin)
		w := wireApprover{UserID: ap.UserID, Name: ap.UserID, Devices: make([]wireApproverDevice, 0, len(ap.Devices)), UI: ap.UI,
			UICritical: ap.UICritical, Language: optional(ap.Language), Reach: wireReach{Normal: normal, Critical: critical}}
		if i := slices.IndexFunc(cand.People, func(p wirePerson) bool { return p.UserID == ap.UserID }); i >= 0 {
			w.Name = cand.People[i].Name
		} else if n := s.users.name(ctx, ap.UserID); n != nil {
			w.Name = *n
		}
		for _, d := range ap.Devices {
			w.Devices = append(w.Devices, wireApproverDevice{Service: d.Service, Critical: d.Critical})
		}
		out.Approvers = append(out.Approvers, w)
	}
	return out, nil
}

// errHAUnavailable means Home Assistant did not answer what the API asked.
var errHAUnavailable = errors.New("api: home assistant unavailable")

// unavailableOr maps a failure to ask Home Assistant to unavailable.
func unavailableOr(err error) error {
	if errors.Is(err, errUsersUnavailable) || errors.Is(err, errHAUnavailable) || errors.Is(err, ha.ErrDisconnected) ||
		errors.Is(err, context.DeadlineExceeded) {
		return fail(codeUnavailable)
	}
	return err
}

func (s *Server) getApprovers(r *request) (any, error) {
	out, err := s.approverList(r.Context())
	if err != nil {
		return nil, unavailableOr(err)
	}
	return out, nil
}

func approverID(r *request) (string, error) {
	id := r.PathValue("id")
	if !userIDPattern.MatchString(id) {
		return "", fail(codeNotFound)
	}
	return id, nil
}

// putApprover saves a person's channels (decision F2). The rules of internal/approval
// apply, and the server checks against Home Assistant now: the person exists, each
// device has the Companion App, and the UI channel is only for an administrator (fail
// closed). Errors name only the field.
func (s *Server) putApprover(r *request) (any, error) {
	id := r.PathValue("id")
	if !userIDPattern.MatchString(id) {
		return nil, failField(codeInvalidInput, "/user_id")
	}
	var in struct {
		Devices     []wireApproverDevice `json:"devices"`
		UI          bool                 `json:"ui"`
		UICritical  bool                 `json:"ui_critical"`
		Language    *string              `json:"language"`
		BaseVersion string               `json:"base_version"`
	}
	if err := r.decode(&in); err != nil {
		return nil, err
	}
	if !approversVersionPattern.MatchString(in.BaseVersion) {
		return nil, failField(codeInvalidInput, "/base_version")
	}
	cand, err := s.candidates(r.Context())
	if err != nil {
		return nil, unavailableOr(err)
	}
	person := slices.IndexFunc(cand.People, func(p wirePerson) bool { return p.UserID == id })
	if person < 0 || id == s.cfg.Status().ServiceUser {
		return nil, failField(codeInvalidInput, "/user_id")
	}
	ap, err := approverOf(id, in.Devices, in.UI, in.UICritical, in.Language, cand.Devices)
	if err != nil {
		return nil, err
	}
	admin, err := s.users.IsAdmin(r.Context(), id)
	if err != nil {
		return nil, fail(codeUnavailable)
	}
	if err := approval.CheckUI(ap, admin); err != nil {
		return nil, failField(codeInvalidInput, "/ui")
	}
	if err := s.cfg.Approvers.PutIf(r.Context(), ap, in.BaseVersion, s.actor(r)); err != nil {
		switch {
		case errors.Is(err, approval.ErrApproversChanged):
			return nil, fail(codeConflict)
		case errors.Is(err, approval.ErrInvalidApprover):
			return nil, failField(codeInvalidInput, "/devices")
		}
		return nil, err
	}
	s.publish(event{Type: "approvers.changed"})
	s.publishSystem(r.Context())
	out, err := s.approverList(r.Context())
	if err != nil {
		return nil, unavailableOr(err)
	}
	return out, nil
}

// approverOf applies the rules of saving (decision F2) that need no Home Assistant:
// known devices, at most five, a channel, ui_critical only with ui, a UI language.
func approverOf(id string, devices []wireApproverDevice, ui, uiCritical bool, lang *string, known []wireCandidateDevice) (approval.Approver, error) {
	ap := approval.Approver{UserID: id, UI: ui, UICritical: uiCritical}
	if lang != nil {
		if *lang != "de" && *lang != "en" {
			return approval.Approver{}, failField(codeInvalidInput, "/language")
		}
		ap.Language = *lang
	}
	for _, d := range devices {
		if !slices.ContainsFunc(known, func(c wireCandidateDevice) bool { return c.Service == d.Service }) {
			return approval.Approver{}, failField(codeInvalidInput, "/devices")
		}
		ap.Devices = append(ap.Devices, approval.Device{Service: d.Service, Critical: d.Critical})
	}
	switch {
	case len(ap.Devices) > 5, len(ap.Devices) == 0 && !ap.UI:
		return approval.Approver{}, failField(codeInvalidInput, "/devices")
	case ap.UICritical && !ap.UI:
		return approval.Approver{}, failField(codeInvalidInput, "/ui_critical")
	}
	return ap, nil
}

// deleteApprover removes a person and takes them out of open requests: their answer
// then counts as anyone else's.
func (s *Server) deleteApprover(r *request) (any, error) {
	id, err := approverID(r)
	if err != nil {
		return nil, err
	}
	base := r.URL.Query()["base_version"]
	if len(base) != 1 || !approversVersionPattern.MatchString(base[0]) {
		return nil, failField(codeInvalidInput, "/base_version")
	}
	switch err := s.cfg.Approvers.RemoveIf(r.Context(), id, base[0], s.actor(r)); {
	case errors.Is(err, approval.ErrApproverNotFound):
		return nil, fail(codeNotFound)
	case errors.Is(err, approval.ErrApproversChanged):
		return nil, fail(codeConflict)
	case err != nil:
		return nil, err
	}
	s.cfg.Approvals.Withdraw(id)
	s.publish(event{Type: "approvers.changed"})
	s.publishSystem(r.Context())
	return nil, nil
}

// testApprover sends a neutral test notification to the person's stored devices: no
// buttons, no nonce, nothing of a request.
func (s *Server) testApprover(r *request) (any, error) {
	id, err := approverID(r)
	if err != nil {
		return nil, err
	}
	list, err := s.cfg.Approvers.List(r.Context())
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(list, func(a approval.Approver) bool { return a.UserID == id })
	if i < 0 {
		return nil, fail(codeNotFound)
	}
	if ok, wait := s.limits.allow("test:"+id, testLimitPerApprover, testPeriodApprover); !ok {
		return nil, failRetry(codeRateLimited, wait)
	}
	if ok, wait := s.limits.allow("test", testLimitOverall, testPeriodOverall); !ok {
		return nil, failRetry(codeRateLimited, wait)
	}
	lang, ok := i18n.Parse(list[i].Language)
	if !ok {
		lang = i18n.Pick(s.cfg.Status().Language)
	}
	n := ha.Notification{Title: i18n.T(lang, i18n.ApprovalTestTitle, nil), Message: i18n.T(lang, i18n.ApprovalTestMessage, nil)}
	sent := 0
	for _, d := range list[i].Devices {
		if err := s.cfg.HA.Notify(r.Context(), d.Service, n); err != nil {
			s.cfg.Logger.Warn("test notification not delivered", "approver", id, "notify_service", d.Service, "error", err)
			continue
		}
		sent++
	}
	if sent == 0 && len(list[i].Devices) > 0 {
		return nil, fail(codeUnavailable)
	}
	return nil, nil
}
