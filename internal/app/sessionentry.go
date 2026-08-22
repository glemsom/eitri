// Package app — sessionentry.go: entry point for the `eitri session <subcommand>` CLI family.
package app

import (
	"fmt"
	"io"
	"strings"
)

// RunSessionCmd dispatches the `eitri session` debug subcommands. args is everything after "session".
func RunSessionCmd(args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: eitri session list | show <guid> [--turn N] [--no-reasoning] | talk <guid> [--turn N|N-M] [--from N] [--role R] [--reasoning] [--all] | grep <pattern> [guid|all] [-full]")
	}
	dataDir, err := resolveDataDir("")
	if err != nil {
		return err
	}
	switch args[0] {
	case "list":
		return ListSessions(dataDir, out)
	case "show":
		if len(args) < 2 {
			return fmt.Errorf("usage: eitri session show <guid> [--turn N] [--no-reasoning]")
		}
		guid := args[1]
		turn := 0
		noReasoning := false
		for i := 2; i < len(args); i++ {
			switch {
			case args[i] == "--turn" && i+1 < len(args):
				if _, err := fmt.Sscanf(args[i+1], "%d", &turn); err != nil {
					return fmt.Errorf("invalid --turn value %q", args[i+1])
				}
				i++
			case args[i] == "--no-reasoning":
				noReasoning = true
			default:
				return fmt.Errorf("unknown flag %q", args[i])
			}
		}
		return ShowSession(dataDir, guid, turn, noReasoning, out)
	case "talk":
		if len(args) < 2 {
			return fmt.Errorf("usage: eitri session talk <guid> [--turn N|N-M] [--from N] [--role user|assistant|tool|system] [--reasoning] [--all]")
		}
		opts := TalkOptions{}
		for i := 2; i < len(args); i++ {
			switch {
			case args[i] == "--turn" && i+1 < len(args):
				lo, hi, err := parseTurnRange(args[i+1])
				if err != nil {
					return err
				}
				opts.FromTurn, opts.ToTurn = lo, hi
				i++
			case args[i] == "--from" && i+1 < len(args):
				if _, err := fmt.Sscanf(args[i+1], "%d", &opts.FromTurn); err != nil {
					return fmt.Errorf("invalid --from value %q", args[i+1])
				}
				i++
			case args[i] == "--role" && i+1 < len(args):
				opts.Role = args[i+1]
				i++
			case args[i] == "--reasoning":
				opts.Reasoning = true
			case args[i] == "--all":
				opts.AllCycles = true
			default:
				return fmt.Errorf("unknown flag %q", args[i])
			}
		}
		return TalkSession(dataDir, args[1], opts, out)
	case "grep":
		if len(args) < 2 {
			return fmt.Errorf("usage: eitri session grep <pattern> [guid|all]")
		}
		full := false
		rest := args[2:]
		filtered := rest[:0]
		for _, a := range rest {
			if a == "-full" || a == "--full" {
				full = true
				continue
			}
			filtered = append(filtered, a)
		}
		guid := "all"
		if len(filtered) > 0 {
			guid = strings.TrimPrefix(filtered[0], "guid=")
		}
		return GrepSession(dataDir, args[1], guid, full, out)
	default:
		return fmt.Errorf("unknown session subcommand %q; want list, show, talk, or grep", args[0])
	}
}

// parseTurnRange parses "N" (single turn) or "N-M" (inclusive range) for session talk --turn.
func parseTurnRange(s string) (int, int, error) {
	var lo, hi int
	if n, _ := fmt.Sscanf(s, "%d-%d", &lo, &hi); n == 2 && lo > 0 && hi >= lo {
		return lo, hi, nil
	}
	if _, err := fmt.Sscanf(s, "%d", &lo); err != nil || lo <= 0 {
		return 0, 0, fmt.Errorf("invalid --turn value %q (want N or N-M)", s)
	}
	return lo, lo, nil
}
