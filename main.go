// Command eitri is the Eitri single-binary agent CLI.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/glemsom/eitri/internal/app"
)

const usage = `eitri — a self-hosted, single-binary AI coding agent for Linux.

Usage:

  eitri [flags]          launch the interactive TUI
  eitri -b <prompt>      run once in batch mode and exit

Flags:

  -b <prompt>    run once in batch mode with the given prompt and exit
  -v             in batch mode, print the model's thinking/reasoning to stdout
  -d             enable debug mode (writes full HTTP traces to/from the provider)
  --version      print the version and exit

Eitri creates its data directory (~/.eitri, or EITRI_DIR) on launch and
requires bubblewrap (bwrap) to be installed; it never runs unsandboxed.
`

func main() {
	var (
		prompt   = flag.String("b", "", "run once in batch mode with the given prompt and exit")
		verbose  = flag.Bool("v", false, "print the model's thinking to stdout in batch mode")
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
		Prompt:  *prompt,
		Verbose: *verbose,
	}

	if err := app.Run(opts); err != nil {
		die(err)
	}
}

// die prints the error to stderr and exits non-zero.
func die(err error) {
	fmt.Fprintf(os.Stderr, "eitri: %v\n", err)
	os.Exit(1)
}
