// Package app — sessionentry.go: entry point for the `eitri session <subcommand>` CLI family.
package app

import (
	"fmt"
	"io"
	"strconv"
	"strings"
)

const (
	sessionUsage = "usage: eitri session list | show <guid> [--turn N] [--no-reasoning] | talk <guid> [--turn N|N-M] [--from N] [--role user|assistant|tool|system] [--reasoning] | grep <pattern> [guid|all] [-full]"
	showUsage    = "usage: eitri session show <guid> [--turn N] [--no-reasoning]"
	talkUsage    = "usage: eitri session talk <guid> [--turn N|N-M] [--from N] [--role user|assistant|tool|system] [--reasoning]"
	grepUsage    = "usage: eitri session grep <pattern> [guid|all] [-full]"
)

// RunSessionCmd dispatches the `eitri session` debug subcommands. args is everything after "session".
func RunSessionCmd(args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("%s", sessionUsage)
	}
	dataDir, err := resolveDataDir("")
	if err != nil {
		return err
	}
	switch args[0] {
	case "list":
		if len(args) != 1 {
			return fmt.Errorf("%s", sessionUsage)
		}
		return ListSessions(dataDir, out)
	case "show":
		if len(args) < 2 || strings.HasPrefix(args[1], "-") {
			return fmt.Errorf("%s", showUsage)
		}
		turn, noReasoning := 0, false
		for i := 2; i < len(args); i++ {
			switch args[i] {
			case "--turn":
				if turn != 0 || i+1 >= len(args) {
					return fmt.Errorf("%s", showUsage)
				}
				turn, err = parsePositiveInt(args[i+1])
				if err != nil {
					return fmt.Errorf("%s: invalid --turn value %q", showUsage, args[i+1])
				}
				i++
			case "--no-reasoning":
				if noReasoning {
					return fmt.Errorf("%s", showUsage)
				}
				noReasoning = true
			default:
				return fmt.Errorf("%s: unexpected operand or flag %q", showUsage, args[i])
			}
		}
		return ShowSession(dataDir, args[1], turn, noReasoning, out)
	case "talk":
		if len(args) < 2 || strings.HasPrefix(args[1], "-") {
			return fmt.Errorf("%s", talkUsage)
		}
		opts := TalkOptions{}
		turnSet, fromSet, roleSet, reasoningSet := false, false, false, false
		for i := 2; i < len(args); i++ {
			switch args[i] {
			case "--turn":
				if turnSet || fromSet || i+1 >= len(args) {
					return fmt.Errorf("%s", talkUsage)
				}
				opts.FromTurn, opts.ToTurn, err = parseTurnRange(args[i+1])
				if err != nil {
					return fmt.Errorf("%s: %w", talkUsage, err)
				}
				turnSet = true
				i++
			case "--from":
				if fromSet || turnSet || i+1 >= len(args) {
					return fmt.Errorf("%s", talkUsage)
				}
				opts.FromTurn, err = parsePositiveInt(args[i+1])
				if err != nil {
					return fmt.Errorf("%s: invalid --from value %q", talkUsage, args[i+1])
				}
				fromSet = true
				i++
			case "--role":
				if roleSet || i+1 >= len(args) || !validRole(args[i+1]) {
					return fmt.Errorf("%s", talkUsage)
				}
				opts.Role, roleSet = args[i+1], true
				i++
			case "--reasoning":
				if reasoningSet {
					return fmt.Errorf("%s", talkUsage)
				}
				opts.Reasoning, reasoningSet = true, true
			default:
				return fmt.Errorf("%s: unexpected operand or flag %q", talkUsage, args[i])
			}
		}
		return TalkSession(dataDir, args[1], opts, out)
	case "grep":
		if len(args) < 2 || len(args) > 4 {
			return fmt.Errorf("%s", grepUsage)
		}
		guid, full := "all", false
		rest := args[2:]
		if len(rest) > 0 && rest[0] != "-full" {
			if strings.HasPrefix(rest[0], "-") || strings.HasPrefix(rest[0], "guid=") {
				return fmt.Errorf("%s: unexpected operand or flag %q", grepUsage, rest[0])
			}
			guid, rest = rest[0], rest[1:]
		}
		if len(rest) > 0 && rest[0] == "-full" {
			full, rest = true, rest[1:]
		}
		if len(rest) != 0 {
			return fmt.Errorf("%s: unexpected operand or flag %q", grepUsage, rest[0])
		}
		return GrepSession(dataDir, args[1], guid, full, out)
	default:
		return fmt.Errorf("%s: unknown session subcommand %q", sessionUsage, args[0])
	}
}

func parsePositiveInt(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("not a positive integer")
	}
	return n, nil
}

func validRole(role string) bool {
	return role == "user" || role == "assistant" || role == "tool" || role == "system"
}

// parseTurnRange parses "N" (single turn) or "N-M" (inclusive range) for session talk --turn.
func parseTurnRange(s string) (int, int, error) {
	parts := strings.Split(s, "-")
	if len(parts) == 1 {
		n, err := parsePositiveInt(s)
		if err == nil {
			return n, n, nil
		}
	} else if len(parts) == 2 {
		lo, loErr := parsePositiveInt(parts[0])
		hi, hiErr := parsePositiveInt(parts[1])
		if loErr == nil && hiErr == nil && hi >= lo {
			return lo, hi, nil
		}
	}
	return 0, 0, fmt.Errorf("invalid --turn value %q (want N or N-M)", s)
}
