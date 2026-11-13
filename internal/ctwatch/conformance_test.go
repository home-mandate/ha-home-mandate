// SPDX-License-Identifier: AGPL-3.0-or-later

package ctwatch

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
	"time"
)

// testdata/ct-v0.json is a pinned copy of relay/conformance/ct-v0.json of the Relay
// Specification, commit 2cc1850 of home-mandate/spec. It is replaced by relay.FS() once a
// release of the spec module carries the relay cases; until then a change of the copy
// must be deliberate, which the digest below enforces.
const (
	ctCasesFile   = "testdata/ct-v0.json"
	ctCasesSHA256 = "6a04817af86ad4fb3f10a5c22aa8c2951844447c7c0a732bb528f268842c0870"
)

type ctCase struct {
	ID          string   `json:"id"`
	PublicNames []string `json:"public_names"`
	Pairing     string   `json:"pairing"`
	Now         string   `json:"now"`
	ServerKeys  struct {
		Current string `json:"current"`
		Earlier []struct {
			PublicKey string `json:"public_key"`
			Replaced  string `json:"replaced"`
		} `json:"earlier"`
	} `json:"server_keys"`
	Acknowledged []string `json:"acknowledged"`
	Certificates []struct {
		ID        string   `json:"id"`
		Digest    string   `json:"digest"`
		Names     []string `json:"names"`
		NotBefore string   `json:"not_before"`
		NotAfter  string   `json:"not_after"`
		PublicKey string   `json:"public_key"`
		Logged    string   `json:"logged"`
	} `json:"certificates"`
	Expected []struct {
		ID     string `json:"id"`
		Result string `json:"result"`
	} `json:"expected"`
	Why string `json:"why"`
}

func loadCases(t *testing.T) []ctCase {
	t.Helper()
	data, err := os.ReadFile(ctCasesFile)
	if err != nil {
		t.Fatal(err)
	}
	if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != ctCasesSHA256 {
		t.Fatalf("%s changed: digest %x, pinned %s; update the pinned commit and digest together", ctCasesFile, sum, ctCasesSHA256)
	}
	var doc struct {
		Cases []ctCase `json:"cases"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Cases) == 0 {
		t.Fatal("no cases")
	}
	return doc.Cases
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("time %q: %v", s, err)
	}
	return v
}

func mustDigest(t *testing.T, s string) Digest {
	t.Helper()
	d, err := ParseDigest(s)
	if err != nil {
		t.Fatalf("digest %q: %v", s, err)
	}
	return d
}

// TestRelayConformanceCT runs every case of RELAY-v0 section 6.7. The pairing of a case
// stands for the first start of monitoring of the name, which takes its place here.
func TestRelayConformanceCT(t *testing.T) {
	for _, c := range loadCases(t) {
		t.Run(c.ID, func(t *testing.T) {
			s := Situation{PublicNames: c.PublicNames, Since: mustTime(t, c.Pairing), Now: mustTime(t, c.Now),
				Keys:         Keys{Current: mustDigest(t, c.ServerKeys.Current), HasCurrent: true},
				Acknowledged: map[Digest]bool{}}
			for _, e := range c.ServerKeys.Earlier {
				s.Keys.Earlier = append(s.Keys.Earlier, EarlierKey{PublicKey: mustDigest(t, e.PublicKey), Replaced: mustTime(t, e.Replaced)})
			}
			for _, a := range c.Acknowledged {
				s.Acknowledged[mustDigest(t, a)] = true
			}
			got := map[string]string{}
			for _, x := range c.Certificates {
				cert := Cert{Digest: mustDigest(t, x.Digest), Names: x.Names, NotBefore: mustTime(t, x.NotBefore),
					NotAfter: mustTime(t, x.NotAfter), PublicKey: mustDigest(t, x.PublicKey), Logged: mustTime(t, x.Logged)}
				got[x.ID] = Decide(s, cert).String()
			}
			if len(got) != len(c.Expected) {
				t.Fatalf("%d results for %d expectations (%s)", len(got), len(c.Expected), c.Why)
			}
			for _, e := range c.Expected {
				if got[e.ID] != e.Result {
					t.Errorf("certificate %s: %s, want %s (%s)", e.ID, got[e.ID], e.Result, c.Why)
				}
			}
		})
	}
}
