// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"slices"
)

// languageKeyPrefix is the settings key of a user's UI language.
const languageKeyPrefix = "ui_language:"

var uiLanguages = []string{"de", "en"}

type wireUser struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type wireHousehold struct {
	TimeZone   string            `json:"time_zone"`
	Language   string            `json:"language"`
	UnitSystem map[string]string `json:"unit_system"`
}

type wireSession struct {
	User      wireUser      `json:"user"`
	CSRFToken string        `json:"csrf_token"`
	Language  *string       `json:"language"`
	Household wireHousehold `json:"household"`
	// SignOut: signed in through Home-Mandate (direct mode), so the UI offers to sign out.
	SignOut bool `json:"sign_out"`
}

// unitKeys are the units the UI formats with (types.ts UnitSystem).
var unitKeys = []string{"temperature", "length", "mass", "volume", "pressure", "wind_speed"}

func (s *Server) getSession(r *request) (any, error) {
	return s.session(r)
}

func (s *Server) session(r *request) (wireSession, error) {
	st := s.cfg.Status()
	units := make(map[string]string, len(unitKeys))
	for _, k := range unitKeys {
		units[k] = st.Units[k]
	}
	tz := st.TimeZone
	if tz == "" {
		tz = "UTC"
	}
	name := r.user
	if n := s.users.name(r.Context(), r.user); n != nil {
		name = *n
	}
	out := wireSession{User: wireUser{ID: r.user, Name: name}, CSRFToken: s.csrfToken(r.user, s.now()),
		Household: wireHousehold{TimeZone: tz, Language: st.Language, UnitSystem: units}}
	_, out.SignOut = entryOf(r.Request)
	lang, ok, err := s.cfg.Store.Setting(r.Context(), languageKeyPrefix+r.user)
	if err != nil {
		return wireSession{}, err
	}
	if ok && slices.Contains(uiLanguages, lang) {
		out.Language = &lang
	}
	return out, nil
}

func (s *Server) putLanguage(r *request) (any, error) {
	var in struct {
		Language *string `json:"language"`
	}
	if err := r.decode(&in); err != nil {
		return nil, err
	}
	key := languageKeyPrefix + r.user
	switch {
	case in.Language == nil:
		if err := s.cfg.Store.DeleteSetting(r.Context(), key); err != nil {
			return nil, err
		}
	case slices.Contains(uiLanguages, *in.Language):
		if err := s.cfg.Store.SetSetting(r.Context(), key, *in.Language); err != nil {
			return nil, err
		}
	default:
		return nil, failField(codeInvalidInput, "/language")
	}
	return s.session(r)
}
