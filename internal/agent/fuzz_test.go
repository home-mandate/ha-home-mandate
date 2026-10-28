// SPDX-License-Identifier: AGPL-3.0-or-later

package agent_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/home-mandate/ha-home-mandate/internal/agent"
)

// FuzzTokens: the bearer token of every MCP request and the refresh token of the token
// endpoint come from the agent. Only the issued access token authenticates, and only for
// its resource; any other refresh token is an invalid grant and leaves the issued tokens
// as they are. In the inputs, ACCESS and REFRESH stand for the issued tokens, so that the
// fuzzer reaches the lookups behind a well-formed token.
func FuzzTokens(f *testing.F) {
	s, _, _ := newStore(f)
	withClock(s) // stands still: the issued access token does not expire during the run
	a := register(f, s, "Voice assistant")
	pair := issue(f, s, a.ClientID)
	for _, seed := range []string{
		"ACCESS", "REFRESH", "ACCESSx", "ACCESS ", "hma_", "hmr_", "Bearer ACCESS",
		"hma_" + strings.Repeat("A", 43), "hmr_" + strings.Repeat("_", 43), "hma_" + strings.Repeat("=", 44), "",
	} {
		f.Add(seed, resource)
	}
	f.Add("ACCESS", resource+"/other")
	ctx := context.Background()
	f.Fuzz(func(t *testing.T, token, res string) {
		token = strings.NewReplacer("ACCESS", pair.AccessToken, "REFRESH", pair.RefreshToken).Replace(token)
		got, err := s.Authenticate(ctx, token, res)
		valid := token == pair.AccessToken && res == resource
		if valid != (err == nil) || err != nil && !errors.Is(err, agent.ErrUnauthorized) || valid && got.ClientID != a.ClientID {
			t.Fatalf("Authenticate(%q, %q) = %+v, %v", token, res, got, err)
		}
		if token == pair.RefreshToken {
			return // would rotate the issued pair
		}
		if p, err := s.Refresh(ctx, token, res, a.OAuthClient); !errors.Is(err, agent.ErrInvalidGrant) {
			t.Fatalf("Refresh(%q) = %+v, %v", token, p, err)
		}
		if _, err := s.Authenticate(ctx, pair.AccessToken, resource); err != nil {
			t.Fatalf("the issued token stopped working after %q: %v", token, err)
		}
	})
}
