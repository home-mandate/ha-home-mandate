// SPDX-License-Identifier: AGPL-3.0-or-later

package mandate_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"testing"

	"github.com/home-mandate/spec"

	"github.com/home-mandate/ha-home-mandate/internal/mandate"
)

// FuzzPut: a mandate document comes from the UI's editor or an import. Whatever it
// holds, Put stores it as a version of the agent's mandate or refuses it with a defined
// error; a refusal writes nothing to the audit log, and what is stored is what the
// agent's decisions are made with afterwards. The helpers that read stored documents
// (NamedEntities, SameEditable, NewCriticalGrant, ReplaceApprovers) do not panic on it.
// In the inputs, CLIENT stands for the client ID of a registered agent.
func FuzzPut(f *testing.F) {
	e := newEnv(f)
	a := e.agent(f, "Voice assistant")
	ctx := context.Background()
	current := voiceAssistant(f, "CLIENT", nil)
	f.Add(current)
	f.Add(voiceAssistant(f, "CLIENT", func(d map[string]any) {
		d["approval"] = map[string]any{"timeout": "PT2M", "approvers": []any{"$approvers"}}
	}))
	f.Add(voiceAssistant(f, "CLIENT", func(d map[string]any) { d["version"] = 7; d["issuer"] = issuer }))
	f.Add(voiceAssistant(f, "hm-client:someone-else", nil))
	f.Add([]byte(`{"principal":"` + household + `","principal":"household:other"}`))
	// The shipped mandates of the specification, as mandates of the agent.
	for _, pattern := range []string{"examples/*.json", "conformance/mandates/*.json"} {
		paths, err := fs.Glob(spec.FS(), pattern)
		if err != nil {
			f.Fatal(err)
		}
		for _, p := range paths {
			data, err := fs.ReadFile(spec.FS(), p)
			if err != nil {
				f.Fatal(err)
			}
			var doc map[string]any
			if json.Unmarshal(data, &doc) == nil {
				doc["principal"] = household
				doc["agent"] = map[string]any{"client_id": "CLIENT", "display_name": "Voice assistant"}
				data, _ = json.Marshal(doc)
			}
			f.Add(data)
		}
	}
	f.Fuzz(func(t *testing.T, document []byte) {
		document = bytes.ReplaceAll(document, []byte("CLIENT"), []byte(a.ClientID))
		_ = mandate.NamedEntities(document)
		_ = mandate.SameEditable(current, document)
		_, _ = mandate.NewCriticalGrant(current, document)
		_, _ = mandate.ReplaceApprovers(document, []string{"user-1"})

		before, err := e.log.LastSeq(ctx)
		if err != nil {
			t.Fatal(err)
		}
		info, err := e.mandates.Put(ctx, document, admin)
		if err != nil {
			if !errors.Is(err, mandate.ErrInvalid) && !errors.Is(err, mandate.ErrConflict) && !errors.Is(err, mandate.ErrCriticalConfirmation) &&
				!errors.Is(err, mandate.ErrAuditModeNeedsConfirmation) {
				t.Fatalf("undefined error %v", err)
			}
			if after, err := e.log.LastSeq(ctx); err != nil || after != before {
				t.Fatalf("refused (%v), but the audit log grew from %d to %d", err, before, after)
			}
			return
		}
		if info.ClientID != a.ClientID {
			t.Fatalf("stored for %q", info.ClientID)
		}
		loaded, err := e.mandates.ForAgent(ctx, a.ClientID)
		if err != nil || loaded.Info.Digest != info.Digest || loaded.Mandate == nil || loaded.Mandate.Digest() != info.Digest {
			t.Fatalf("stored %+v, loaded %+v, %v", info, loaded.Info, err)
		}
	})
}
