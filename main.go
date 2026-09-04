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
  eitri session list     list recorded sessions (GUID, time, cycles, model)
  eitri session show <guid> [--turn N] [--no-reasoning]
                         compact per-cycle summary; --turn N dumps that cycle's full JSON records
  eitri session talk <guid> [--turn N|N-M] [--from N] [--role user|assistant|tool|system]
                     [--reasoning]
                         full conversation as plain text; shared request history is deduped
                         reasoning is stripped unless --reasoning
  eitri session grep <pattern> [guid|all] [-full]
                         find cycles whose messages match pattern, with snippets;
                         -full prints the complete matching field text

Flags:

  -b <prompt>    run once in batch mode with the given prompt and exit
  -v             in batch mode, print the model's thinking/reasoning to stdout
  -d             enable debug mode (writes full HTTP traces to/from the provider)
  --pprof <addr> enable localhost pprof diagnostics (example: 127.0.0.1:6060)
  --pprof-mutex  include mutex profile evidence when --pprof is enabled
  --pprof-block  include block profile evidence when --pprof is enabled
  --version      print the version and exit

Eitri creates its data directory (~/.eitri, or EITRI_DIR) on launch and
refuses to start without its declared toolset — bwrap, bash, rg, curl, lynx,
patch, python3, git, jq, xdg-open — because its agent prompt promises those tools
unconditionally. Install hints:
  Debian/Ubuntu: sudo apt install bubblewrap bash ripgrep curl lynx patch python3 git jq xdg-utils
  Fedora:        sudo dnf install bubblewrap bash ripgrep curl lynx patch python3 git jq xdg-utils
  Arch:          sudo pacman -S bubblewrap bash ripgrep curl lynx patch python3 git jq xdg-utils
The base coreutils (grep, sed, awk, cat, nl, diff) are assumed present.
Eitri never runs unsandboxed.
`

func main() {
	if len(os.Args) > 1 && os.Args[1] == "session" {
		if err := app.RunSessionCmd(os.Args[2:], os.Stdout); err != nil {
			die(err)
		}
		return
	}

	var (
		prompt     = flag.String("b", "", "run once in batch mode with the given prompt and exit")
		verbose    = flag.Bool("v", false, "print the model's thinking to stdout in batch mode")
		debug      = flag.Bool("d", false, "enable debug mode")
		pprofAddr  = flag.String("pprof", "", "enable localhost pprof diagnostics, optionally with an address")
		pprofMutex = flag.Bool("pprof-mutex", false, "include mutex profile evidence when --pprof is enabled")
		pprofBlock = flag.Bool("pprof-block", false, "include block profile evidence when --pprof is enabled")
		showVers   = flag.Bool("version", false, "print the version and exit")
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
		Pprof: app.PprofOptions{
			Enabled: *pprofAddr != "",
			Addr:    *pprofAddr,
			Mutex:   *pprofMutex,
			Block:   *pprofBlock,
		},
	}

	if err := app.Run(opts); err != nil {
		die(err)
	}
}

func die(err error) {
	fmt.Fprintf(os.Stderr, "eitri: %v\n", err)
	os.Exit(1)
}
