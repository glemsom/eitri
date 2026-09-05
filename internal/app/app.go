// Package app drives the Eitri boot sequence: resolving the data directory, checking the declared dependency toolset, and wiring flag-driven behavior.
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
	"strings"

	"golang.org/x/term"

	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/constants"
	"github.com/glemsom/eitri/internal/engine"
	"github.com/glemsom/eitri/internal/provider"
	"github.com/glemsom/eitri/internal/session"
	"github.com/glemsom/eitri/internal/tools"
)

// Version reports the Eitri build version tag, set at build time.
var Version = "0.1.0-dev"

// Environment variables honored at boot.
const (
	DataDirEnv = "EITRI_DIR"
	ConfigEnv  = "EITRI_CONFIG"
)

// ErrTUINotInteractive is returned when the interactive TUI cannot render into the host terminal — stdout is not a TTY, TERM is unset or "dumb", or the window is below the minimum width.
var ErrTUINotInteractive = errors.New("the interactive TUI requires an interactive terminal: stdout must be a TTY, TERM must be set (not \"dumb\"), and the window must be at least 80 columns wide; run in batch mode instead: eitri -b \"<prompt>\"")

// minTUIWidth is the narrowest terminal (in columns) the full-screen TUI renders into; below it the transcript is squeezed unusably, so the TUI is refused in favor of batch mode.
const minTUIWidth = constants.MinTUIWidth

// tuiEnv captures the host-terminal facts the TUI boot guard reads. width is the terminal width in columns; 0 means unknown.
type tuiEnv struct {
	stdoutTTY bool
	term      string
	width     int
}

// currentTUIEnv reads the host-terminal facts from os.Stdout, TERM, and the terminal size.
var currentTUIEnv = func() tuiEnv {
	fi, err := os.Stdout.Stat()
	stdoutTTY := err == nil && fi.Mode()&os.ModeCharDevice != 0
	width := 0
	if stdoutTTY {
		if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
			width = w
		}
	}
	return tuiEnv{stdoutTTY: stdoutTTY, term: os.Getenv("TERM"), width: width}
}

// tuiBootError decides whether the interactive TUI can render into the host context: nil when it can, ErrTUINotInteractive when stdout is not a TTY, TERM is unset or a dumb terminfo (any case, incl. dumb-* variants), or the window is narrower than minTUIWidth.
func tuiBootError(env tuiEnv) error {
	switch {
	case !env.stdoutTTY:
		return fmt.Errorf("%w: stdout is not an interactive terminal (output piped?)", ErrTUINotInteractive)
	case isDumbTerm(env.term):
		return fmt.Errorf("%w: TERM is %q; a real terminal emulator is required", ErrTUINotInteractive, env.term)
	case env.width > 0 && env.width < minTUIWidth:
		return fmt.Errorf("%w: terminal is %d columns wide; %d are required", ErrTUINotInteractive, env.width, minTUIWidth)
	}
	return nil
}

// isDumbTerm reports whether TERM denotes a non-interactive termcap: unset, "dumb", or a dumb-* variant (e.g. dumb-16color), case-insensitively.
func isDumbTerm(term string) bool {
	lower := strings.ToLower(term)
	return lower == "" || lower == "dumb" || strings.HasPrefix(lower, "dumb-")
}

// Options control a single Run invocation.
type Options struct {
	Version bool

	DataDir string

	ConfigPath string

	Debug bool

	Prompt string

	Verbose bool

	Yolo bool

	Stdout io.Writer

	Provider provider.Provider

	LookPath func(name string) (string, error)

	Browser tools.BrowserLauncher

	Pprof PprofOptions
}

// Run performs the Eitri boot sequence and returns the first error it hits, so a caller can map it to an exit status.
func Run(opts Options) error {
	if opts.Version {
		fmt.Println(Version)
		return nil
	}
	if err := startPprof(opts.Pprof); err != nil {
		return err
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

	sess, err := session.New(dir, opts.Debug)
	if err != nil {
		return err
	}

	lookPath := opts.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	if err := checkDependencies(lookPath); err != nil {
		return err
	}

	tempHost := sess.TempDir()
	workspace, err := os.Getwd()
	if err != nil {
		return err
	}
	skills := discoverSkills(workspace)
	defer func() { _ = os.RemoveAll(tempHost) }()
	reg, err := tools.NewRegistry(tools.Deps{
		Workspace:     workspace,
		TempHost:      tempHost,
		ExtraWritable: cfg.ExtraWritablePaths,
		Runner:        tools.RealRunner,
		Yolo:          opts.Yolo,
		Browser:       opts.Browser,
		Skills:        skills,
	})
	if err != nil {
		return err
	}
	if opts.Prompt == "" {
		if err := tuiBootError(currentTUIEnv()); err != nil {
			return err
		}
	}

	p := opts.Provider
	if p == nil {
		var err error
		p, err = buildProvider(cfg, cfgPath)
		if err != nil {
			return err
		}
	}
	liveProvider := newHotProvider(p)
	// Message-layer debug transcript: every request/response cycle the engine sees is mirrored to messages.jsonl.
	logged := provider.NewLoggingProvider(liveProvider, sess.MessageLogSink())
	e := engine.New(logged, sess)
	key := sess.GUID() // opt into the session-scoped prompt cache

	if opts.Prompt == "" {
		if _, err := e.ResolveCompaction(context.Background(), cfg.ContextOverflowRecovery); err != nil {
			return fmt.Errorf("configure context overflow recovery: %w", err)
		}
		return runTUI(e, logged, cfg, reg, key, liveProvider, cfgPath, dir, skills, workspace, tempHost)
	}

	res, err := runAgent(context.Background(), e, cfg, reg, key, opts.Prompt, skills, nil, nil)
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

// discoverSkills discovers Agent Skill packs from the user-global ~/.agents/skills root and the project .agents/skills root under workspace (project shadows user on exact-name collision).
func discoverSkills(workspace string) *tools.Catalog {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "eitri: resolve home for skill discovery: %v\n", err)
		return &tools.Catalog{}
	}
	c, err := tools.Discover(
		filepath.Join(home, ".agents", "skills"),
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

// runAgent drives one agent turn (user prompt → assistant answer) over the shared run engine, session transcript, and tool registry that both the TUI and batch use. The model-visible index and the per-run workspace directive are each carried as their own system message so they reach the model without perturbing the byte-stable system prompt; a catalog with none renders to a nil index that keeps the no-index wire bytes intact. ctx is threaded through to the engine so the TUI's per-turn cancellation (Ctrl+C/Esc) reaches an in-flight run; batch passes context.Background() (no stop binding).
func runAgent(ctx context.Context, e *engine.Engine, cfg config.Config, reg *tools.Registry, sessionKey, prompt string, catalog *tools.Catalog, skillInject *string, canContinue func() bool) (engine.Result, error) {
	compaction, err := e.ResolveCompaction(ctx, cfg.ContextOverflowRecovery)
	if err != nil {
		return engine.Result{}, fmt.Errorf("configure context overflow recovery: %w", err)
	}
	var skillIndex *string
	if catalog != nil {
		if idx := catalog.RenderIndex(); idx != "" {
			skillIndex = &idx
		}
	}
	repoInstructions := loadRepoInstructions(reg.Workspace())
	effort := cfg.ReasoningEffort
	if !cfg.ThinkingEnabled {
		effort = ""
	}
	return e.RunAgent(ctx, engine.RunRequest{
		Model:            cfg.Model,
		Prompt:           prompt,
		Workspace:        reg.Workspace(),
		SkillIndex:       skillIndex,
		RepoInstructions: repoInstructions,
		SkillInject:      skillInject,
		SessionKey:       sessionKey,
		ThinkingEnabled:  cfg.ThinkingEnabled,
		ReasoningEffort:  effort,
		ProviderID:       provider.ProviderID(cfg.Provider),
	}, engine.AgentOptions{
		Tools:      providerTools(reg.Definitions()),
		ToolChoice: "auto",
		Executor: engine.ExecutorFunc(func(ctx context.Context, name, argsJSON string) (engine.ToolExecResult, error) {
			var args map[string]any
			if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
				return engine.ToolExecResult{}, err
			}
			res, err := reg.Run(ctx, name, args)
			if err != nil {
				// Preserve any output the tool produced alongside its error; bash
				// returns combined stdout+stderr even on a non-zero exit, and
				// dropping it would rob the model of diagnostic context.
				return engine.ToolExecResult{Text: res.Text, Compressed: res.Compressed, Dropped: res.Dropped}, err
			}
			return engine.ToolExecResult{Text: res.Text, Compressed: res.Compressed, Dropped: res.Dropped}, nil
		}),
		MaxTurns:    cfg.MaxTurns,
		CanContinue: canContinue,
		Compaction:  compaction,
		OnCompacted: func() { fmt.Fprint(os.Stderr, "[context overflow: summarized older history and retried]\n") },
	})
}

// loadRepoInstructions reads the workspace-root AGENTS.md (if present) so its
// content can ride to the provider as the repository-instructions system message.
// A missing or unreadable file returns nil, leaving the wire request
// byte-identical to the no-instructions case — no opt-in, no escape hatch.
func loadRepoInstructions(workspace string) *string {
	b, err := os.ReadFile(filepath.Join(workspace, "AGENTS.md"))
	if err != nil {
		return nil
	}
	content := string(b)
	return &content
}

// providerTools maps the registry's definitions to provider Chat-Completions Tool objects via the dialect's tool-schema re-expression (provider.NewChatCompletionsDialect().Manifest): one canonical JSON-Schema per tool is re-expressed per dialect, never hand-copied per provider.
func providerTools(defs []tools.Definition) []provider.Tool {
	canonical := make([]provider.DialectDefinition, 0, len(defs))
	for _, d := range defs {
		canonical = append(canonical, provider.DialectDefinition{
			Name:        d.Name,
			Description: d.Description,
			Schema:      d.Parameters,
		})
	}
	return provider.NewChatCompletionsDialect().Manifest(canonical).([]provider.Tool)
}

// ProviderKeyEnv is the environment variable holding the OpenCode Go API key.
const ProviderKeyEnv = "OPENCODE_API_KEY"

// ProviderURLEnv optionally overrides the Chat-Completions endpoint Eitri talks to, for local testing and custom OpenAI-compatible endpoints.
const ProviderURLEnv = "EITRI_PROVIDER_URL"

// buildProvider builds the provider the saved config selects via the shared factory (provider.FromConfig): it honors cfg.Provider across TUI and batch and wires the Copilot non-interactive refresh + token persistence into the config file so a renewed device-flow session is reused by later runs.
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

// resolveConfigPath selects the config file path: an explicit override, else EITRI_CONFIG, else <dataDir>/config.json.
func resolveConfigPath(dataDir, explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if env := os.Getenv(ConfigEnv); env != "" {
		return env, nil
	}
	return filepath.Join(dataDir, "config.json"), nil
}

// resolveDataDir selects the data directory: the explicit override, else EITRI_DIR, else ~/.eitri.
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
