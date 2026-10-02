// SPDX-License-Identifier: AGPL-3.0-or-later

// Package i18n holds the texts the server generates for humans (approval notifications,
// sign-in and pairing pages, error messages) in German and English. The catalogs follow
// the conventions of web/messages: flat JSON, snake_case keys, {name} placeholders.
// Identifiers from the specification (categories, actions, reason codes) stay English;
// only their display names are translated.
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Lang is a supported language.
type Lang string

// Supported languages; Default is the fallback.
const (
	DE      Lang = "de"
	EN      Lang = "en"
	Default      = EN
)

// Supported lists all languages with a complete catalog.
var Supported = []Lang{DE, EN}

// Key identifies a message.
type Key string

// Messages. Every key is in Keys and in every catalog (checked by the tests).
const (
	ApprovalTitle          Key = "approval_title"
	ApprovalMessage        Key = "approval_message"
	ApprovalReason         Key = "approval_reason"
	ApprovalParams         Key = "approval_params"
	ApprovalNoAnswer       Key = "approval_no_answer"
	ApprovalApprove        Key = "approval_approve"
	ApprovalDeny           Key = "approval_deny"
	ApprovalInvalidTitle   Key = "approval_invalid_title"
	ApprovalInvalidMessage Key = "approval_invalid_message"

	PageErrorTitle         Key = "page_error_title"
	PageInvalidRequest     Key = "page_invalid_request"
	PageInvalidClient      Key = "page_invalid_client"
	PageSessionExpired     Key = "page_session_expired"
	PageSignInFailed       Key = "page_signin_failed"
	PageNotAdmin           Key = "page_not_admin"
	PageBusy               Key = "page_busy"
	PageSignedInAs         Key = "page_signed_in_as"
	PageConsentTitle       Key = "page_consent_title"
	PageConsentClaimed     Key = "page_consent_claimed"
	PageConsentVerified    Key = "page_consent_verified"
	PageConsentUnverified  Key = "page_consent_unverified"
	PageConsentReturn      Key = "page_consent_return"
	PageConsentName        Key = "page_consent_name"
	PageConsentTemplate    Key = "page_consent_template"
	PageConsentApprove     Key = "page_consent_approve"
	PageConsentDeny        Key = "page_consent_deny"
	PageConsentNoTemplates Key = "page_consent_no_templates"
	PageConsentInvalid     Key = "page_consent_invalid"
	PageDenied             Key = "page_denied"
	PageAdmitted           Key = "page_admitted"
	PagePairTitle          Key = "page_pair_title"
	PagePairIntro          Key = "page_pair_intro"
	PagePairCode           Key = "page_pair_code"
	PagePairSubmit         Key = "page_pair_submit"
	PagePairInvalid        Key = "page_pair_invalid"
	PagePairLocked         Key = "page_pair_locked"
)

// actions are the vocabulary actions of SPEC-v0 section 5; their display names have the
// keys action_<action>.
var actions = []string{"read", "turn_on", "turn_off", "set", "set_temperature", "set_mode", "open", "close", "stop",
	"set_position", "lock", "unlock", "arm", "disarm", "snapshot", "play", "pause", "set_volume", "activate", "run"}

// Keys lists every message key.
var Keys = append([]Key{
	ApprovalTitle, ApprovalMessage, ApprovalReason, ApprovalParams, ApprovalNoAnswer, ApprovalApprove, ApprovalDeny,
	ApprovalInvalidTitle, ApprovalInvalidMessage,
	PageErrorTitle, PageInvalidRequest, PageInvalidClient, PageSessionExpired, PageSignInFailed, PageNotAdmin, PageBusy, PageSignedInAs, PageConsentTitle, PageConsentClaimed, PageConsentVerified, PageConsentUnverified, PageConsentReturn, PageConsentName, PageConsentTemplate, PageConsentApprove, PageConsentDeny, PageConsentNoTemplates, PageConsentInvalid, PageDenied, PageAdmitted, PagePairTitle, PagePairIntro, PagePairCode, PagePairSubmit, PagePairInvalid, PagePairLocked,
}, actionKeys()...)

func actionKeys() []Key {
	keys := make([]Key, len(actions))
	for i, a := range actions {
		keys[i] = Key("action_" + a)
	}
	return keys
}

//go:embed messages/*.json
var files embed.FS

var catalogs = loadCatalogs()

func loadCatalogs() map[Lang]map[Key]string {
	out := make(map[Lang]map[Key]string, len(Supported))
	for _, lang := range Supported {
		data, err := files.ReadFile("messages/" + string(lang) + ".json")
		if err != nil {
			panic(fmt.Sprintf("i18n: catalog %s: %v", lang, err))
		}
		var m map[Key]string
		if err := json.Unmarshal(data, &m); err != nil {
			panic(fmt.Sprintf("i18n: catalog %s: %v", lang, err))
		}
		out[lang] = m
	}
	return out
}

// Args are the placeholder values of a message, inserted literally.
type Args map[string]string

// T returns the message key in lang with args inserted. An unsupported language falls
// back to Default, an unknown key to the key itself; a missing argument leaves its
// placeholder visible. Arguments are never expanded again, so an argument cannot inject
// another placeholder's value.
func T(lang Lang, key Key, args Args) string {
	catalog, ok := catalogs[lang]
	if !ok {
		catalog = catalogs[Default]
	}
	msg, ok := catalog[key]
	if !ok {
		return string(key)
	}
	if len(args) == 0 {
		return msg
	}
	pairs := make([]string, 0, 2*len(args))
	for name, value := range args {
		pairs = append(pairs, "{"+name+"}", value)
	}
	return strings.NewReplacer(pairs...).Replace(msg)
}

// ActionName returns the display name of a vocabulary action, or the action itself if
// it is unknown.
func ActionName(lang Lang, action string) string {
	if !slices.Contains(actions, action) {
		return action
	}
	return T(lang, Key("action_"+action), nil)
}

// Parse maps a language tag such as "de-DE" or "en_US" to a supported language.
func Parse(tag string) (Lang, bool) {
	primary, _, _ := strings.Cut(strings.ReplaceAll(tag, "_", "-"), "-")
	lang := Lang(strings.ToLower(primary))
	if slices.Contains(Supported, lang) {
		return lang, true
	}
	return Default, false
}

// Pick returns the first supported language among tags, in order of preference, or
// Default.
func Pick(tags ...string) Lang {
	for _, tag := range tags {
		if lang, ok := Parse(tag); ok {
			return lang
		}
	}
	return Default
}

var placeholderPattern = regexp.MustCompile(`\{[a-z_]+\}`)

// placeholders returns the sorted placeholders of a message.
func placeholders(msg string) []string {
	found := placeholderPattern.FindAllString(msg, -1)
	slices.Sort(found)
	return slices.Compact(found)
}
