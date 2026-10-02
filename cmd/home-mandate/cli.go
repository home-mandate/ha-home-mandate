// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/mandate-spec/mandate-spec/evaluator"

	"github.com/home-mandate/home-mandate/internal/agent"
	"github.com/home-mandate/home-mandate/internal/audit"
	"github.com/home-mandate/home-mandate/internal/config"
	"github.com/home-mandate/home-mandate/internal/mandate"
	"github.com/home-mandate/home-mandate/internal/store"
)

const databaseFile = "home-mandate.db"

// localAdmin is the actor of changes made with the administration commands.
var localAdmin = audit.Actor{Kind: audit.ActorUser, ID: "local-admin"}

// errBrokenLog is reported by audit verify; the details are printed already.
var errBrokenLog = errors.New("audit log is broken")

// state is the opened database with everything built on it.
type state struct {
	cfg       config.Config
	store     *store.Store
	household string
	log       *audit.Log
	agents    *agent.Store
	mandates  *mandate.Store
}

// openState opens the database for the administration commands; they need the data
// directory only, no Home Assistant credentials.
func openState(ctx context.Context, e env) (*state, error) {
	dir, err := config.DataDir(e.getenv)
	if err != nil {
		return nil, err
	}
	return openStore(ctx, dir)
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
	log := audit.New(st.DB(), household)
	return &state{store: st, household: household, log: log,
		agents: agent.New(st.DB(), log), mandates: mandate.New(st.DB(), log, household)}, nil
}

// withState opens the state, runs fn and reports its error.
func withState(ctx context.Context, e env, fn func(*state) error) int {
	s, err := openState(ctx, e)
	if err != nil {
		fmt.Fprintln(e.stderr, "home-mandate:", err)
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
	default:
		return usageError(e, fmt.Sprintf("unknown mandate subcommand %q", args[0]))
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
		return usageError(e, "audit needs verify or export")
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
			return nil
		})
	case "export":
		return withState(ctx, e, func(s *state) error { return s.log.Export(ctx, e.stdout) })
	default:
		return usageError(e, fmt.Sprintf("unknown audit subcommand %q", args[0]))
	}
}
