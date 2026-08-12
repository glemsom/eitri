// Command eitri is the Eitri single-binary agent CLI. This ticket (T1) wires
// the boot skeleton: flag parsing, the data directory, and the hard bubblewrap
// prerequisite. The batch (-b) prompt and -d debug flag are parsed here and
// their engine behavior is hooked up in later tickets.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/glemsom/eitri/internal/app"
)

const usage = `eitri — a self-hosted, single-binary AI coding agent for Linux.

Usage:

  eitri [flags]

Flags:

  -b <prompt>    run once in batch mode with the given prompt and exit
  -d             enable debug mode (writes full HTTP traces in later tickets)
  --version      print the version and exit

Eitri creates its data directory (~/.eitri, or EITRI_DIR) on launch and
requires bubblewrap (bwrap) to be installed; it never runs unsandboxed.
`

func main() {
	var (
		prompt   = flag.String("b", "", "run once in batch mode with the given prompt and exit")
		debug    = flag.Bool("d", false, "enable debug mode")
		showVers = flag.Bool("version", false, "print the version and exit")
	)
	flag.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	flag.Parse()

	if *showVers {
		if err := app.Run(app.Options{Version: true}); err != nil {
			die(err)
		}
		return
	}

	opts := app.Options{
		DataDir: os.Getenv(app.DataDirEnv),
		Debug:   *debug,
		// Batch prompt is accepted now; Run wires it in a later ticket (T1c),
		// so referencing it keeps the contract explicit.
	}
	_ = prompt

	if err := app.Run(opts); err != nil {
		die(err)
	}
}

// die prints the error to stderr and exits non-zero.
func die(err error) {
	fmt.Fprintf(os.Stderr, "eitri: %v\n", err)
	os.Exit(1)
}
