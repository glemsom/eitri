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
	"net/http"
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

	// Fetcher backs the web_fetch tool's network seam. When nil, a real
	// http.Client is used; tests inject a stub to drive engine-seam web_fetch
	// turns without a network.
	Fetcher tools.Fetcher

	// Browser backs the open_in_browser tool's host-side launch seam. When nil,
	// xdg-open is used; tests inject a recording stub.
	Browser tools.BrowserLauncher
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
	// Agent Skill discovery: user-global ~/.agents/skills + project
	// .agents/skills, project shadowing user on exact-name collision. The
	// catalog is threaded through the registry so the dedicated skill tool is
	// registered only when skills exist (docs/spec.md §3; ticket #33).
	skills := discoverSkills(workspace)
	reg := tools.NewRegistry(tools.Deps{
		Workspace:     workspace,
		TempHost:      tools.HostTempFor(guid),
		GUID:          guid,
		ExtraWritable: cfg.ExtraWritablePaths,
		Runner:        tools.RealRunner,
		Fetcher:       opts.Fetcher,
		Browser:       opts.Browser,
		Skills:        skills,
	})
	// Session temp is ephemeral per run and removed when the run ends (ADR-0002).
	tempHost := tools.HostTempFor(guid)
	defer func() { _ = os.RemoveAll(tempHost) }()

	// Build the provider the saved config selects (opencode-go, github-copilot,
	// or custom-openai) and honor it across both run kinds (T11 / eitri.md §2.2).
	// Tests inject a deterministic provider via Options.Provider; production
	// builds it from the loaded config + env credentials.
	p := opts.Provider
	if p == nil {
		var err error
		p, err = buildProvider(cfg, cfgPath)
		if err != nil {
			return err
		}
	}
	e := engine.New(p, sess)
	key := sess.GUID() // opt into the session-scoped prompt cache (T6)

	// Build the interactive TUI run when no batch prompt is given. It sits on
	// the same engine, session transcript, and tool registry as batch, and
	// renders into the primary buffer (docs/spec.md §9).
	if opts.Prompt == "" {
		return runTUI(e, cfg, reg, key, p, cfgPath, skills)
	}

	res, err := runAgent(e, cfg, reg, key, opts.Prompt, nil)
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

	return nil
}

// discoverSkills discovers Agent Skill packs from the user-global
// ~/.agents/skills root and the project .agents/skills root under workspace
// (project shadows user on exact-name collision). Discovery is lenient:
// unparseable packs are omitted with a warning to stderr (fail-closed). It
// returns an empty catalog when no roots exist or none parse, so the skill
// tool is simply unregistered.
func discoverSkills(workspace string) *tools.Catalog {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "eitri: resolve home for skill discovery: %v\n", err)
		return &tools.Catalog{}
	}
	c, err := tools.Discover(
		filepath.Join(home, "agents", "skills"),
		filepath.Join(workspace, ".agents", "skills"),
		stderrWarner{},
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "eitri: skill discovery failed: %v\n", err)
		return &tools.Catalog{}
	}
	return c
}

// stderrWarner reports discovery warnings to stderr.
type stderrWarner struct{}

func (stderrWarner) Warnf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "eitri: "+format+"\n", args...)
}

// runAgent drives one agent turn (user prompt → assistant answer) over the
// shared run engine, session transcript, and tool registry that both the TUI
// and batch use. It is the single turn seam for both run kinds, so a TUI run
// round-trips through the engine exactly like batch (docs/spec.md §9, eitri.md
// §2.6).
func runAgent(e *engine.Engine, cfg config.Config, reg *tools.Registry, sessionKey, prompt string, canContinue func() bool) (engine.Result, error) {
	compaction := &engine.CompactionConfig{Fraction: cfg.CompactionFraction}
	return e.RunAgent(context.Background(), engine.RunRequest{
		Model:           cfg.Model,
		Prompt:          prompt,
		SessionKey:      sessionKey,
		ThinkingEnabled: true, // deepseek thinking stays default-on (spec §6)
		ReasoningEffort: cfg.ReasoningEffort,
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
		MaxTurns:    cfg.MaxTurns,
		CanContinue: canContinue,
		// Session compaction (T10): auto-compact at the configured fraction;
		// a [compacted] marker is surfaced read-only on each event (spec §7).
		Compaction:  compaction,
		OnCompacted: func() { fmt.Fprint(os.Stderr, "[compacted]\n") },
	})
}

// providerTools maps the registry's definitions to provider Chat-Completions
// Tool objects via the single per-dialect serializer (provider.ReExpress, T5/#10):
// one canonical JSON-Schema per tool is re-expressed per dialect, never
// hand-copied per provider. Only the Chat dialect is emitted today — the engine
// and every current provider talk the Chat-Completions wire (§2 of the spec).
func providerTools(defs []tools.Definition) []provider.Tool {
	canonical := make([]provider.DialectDefinition, 0, len(defs))
	for _, d := range defs {
		canonical = append(canonical, provider.DialectDefinition{
			Name:        d.Name,
			Description: d.Description,
			Schema:      d.Parameters,
		})
	}
	return provider.ReExpress(canonical, provider.DialectChat).([]provider.Tool)
}

// ProviderKeyEnv is the environment variable holding the OpenCode Go API key.
// Per docs/research/opencode-endpoints.md, the OpenCode Go credential is
// delivered via this env var (no key material in config).
const ProviderKeyEnv = "OPENCODE_API_KEY"

// ProviderURLEnv optionally overrides the Chat-Completions endpoint Eitri
// talks to, for local testing and custom OpenAI-compatible endpoints (the
// latter formalized in T11). It defaults to OpenCode Go.
const ProviderURLEnv = "EITRI_PROVIDER_URL"

// buildProvider builds the provider the saved config selects via the shared
// factory (provider.FromConfig, T11): it honors cfg.Provider across TUI and
// batch and wires the Copilot non-interactive refresh + token persistence into
// the config file so a renewed device-flow session is reused by later runs.
func buildProvider(cfg config.Config, cfgPath string) (provider.Provider, error) {
	return provider.FromConfig(cfg, provider.ProviderEnv{
		OpenCodeKey:    os.Getenv(ProviderKeyEnv),
		OpenCodeURL:    os.Getenv(ProviderURLEnv),
		CopilotRefresh: copilotRefresh(http.DefaultClient),
		CopilotPersist: func(c config.CopilotConfig) error {
			cfg.Copilot = c
			return config.Save(cfg, cfgPath)
		},
	})
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
