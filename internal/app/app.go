// Package app drives the Eitri boot sequence: resolving the data directory,
// checking the bubblewrap (bwrap) sandbox prerequisite, and wiring flag-driven
// behavior. Later tickets hang the run engine, config, and storage off this
// boot path.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/engine"
	"github.com/glemsom/eitri/internal/provider"
	"github.com/glemsom/eitri/internal/session"
	"github.com/glemsom/eitri/internal/tools"
)

// Version reports the Eitri build version tag, set at build time.
var Version = "0.1.0-dev"

// Environment variables honored at boot (eitri.md §2.7).
const (
	// DataDirEnv overrides the default ~/.eitri data directory.
	DataDirEnv = "EITRI_DIR"
	// ConfigEnv overrides the default config file path (~/.eitri/config.json).
	ConfigEnv = "EITRI_CONFIG"
)

// ErrMissingBwrap is returned when the bubblewrap (bwrap) executable cannot be
// found on the host. Per ADR-0001 decision 3, bwrap is a hard prerequisite:
// Eitri never falls back to unsandboxed execution.
var ErrMissingBwrap = errors.New("bubblewrap (bwrap) is required but was not found; install bubblewrap to continue")

// Options control a single Run invocation.
type Options struct {
	// Version, when true, prints the version and exits without booting.
	Version bool

	// DataDir is the top-level data directory. When empty, it is resolved from
	// EITRI_DIR or defaults to ~/.eitri.
	DataDir string

	// ConfigPath is the config file path. When empty, it is resolved from
	// EITRI_DIR/config.json (or EITRI_CONFIG when that is set).
	ConfigPath string

	// Debug enables debug mode (-d), attaching the HTTP trace sink to the run
	// session for deep-dive provider debugging (eitri.md §2.5).
	Debug bool

	// Prompt, when non-empty, runs batch mode with the given prompt and prints
	// the final answer to Stdout (eitri -b). Reasoning is suppressed from
	// stdout unless Verbose is set (docs/spec.md §6).
	Prompt string

	// Verbose (-v) enables reasoning output to stdout in batch mode.
	Verbose bool

	// Stdout receives the batch answer. When nil it defaults to os.Stdout.
	Stdout io.Writer

	// Provider, when non-nil, backs the batch engine. When nil, a provider is
	// built from the loaded config (OpenCode Go via OPENCODE_API_KEY). Tests
	// inject the fake provider through this option.
	Provider provider.Provider

	// LookPath locates an executable on the host PATH. It defaults to
	// exec.LookPath; tests inject a stub to drive bwrap-missing behavior.
	LookPath func(name string) (string, error)
}

// Run performs the Eitri boot sequence and returns the first error it hits, so
// a caller can map it to an exit status. The workspace, session temp, sandbox
// (bwrap cage), host-side tools, and path namespace are later components that
// hang off this sequence; batch mode (opts.Prompt) is driven through the shared
// run engine here.
func Run(opts Options) error {
	// The debug (-d) flag is honored here via opts.Debug when establishing the
	// run session.
	if opts.Version {
		fmt.Println(Version)
		return nil
	}

	dir, err := resolveDataDir(opts.DataDir)
	if err != nil {
		return err
	}
	if err := ensureDataDir(dir); err != nil {
		return err
	}

	cfgPath, err := resolveConfigPath(dir, opts.ConfigPath)
	if err != nil {
		return err
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}

	// Establish the run's on-disk session trail under sessions/<GUID>.
	sess, err := session.New(dir, opts.Debug)
	if err != nil {
		return err
	}

	lookPath := opts.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	if _, err := lookPath("bwrap"); err != nil {
		return ErrMissingBwrap
	}

	// Build the shared tool registry wired to this run's workspace + session
	// temp so bash/read/write/edit resolve the same path namespace (ADR-0002).
	// Both the registry and the sandbox mount agree on the temp host root.
	guid := tools.GUID(sess.GUID())
	workspace, err := os.Getwd()
	if err != nil {
		return err
	}
	reg := tools.NewRegistry(tools.Deps{
		Workspace:     workspace,
		TempHost:      tools.HostTempFor(guid),
		GUID:          guid,
		ExtraWritable: cfg.ExtraWritablePaths,
		Runner:        tools.RealRunner,
	})
	// Session temp is ephemeral per run and removed when the run ends (ADR-0002).
	tempHost := tools.HostTempFor(guid)
	defer func() { _ = os.RemoveAll(tempHost) }()

	// Batch mode: run one agent turn through the shared engine and print the
	// final answer (docs/spec.md §6; eitri.md §2.1). The engine dispatches any
	// tool calls through the registry over the provider seam.
	if opts.Prompt != "" {
		p := opts.Provider
		if p == nil {
			p = defaultProvider(os.Getenv(ProviderKeyEnv), os.Getenv(ProviderURLEnv))
		}
		e := engine.New(p, sess)
		res, err := e.RunAgent(context.Background(), engine.RunRequest{
			Model:  cfg.Model,
			Prompt: opts.Prompt,
		}, engine.AgentOptions{
			Tools:      providerTools(reg.Definitions()),
			ToolChoice: "auto",
			Executor: engine.ExecutorFunc(func(ctx context.Context, name, argsJSON string) (string, error) {
				var args map[string]any
				if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
					return "", err
				}
				return reg.Run(ctx, name, args)
			}),
			MaxTurns: cfg.MaxTurns,
		})
		if err != nil {
			return err
		}
		out := opts.Stdout
		if out == nil {
			out = os.Stdout
		}
		if opts.Verbose && res.Reasoning != "" {
			fmt.Fprintf(out, "‹thinking›\n%s\n‹/thinking›\n", res.Reasoning)
		}
		fmt.Fprintln(out, res.Answer)
	}

	return nil
}

// providerTools maps the registry's definitions to provider Tool objects for
// the Chat-Completions request head.
func providerTools(defs []tools.Definition) []provider.Tool {
	out := make([]provider.Tool, 0, len(defs))
	for _, d := range defs {
		out = append(out, provider.Tool{
			Type: "function",
			Function: provider.ToolFunction{
				Name:        d.Name,
				Description: d.Description,
				Parameters:  d.Parameters,
			},
		})
	}
	return out
}

// ProviderKeyEnv is the environment variable holding the OpenCode Go API key.
// Per docs/research/opencode-endpoints.md, the OpenCode Go credential is
// delivered via this env var (no key material in config).
const ProviderKeyEnv = "OPENCODE_API_KEY"

// ProviderURLEnv optionally overrides the Chat-Completions endpoint Eitri
// talks to, for local testing and custom OpenAI-compatible endpoints (the
// latter formalized in T11). It defaults to OpenCode Go.
const ProviderURLEnv = "EITRI_PROVIDER_URL"

// defaultProvider builds the primary provider: an OpenAI-compatible
// Chat-Completions client against OpenCode Go's endpoint, overridable via env.
func defaultProvider(apiKey, url string) provider.Provider {
	if url == "" {
		url = "https://opencode.ai/zen/go/v1/chat/completions"
	}
	return provider.NewOpenAICompatible(apiKeyOrDefault(apiKey), url)
}

func apiKeyOrDefault(key string) string {
	if key != "" {
		return key
	}
	return "not-configured"
}

// resolveConfigPath selects the config file path: an explicit override, else
// EITRI_CONFIG, else <dataDir>/config.json.
func resolveConfigPath(dataDir, explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if env := os.Getenv(ConfigEnv); env != "" {
		return env, nil
	}
	return filepath.Join(dataDir, "config.json"), nil
}

// resolveDataDir selects the data directory: the explicit override, else
// EITRI_DIR, else ~/.eitri.
func resolveDataDir(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if env := os.Getenv(DataDirEnv); env != "" {
		return env, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.New("resolve data dir: cannot determine home directory")
	}
	return filepath.Join(home, ".eitri"), nil
}

// ensureDataDir creates the data directory, tolerating its pre-existence.
func ensureDataDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return errors.New("create data dir " + dir + ": " + err.Error())
	}
	return nil
}
