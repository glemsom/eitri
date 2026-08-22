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
		return fmt.Errorf("usage: eitri session list | show <guid> [--turn N] [--no-reasoning] | grep <pattern> [guid|all]")
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
	case "grep":
		if len(args) < 2 {
			return fmt.Errorf("usage: eitri session grep <pattern> [guid|all]")
		}
		guid := "all"
		if len(args) > 2 {
			guid = strings.TrimPrefix(args[2], "guid=")
		}
		return GrepSession(dataDir, args[1], guid, out)
	default:
		return fmt.Errorf("unknown session subcommand %q; want list, show, or grep", args[0])
	}
}
