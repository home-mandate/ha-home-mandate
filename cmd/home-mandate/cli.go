// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/home-mandate/spec/jws"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/home-mandate/spec/evaluator"

	"github.com/home-mandate/ha-home-mandate/internal/admission"
	"github.com/home-mandate/ha-home-mandate/internal/agent"
	"github.com/home-mandate/ha-home-mandate/internal/approval"
	"github.com/home-mandate/ha-home-mandate/internal/audit"
	"github.com/home-mandate/ha-home-mandate/internal/config"
	"github.com/home-mandate/ha-home-mandate/internal/mandate"
	"github.com/home-mandate/ha-home-mandate/internal/store"
)

const databaseFile = "home-mandate.db"

// localAdmin is the actor of changes made with the administration commands.
var localAdmin = audit.Actor{Kind: audit.ActorUser, ID: "local-admin"}

// errBrokenLog is reported by audit verify; the details are printed already.
var errBrokenLog = errors.New("audit log is broken")

// state is the opened database with everything built on it.
type state struct {
	// signer signs the checkpoints of the audit log.
	signer    *audit.Signer
	cfg       config.Config
	store     *store.Store
	household string
	log       *audit.Log
	agents    *agent.Store
	mandates  *mandate.Store
	admission *admission.Store
	approvers *approval.Approvers
}

// openState opens the database for the administration commands; they need the data
// directory only, no Home Assistant credentials.
func openState(ctx context.Context, e env) (*state, error) {
	dir, err := config.DataDir(e.getenv)
	if err != nil {
		return nil, err
	}
	s, err := openStore(ctx, dir)
	if err != nil {
		return nil, err
	}
	if err := attachSigner(ctx, s, dir, e.getenv); err != nil {
		_ = s.store.Close()
		return nil, err
	}
	return s, nil
}

// attachSigner gives the audit log the key for its checkpoints, so that it writes them
// and verifies how far the log is anchored.
func attachSigner(ctx context.Context, s *state, dataDir string, getenv func(string) string) error {
	signer, err := loadSigner(ctx, s.store, dataDir, getenv)
	if err != nil {
		return err
	}
	s.signer = signer
	s.log.SetSigner(signer)
	return nil
}

func openStore(ctx context.Context, dataDir string) (*state, error) {
	st, err := store.Open(ctx, filepath.Join(dataDir, databaseFile))
	if err != nil {
		return nil, err
	}
	household, err := st.Household(ctx)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	issuer, err := mandateIssuer(ctx, st)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	log := audit.New(st.DB(), household)
	agents, mandates := agent.New(st.DB(), log), mandate.New(st.DB(), log, household, issuer)
	approvers := approval.NewApprovers(st.DB(), log)
	adm := admission.New(st.DB(), log, agents, mandates, household)
	// The approvers placeholder of templates stands for them, with the admitting human.
	adm.SetApprovers(func(ctx context.Context) ([]string, error) {
		list, err := approvers.List(ctx)
		if err != nil {
			return nil, err
		}
		ids := make([]string, len(list))
		for i, a := range list {
			ids[i] = a.UserID
		}
		return ids, nil
	})
	return &state{store: st, household: household, log: log, agents: agents, mandates: mandates,
		admission: adm, approvers: approvers}, nil
}

// settingMandateIssuer holds the issuer of the mandates this installation stores.
const settingMandateIssuer = "mandate_issuer"

// mandateIssuer returns the URI this installation issues mandates as (SPEC-v0 section
// 3.5), created once. It must never change: a mandate with a version only follows one
// of the same issuer.
func mandateIssuer(ctx context.Context, st *store.Store) (string, error) {
	return st.SettingOnce(ctx, settingMandateIssuer, "urn:uuid:"+newUUID())
}

// withState opens the state, runs fn and reports its error.
func withState(ctx context.Context, e env, fn func(*state) error) int {
	s, err := openState(ctx, e)
	if err != nil {
		fmt.Fprintln(e.stderr, "home-mandate:", permissionHint(err, os.Getuid(), os.Getgid()))
		return exitFailure
	}
	defer s.store.Close()
	if err := fn(s); err != nil {
		if !errors.Is(err, errBrokenLog) {
			fmt.Fprintln(e.stderr, "home-mandate:", err)
		}
		return exitFailure
	}
	return exitOK
}

func agentCommand(ctx context.Context, e env, args []string) int {
	if len(args) == 0 {
		return usageError(e, "agent needs a subcommand")
	}
	switch args[0] {
	case "list":
		return withState(ctx, e, func(s *state) error {
			agents, err := s.agents.List(ctx)
			for _, a := range agents {
				fmt.Fprintf(e.stdout, "%s\t%s\t%s\n", a.ClientID, a.Status, a.DisplayName)
			}
			return err
		})
	case "revoke":
		if len(args) != 2 {
			return usageError(e, "agent revoke needs CLIENT_ID")
		}
		return withState(ctx, e, func(s *state) error { return s.agents.Revoke(ctx, args[1], localAdmin) })
	default:
		return usageError(e, fmt.Sprintf("unknown agent subcommand %q", args[0]))
	}
}

// emergencyStopCommand switches the emergency stop. It takes effect in a running gateway
// at once: the stop and the token revocation are read from the database per request.
func emergencyStopCommand(ctx context.Context, e env, args []string) int {
	if len(args) != 1 {
		return usageError(e, "emergency-stop needs on, off or status")
	}
	switch args[0] {
	case "status":
		return withState(ctx, e, func(s *state) error {
			on, err := s.agents.EmergencyStopActive(ctx)
			if err == nil {
				fmt.Fprintln(e.stdout, "emergency stop:", onOff(on))
			}
			return err
		})
	case "on", "off":
		on := args[0] == "on"
		return withState(ctx, e, func(s *state) error {
			changed, err := s.agents.SetEmergencyStop(ctx, on, localAdmin)
			switch {
			case err != nil:
				return err
			case !changed:
				fmt.Fprintln(e.stdout, "emergency stop already", onOff(on))
			case on:
				fmt.Fprintln(e.stdout, "emergency stop activated: all tokens revoked, agents blocked")
			default:
				fmt.Fprintln(e.stdout, "emergency stop released: agents need new tokens")
			}
			return nil
		})
	default:
		return usageError(e, fmt.Sprintf("unknown emergency-stop argument %q", args[0]))
	}
}

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

// approverCommand manages who receives approval requests on which devices. The UI
// channel is set in the UI only, where administrator rights are checked; adding
// devices here keeps it.
func approverCommand(ctx context.Context, e env, args []string) int {
	switch {
	case (len(args) == 3 || len(args) == 4) && args[0] == "add":
		ap := approval.Approver{UserID: args[1], Devices: parseDevices(args[2])}
		if len(args) == 4 {
			ap.Language = args[3]
		}
		return withState(ctx, e, func(s *state) error {
			list, err := s.approvers.List(ctx)
			if err != nil {
				return err
			}
			if i := slices.IndexFunc(list, func(a approval.Approver) bool { return a.UserID == ap.UserID }); i >= 0 {
				ap.UI, ap.UICritical = list[i].UI, list[i].UICritical
			}
			return s.approvers.Put(ctx, ap, localAdmin)
		})
	case len(args) == 1 && args[0] == "list":
		return withState(ctx, e, func(s *state) error {
			list, err := s.approvers.List(ctx)
			for _, ap := range list {
				fmt.Fprintf(e.stdout, "%s\t%s\t%s\t%s\n", ap.UserID, devicesText(ap.Devices), languageText(ap.Language), uiText(ap))
			}
			return err
		})
	case len(args) == 2 && args[0] == "remove":
		return withState(ctx, e, func(s *state) error { return s.approvers.Remove(ctx, args[1], localAdmin) })
	default:
		return usageError(e, "approver needs add USER_ID NOTIFY_SERVICE[:no-critical][,…] [de|en], list or remove USER_ID")
	}
}

// noCritical marks a device without critical requests (Mac app, Android: no unlocking).
const noCritical = ":no-critical"

// parseDevices reads SERVICE[:no-critical],…; anything else is left for the validation
// to refuse.
func parseDevices(arg string) []approval.Device {
	var out []approval.Device
	for _, part := range strings.Split(arg, ",") {
		service, without := strings.CutSuffix(part, noCritical)
		out = append(out, approval.Device{Service: service, Critical: !without})
	}
	return out
}

func devicesText(devices []approval.Device) string {
	if len(devices) == 0 {
		return "-"
	}
	out := make([]string, len(devices))
	for i, d := range devices {
		out[i] = "notify." + d.Service
		if !d.Critical {
			out[i] += noCritical
		}
	}
	return strings.Join(out, ",")
}

func languageText(lang string) string {
	if lang == "" {
		return "household"
	}
	return lang
}

func uiText(ap approval.Approver) string {
	switch {
	case ap.UICritical:
		return "ui+critical"
	case ap.UI:
		return "ui"
	}
	return "-"
}

func mandateCommand(ctx context.Context, e env, args []string) int {
	if len(args) == 0 {
		return usageError(e, "mandate needs a subcommand")
	}
	switch args[0] {
	case "import":
		if len(args) != 2 {
			return usageError(e, "mandate import needs FILE or -")
		}
		return withState(ctx, e, func(s *state) error {
			doc, err := readDocument(e, args[1])
			if err != nil {
				return err
			}
			info, err := s.mandates.Put(ctx, doc, localAdmin)
			if err != nil {
				return err
			}
			fmt.Fprintf(e.stdout, "id=%s\nclient_id=%s\ndigest=%s\n", info.ID, info.ClientID, info.Digest)
			return nil
		})
	case "list":
		return withState(ctx, e, func(s *state) error {
			list, err := s.mandates.List(ctx)
			for _, m := range list {
				fmt.Fprintf(e.stdout, "%s\t%s\t%s\t%s\n", m.ID, m.Status, m.ClientID, m.Digest)
			}
			return err
		})
	case "revoke":
		if len(args) != 2 {
			return usageError(e, "mandate revoke needs ID")
		}
		return withState(ctx, e, func(s *state) error { return s.mandates.Revoke(ctx, args[1], localAdmin) })
	case "check":
		if len(args) != 1 {
			return usageError(e, "mandate check takes no arguments")
		}
		return withState(ctx, e, func(s *state) error { return checkMandates(ctx, e, s) })
	case "template":
		return templateCommand(ctx, e, args[1:])
	default:
		return usageError(e, fmt.Sprintf("unknown mandate subcommand %q", args[0]))
	}
}

// templateCommand manages the mandate templates a human picks from when admitting an
// agent.
func templateCommand(ctx context.Context, e env, args []string) int {
	switch {
	case len(args) == 3 && args[0] == "import":
		return withState(ctx, e, func(s *state) error {
			doc, err := readDocument(e, args[2])
			if err != nil {
				return err
			}
			return s.admission.PutTemplate(ctx, args[1], doc, localAdmin)
		})
	case len(args) == 1 && args[0] == "list":
		return withState(ctx, e, func(s *state) error {
			list, err := s.admission.Templates(ctx)
			for _, t := range list {
				switch {
				case t.Builtin && t.Hidden:
					fmt.Fprintf(e.stdout, "%s\tbase template, hidden\n", t.Name)
				case t.Builtin:
					fmt.Fprintf(e.stdout, "%s\tbase template\n", t.Name)
				default:
					fmt.Fprintf(e.stdout, "%s\t%s\t%s\n", t.Name, t.CreatedAt.Format(time.RFC3339), t.CreatedBy)
				}
			}
			return err
		})
	case len(args) == 2 && args[0] == "remove":
		return withState(ctx, e, func(s *state) error { return s.admission.RemoveTemplate(ctx, args[1], localAdmin) })
	default:
		return usageError(e, "mandate template needs import NAME FILE|-, list or remove NAME")
	}
}

// readDocument reads a mandate from a file or stdin, at most one byte more than the
// size limit so that the evaluator reports an oversized mandate.
func readDocument(e env, name string) ([]byte, error) {
	r := e.stdin
	if name != "-" {
		f, err := os.Open(name)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		r = f
	}
	return io.ReadAll(io.LimitReader(r, evaluator.MaxMandateBytes+1))
}

func auditCommand(ctx context.Context, e env, args []string) int {
	if len(args) != 1 {
		return usageError(e, "audit needs verify, export, key or accept-clock")
	}
	switch args[0] {
	case "verify":
		return withState(ctx, e, func(s *state) error {
			r, err := s.log.Verify(ctx)
			if err != nil {
				return err
			}
			if !r.Valid {
				fmt.Fprintf(e.stdout, "audit log broken at seq %d (entry %d)\n", r.BrokenAt, r.Index+1)
				return errBrokenLog
			}
			fmt.Fprintln(e.stdout, "audit log valid")
			// Entries after the last checkpoint are consistent but not anchored.
			fmt.Fprintf(e.stdout, "entries=%d\nanchored_up_to=%d\nlog_id=%s\n", r.Entries, r.AnchoredSeq, s.signer.LogID)
			return nil
		})
	case "key":
		// The public key and the log ID belong outside the device: with them, anyone can
		// verify an exported log and its checkpoints (SPEC-v0 section 9.5).
		return withState(ctx, e, func(s *state) error {
			set, err := jws.MarshalJWKS(jws.Keys{s.signer.KeyID: s.signer.Key.Public()})
			if err != nil {
				return err
			}
			fmt.Fprintf(e.stdout, "log_id=%s\n%s\n", s.signer.LogID, set)
			return nil
		})
	case "export":
		return withState(ctx, e, func(s *state) error { return s.log.Export(ctx, e.stdout) })
	case "accept-clock":
		// After the clock ran ahead by mistake and was corrected, the entries with future
		// times would stop every decision (SPEC-v0 section 11.4) until that time.
		return withState(ctx, e, func(s *state) error {
			seq, latest, err := s.log.AcceptClock(ctx)
			if err != nil {
				return err
			}
			fmt.Fprintf(e.stdout, "entries up to seq %d (latest time %s) no longer count for the clock check\n", seq, latest)
			return nil
		})
	default:
		return usageError(e, fmt.Sprintf("unknown audit subcommand %q", args[0]))
	}
}

// errInvalidStored makes "mandate check" end with a failure after it listed the findings.
var errInvalidStored = errors.New("stored mandates or templates are not valid")

// checkMandates lists the stored mandates and templates that the evaluator of this
// version does not accept. Run it with the new binary before an update goes live: an
// invalid mandate denies every request of its agent.
func checkMandates(ctx context.Context, e env, s *state) error {
	mandates, err := s.mandates.Invalid(ctx)
	if err != nil {
		return err
	}
	templates, err := s.admission.InvalidTemplates(ctx)
	if err != nil {
		return err
	}
	for _, m := range mandates {
		fmt.Fprintf(e.stdout, "mandate\t%s\t%s\t%s\t%s\n", m.ID, m.Status, m.ClientID, m.Problem)
	}
	for _, t := range templates {
		fmt.Fprintf(e.stdout, "template\t%s\t%s\n", t.Name, t.Problem)
	}
	if len(mandates)+len(templates) > 0 {
		return errInvalidStored
	}
	fmt.Fprintln(e.stdout, "ok")
	return nil
}
