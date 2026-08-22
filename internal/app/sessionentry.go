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
		return fmt.Errorf("usage: eitri session list | show <guid> [--turn N] | grep <pattern> [guid|all]")
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
			return fmt.Errorf("usage: eitri session show <guid> [--turn N]")
		}
		guid := args[1]
		turn := 0
		for i := 2; i < len(args); i++ {
			if args[i] == "--turn" && i+1 < len(args) {
				if _, err := fmt.Sscanf(args[i+1], "%d", &turn); err != nil {
					return fmt.Errorf("invalid --turn value %q", args[i+1])
				}
				i++
			}
		}
		return ShowSession(dataDir, guid, turn, out)
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
