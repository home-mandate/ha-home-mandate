// SPDX-License-Identifier: AGPL-3.0-or-later

package approval

import (
	"context"
	"errors"
	"slices"
)

// ReachUnknown means it could not be checked how a person is reached (Home Assistant did
// not answer): never counted as reachable.
const ReachUnknown = "unknown"

// Whether a kind of request reaches anyone (the admission's warning).
const (
	CoverageNotNeeded = "not_needed" // the mandate asks nobody for it
	CoverageReachable = "reachable"  // every list that may be asked has someone reachable
	CoverageNobody    = "nobody"     // some list has nobody reachable: those requests are denied after the timeout
	CoverageUnknown   = "unknown"    // could not be checked; never counted as reachable
)

// Expected is who a mandate may ask: everyone named, and the approver lists that may get
// ordinary and critical requests (admission.Approvers).
type Expected struct {
	People           []string
	Normal, Critical [][]string
}

// PersonReach is how one person is reached for ordinary and critical requests: ReachPush,
// ReachUI, ReachNone or ReachUnknown. Service marks Home-Mandate's own Home Assistant
// user, who is never asked. Name is the person's name in Home Assistant, which the
// caller fills in ("" if unknown).
type PersonReach struct {
	UserID           string
	Name             string
	Normal, Critical string
	Service          bool
}

// ReachPreview is what the human who admits an agent is shown.
type ReachPreview struct {
	People           []PersonReach
	Normal, Critical string // Coverage…
}

// ReachOf tells for each person how they are reached now and whether the lists of e
// reach anyone. Someone who is set up but whose administrator role could not be checked
// is ReachUnknown (fail closed: never reachable); someone not set up and the service
// user are ReachNone.
func (a *Approvers) ReachOf(ctx context.Context, e Expected, serviceUser string, isAdmin func(context.Context, string) (bool, error)) (ReachPreview, error) {
	all, err := a.List(ctx)
	if err != nil {
		return ReachPreview{}, err
	}
	known := map[string]PersonReach{}
	reach := func(user string) PersonReach {
		if r, ok := known[user]; ok {
			return r
		}
		r := PersonReach{UserID: user, Normal: ReachNone, Critical: ReachNone, Service: user != "" && user == serviceUser}
		i := slices.IndexFunc(all, func(ap Approver) bool { return ap.UserID == user })
		if i >= 0 && !r.Service {
			if admin, err := checkAdmin(ctx, isAdmin, user); err != nil {
				r.Normal, r.Critical = ReachUnknown, ReachUnknown
			} else {
				r.Normal, r.Critical = all[i].ReachBy(admin)
			}
		}
		known[user] = r
		return r
	}
	out := ReachPreview{People: make([]PersonReach, 0, len(e.People))}
	for _, p := range e.People {
		out.People = append(out.People, reach(p))
	}
	out.Normal = coverage(e.Normal, func(u string) string { return reach(u).Normal })
	out.Critical = coverage(e.Critical, func(u string) string { return reach(u).Critical })
	return out, nil
}

func checkAdmin(ctx context.Context, isAdmin func(context.Context, string) (bool, error), user string) (bool, error) {
	if isAdmin == nil {
		return false, errNoAdminCheck
	}
	return isAdmin(ctx, user)
}

var errNoAdminCheck = errors.New("approval: no administrator check")

// coverage is CoverageNobody if any list has nobody who may be reachable, otherwise
// CoverageUnknown if any list has nobody known to be reachable.
func coverage(lists [][]string, by func(string) string) string {
	if len(lists) == 0 {
		return CoverageNotNeeded
	}
	out := CoverageReachable
	for _, list := range lists {
		state := CoverageNobody
		for _, u := range list {
			switch by(u) {
			case ReachPush, ReachUI:
				state = CoverageReachable
			case ReachUnknown:
				if state == CoverageNobody {
					state = CoverageUnknown
				}
			}
			if state == CoverageReachable {
				break
			}
		}
		switch {
		case state == CoverageNobody:
			return CoverageNobody
		case state == CoverageUnknown:
			out = CoverageUnknown
		}
	}
	return out
}
