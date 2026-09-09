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
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/term"

	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/constants"
	"github.com/glemsom/eitri/internal/engine"
	"github.com/glemsom/eitri/internal/engine/skillspack"
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

// DefaultFormat is the batch-mode stdout format when none is given: the
// byte-identical pre-feature behavior, a streamed final answer only.
const DefaultFormat = "text"

// ValidateFormat reports whether format is a supported batch output format.
// The empty string is accepted as the default (text). Unknown values are
// rejected before any provider boot or session creation.
func ValidateFormat(format string) error {
	if format != "" && format != "text" && format != "json" {
		return fmt.Errorf("unknown --format %q (want: text, json)", format)
	}
	return nil
}

// Options control a single Run invocation.
type Options struct {
	Version bool

	DataDir string

	ConfigPath string

	Debug bool

	Prompt string

	// Format selects the batch-mode stdout output: "text" (default, streamed
	// final answer only) or "json" (a single machine-parseable envelope).
	Format string

	Verbose bool

	Yolo bool

	// Stdin is the stream batch mode consumes as piped context and appends to
	// the -b prompt as a fenced block. Nil falls back to host stdin, which is
	// only read when it is not an interactive terminal; terminal input is never
	// drained.
	Stdin io.Reader

	Stdout io.Writer

	// Stderr is where batch mode streams the model's thinking under -v. Nil
	// falls back to host stderr, keeping stdout parseable under --format json.
	Stderr io.Writer

	Provider provider.Provider

	LookPath func(name string) (string, error)

	Browser tools.BrowserLauncher

	Pprof PprofOptions
}

// batchSignalContext returns a context cancelled by the first SIGINT/SIGTERM
// received by the process, plus a stop function. It is a package var so tests
// can drive the graceful-stop envelope path deterministically instead of
// signalling the test process.
var batchSignalContext = func() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// Run performs the Eitri boot sequence and returns the first error it hits, so a caller can map it to an exit status.
func Run(opts Options) error {
	if opts.Version {
		fmt.Println(Version)
		return nil
	}
	if err := ValidateFormat(opts.Format); err != nil {
		return err
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
	if err := checkDependencies(lookPath, opts.Yolo); err != nil {
		return err
	}

	tempHost := sess.TempDir()
	workspace, err := os.Getwd()
	if err != nil {
		return err
	}
	// Materialize the embedded builtin skill packs to <dir>/skills-builtin before
	// discovery so the normal skill roots can span them. The dir is binary-owned
	// ROM: rewrites happen on content mismatch and edits are reverted by design;
	// an unwritable $EITRI_DIR is warn-and-skip, boot continues without builtin
	// skills.
	skillspack.Materialize(dir, skillspack.FS, stderrWarner{}.Warnf)
	skills := discoverSkills(dir, workspace)
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
		// Piped stdin into an interactive launch would be silently drained by a
		// TUI that never reads it; refuse with a pointer at batch mode instead.
		if opts.Stdin != nil || !stdinIsTerminal() {
			return ErrStdinWithoutBatch
		}
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

	prompt := opts.Prompt
	if prompt != "" {
		var err error
		if prompt, err = withStdinContext(prompt, stdinSource(opts.Stdin)); err != nil {
			return err
		}
	}
	// Batch runs bind the process's interrupt signals to the run context: the
	// first SIGINT/SIGTERM cancels the turn gracefully (the run stops, the json
	// envelope still flushes with stopped:true, and the session-temp defer runs),
	// while a second signal hard-exits for a user who is not waiting on the
	// graceful path.
	ctx, stop := batchSignalContext()
	defer stop()
	go func() {
		<-ctx.Done() // the first signal is already being handled gracefully
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
		<-ch // the next signal means graceful shutdown is not fast enough
		os.Exit(1)
	}()

	res, err := runAgent(ctx, e, cfg, reg, key, prompt, skills, nil, nil)
	if err != nil && !errors.Is(err, engine.ErrStopped) {
		return err
	}
	out := opts.Stdout
	if out == nil {
		out = os.Stdout
	}
	errOut := opts.Stderr
	if errOut == nil {
		errOut = os.Stderr
	}
	if opts.Verbose && res.Reasoning != "" {
		fmt.Fprintf(errOut, "‹thinking›\n%s\n‹/thinking›\n", res.Reasoning)
	}
	format := opts.Format
	if format == "" {
		format = DefaultFormat
	}
	if err != nil {
		// Graceful stop (SIGINT/SIGTERM): honor the json envelope contract with
		// stopped:true so a script can tell an interrupted run from a failure; a
		// text run emits no partial answer. Both return nil so the session-temp
		// defer above runs before exit.
		if format == "json" {
			return writeBatchEnvelope(out, sess.GUID(), res)
		}
		return nil
	}
	if format == "json" {
		return writeBatchEnvelope(out, sess.GUID(), res)
	}
	if batches := skills.SkippedSkills(); len(batches) > 0 {
		// The lenient discovery drop is only safe if it is audible: give the
		// batch text run an explicit notice (on stderr, so the --format text
		// stdout answer stays byte-stable) naming the skipped packs.
		names := make([]string, 0, len(batches))
		for _, b := range batches {
			names = append(names, b.Name)
		}
		fmt.Fprintf(errOut, "eitri: warning: %d skill pack(s) skipped (unparseable SKILL.md): %s\n", len(names), strings.Join(names, ", "))
	}
	fmt.Fprintln(out, res.Answer)

	return nil
}

// batchEnvelope is the single machine-parseable object --format json prints at
// run end: the answer plus run metadata a script needs that exit codes alone
// don't carry (turns, stopped).
type batchEnvelope struct {
	Answer  string `json:"answer"`
	Session string `json:"session"`
	Turns   int    `json:"turns"`
	Stopped bool   `json:"stopped"`
}

// writeBatchEnvelope marshals the run result into one JSON object and writes it
// as the final stdout output.
func writeBatchEnvelope(out io.Writer, sessionGUID string, res engine.Result) error {
	// Marshal cannot fail for a struct of string/int/bool fields; only the write
	// can.
	env, _ := json.Marshal(batchEnvelope{
		Answer:  res.Answer,
		Session: sessionGUID,
		Turns:   res.Turns,
		Stopped: res.Stopped,
	})
	_, err := fmt.Fprintln(out, string(env))
	return err
}

// discoverSkills discovers Agent Skill packs from the builtin root
// (<dataDir>/skills-builtin, materialized from the binary), the user-global
// ~/.agents/skills root, and the project .agents/skills root under workspace
// (project shadows user, and user shadows builtin on exact-name collision).
func discoverSkills(dataDir, workspace string) *tools.Catalog {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "eitri: resolve home for skill discovery: %v\n", err)
		return &tools.Catalog{}
	}
	c, err := tools.Discover(
		filepath.Join(home, ".agents", "skills"),
		filepath.Join(workspace, ".agents", "skills"),
		filepath.Join(dataDir, skillspack.BuiltinRootName),
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

// runAgent drives one agent turn (user prompt → assistant answer) over the shared run engine, session transcript, and tool registry that both the TUI and batch use. The model-visible index and the per-run workspace directive are each carried as their own system message so they reach the model without perturbing the byte-stable system prompt; a catalog with none renders to a nil index that keeps the no-index wire bytes intact. ctx is threaded through to the engine so the TUI's per-turn cancellation (Ctrl+C/Esc) and batch's interrupt binding (SIGINT/SIGTERM cancel the run) both reach an in-flight run.
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
				return engine.ToolExecResult{Text: res.Text, Compressed: res.Compressed, Dropped: res.Dropped, BytesDropped: res.BytesDropped}, err
			}
			return engine.ToolExecResult{Text: res.Text, Compressed: res.Compressed, Dropped: res.Dropped, BytesDropped: res.BytesDropped}, nil
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
