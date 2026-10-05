// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestUISessionLifetime(t *testing.T) {
	c := &clock{t: testStart}
	s := newUISessions(c.Now)
	token, err := s.create(adminID)
	if err != nil {
		t.Fatal(err)
	}
	if len(token) < 43 || strings.ContainsAny(token, "+/=") {
		t.Errorf("token %q is not 256 bits in base64url", token)
	}
	if user, ok := s.lookup(token); !ok || user != adminID {
		t.Fatalf("lookup = %q, %v", user, ok)
	}
	// Idle: every request moves the end of the idle time.
	for range 5 {
		c.Add(uiIdle - time.Minute)
		if _, ok := s.lookup(token); !ok {
			t.Fatal("session ended although it was used within the idle time")
		}
	}
	c.Add(uiIdle)
	if _, ok := s.lookup(token); ok {
		t.Error("session lives on after the idle time")
	}

	// Absolute: ends after 12 hours however busy it is.
	token, _ = s.create(adminID)
	for elapsed := time.Duration(0); elapsed < uiMaxAge-time.Minute; elapsed += 20 * time.Minute {
		c.Add(20 * time.Minute)
		if _, ok := s.lookup(token); !ok && elapsed+20*time.Minute < uiMaxAge {
			t.Fatalf("session ended after %v", elapsed+20*time.Minute)
		}
	}
	c.Add(20 * time.Minute)
	if _, ok := s.lookup(token); ok {
		t.Error("session lives on after 12 hours")
	}
}

func TestUISessionTokensAreCheckedExactly(t *testing.T) {
	c := &clock{t: testStart}
	s := newUISessions(c.Now)
	token, _ := s.create(adminID)
	for _, guess := range []string{"", "x", token[:len(token)-1], token + "A", strings.ToUpper(token), " " + token} {
		if _, ok := s.lookup(guess); ok && guess != token {
			t.Errorf("lookup(%q) succeeded", guess)
		}
	}
	s.end(token)
	if _, ok := s.lookup(token); ok {
		t.Error("session lives on after the sign-out")
	}
	s.end("unknown") // no panic
}

func TestUISessionLimits(t *testing.T) {
	c := &clock{t: testStart}
	s := newUISessions(c.Now)
	var tokens []string
	for range uiPerUser {
		token, err := s.create(adminID)
		if err != nil {
			t.Fatal(err)
		}
		tokens = append(tokens, token)
		c.Add(time.Second)
	}
	sixth, err := s.create(adminID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.lookup(tokens[0]); ok {
		t.Error("the oldest session of the user did not end with the sixth")
	}
	for _, token := range append(tokens[1:], sixth) {
		if _, ok := s.lookup(token); !ok {
			t.Error("a newer session ended")
		}
	}

	// Overall: full means no new session, until sessions expire.
	full := newUISessions(c.Now)
	for i := range uiTotal {
		if _, err := full.create(strings.Repeat("u", 10) + string(rune('a'+i%26)) + strings.Repeat("x", i/26)); err != nil {
			t.Fatalf("session %d: %v", i, err)
		}
	}
	if _, err := full.create(annaID); !errors.Is(err, errUISessionsFull) {
		t.Errorf("err = %v, want errUISessionsFull", err)
	}
	c.Add(uiIdle + time.Second)
	if _, err := full.create(annaID); err != nil {
		t.Errorf("no session after the others expired: %v", err)
	}
}

func TestSignInStates(t *testing.T) {
	c := &clock{t: testStart}
	s := newUISessions(c.Now)
	state := s.startSignIn("192.0.2.10")
	if s.finishSignIn(state, state+"x") || s.finishSignIn("", "") {
		t.Error("a sign-in finished with a state that does not match the cookie")
	}
	if !s.finishSignIn(state, state) {
		t.Fatal("matching state refused")
	}
	if s.finishSignIn(state, state) {
		t.Error("a state was used twice")
	}

	old := s.startSignIn("192.0.2.10")
	c.Add(signInTTL + time.Second)
	if s.finishSignIn(old, old) {
		t.Error("an expired sign-in finished")
	}

	// Per address, the oldest gives way; other addresses keep theirs.
	other := s.startSignIn("192.0.2.21")
	var states []string
	for range signInPerAddr + 1 {
		states = append(states, s.startSignIn("192.0.2.20"))
		c.Add(time.Second)
	}
	if s.finishSignIn(states[0], states[0]) {
		t.Error("the oldest sign-in of the address did not give way")
	}
	if !s.finishSignIn(states[1], states[1]) || !s.finishSignIn(other, other) {
		t.Error("a newer sign-in or one of another address is gone")
	}

	// Overall, the oldest gives way too: the table never grows past signInTotal.
	for i := range signInTotal * 2 {
		s.startSignIn("198.51.100." + strconv.Itoa(i))
	}
	if n := len(s.signIns); n > signInTotal {
		t.Errorf("%d sign-ins in progress, at most %d", n, signInTotal)
	}
}
