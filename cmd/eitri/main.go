package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	runtimeDebug "runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/glemsom/eitri/internal/api"
	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/debug"
	"github.com/glemsom/eitri/internal/history"
	"github.com/glemsom/eitri/internal/persist"
	"github.com/glemsom/eitri/internal/persona"
	"github.com/glemsom/eitri/internal/sandbox"

	runner "github.com/glemsom/eitri/internal/runner"
	"github.com/glemsom/eitri/internal/runner/runconfig"
	"github.com/glemsom/eitri/internal/session"
	"github.com/glemsom/eitri/internal/skills"
	"github.com/glemsom/eitri/internal/tokenizer"
)

type serveOptions struct {
	Addr      string
	Workspace string
	Handler   http.Handler
	Stdout    io.Writer
	Stderr    io.Writer
	Getenv    func(string) string
	OpenURL   func(string) error
}

func cleanupRuntime(server *api.Server, runSvc *runner.RunService) {
	if server != nil {
		server.CloseActiveStreams("Server shutting down")
	}
	if runSvc != nil {
		runSvc.CancelAll()
	}
}

// Version is set at build time via -ldflags -X main.Version=<version>.
// Default "dev" indicates an unversioned development build.
var Version = "dev"

// processStartTime records when the process started, used for computing real uptime
// in crash dumps.
var processStartTime = time.Now()

// logBuffer is the global ring buffer handler that captures log entries for crash dumps.
var logBuffer *debug.RingBufferHandler

func main() {
	inner := slog.NewJSONHandler(os.Stdout, nil)
	logBuffer = debug.NewRingBufferHandler(inner, 0) // default capacity 100
	slog.SetDefault(slog.New(logBuffer))

	versionFlag := flag.Bool("version", false, "Print version and exit")
	batchPrompt := flag.String("b", "", "Batch mode: run headless with the given prompt and stream output to stdout")
	personaFlag := flag.String("persona", "", "Persona name to use for batch run (overrides config active_persona)")
	flag.Parse()

	if *versionFlag {
		fmt.Println(Version)
		return
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	workspace, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get workspace: %v\n", err)
		os.Exit(1)
	}

	configPath := os.Getenv("EITRI_CONFIG")
	if configPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Cannot determine home directory: %v\n", err)
			os.Exit(1)
		}
		configPath = filepath.Join(home, ".eitri", "config.json")
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	// Prime the bwrap availability cache and log sandbox status.
	sandbox.BwrapAvailable()
	switch {
	case cfg.Sandbox.Profile == sandbox.ProfileNone:
		slog.Warn("sandbox: disabled in config — commands run without isolation")
	case !sandbox.BwrapAvailable():
		slog.Warn("bwrap sandbox: NOT available — commands running without isolation. Install bwrap for better security.")
	default:
		slog.Info("bwrap sandbox: enabled — commands run inside a sandbox")
	}

	// Ensure the generic persona exists on first run.
	// This is idempotent — safe to call every startup.
	if err := persona.EnsureGeneric(); err != nil {
		slog.Warn("failed to ensure generic persona", slog.Any("error", err))
	}

	// Create calibration store for per-model chars-per-token tracking.
	calStore := tokenizer.NewCalibrationStore()

	if *batchPrompt != "" {
		// Batch mode: headless, no UI session manager
		cmdTimeout := time.Duration(cfg.CommandTimeout)
		runCfg := runconfig.FromConfig(cfg, workspace, cmdTimeout)
		if *personaFlag != "" {
			// Validate persona exists before starting the batch run.
			// This produces a clear error for non-existent --persona before any
			// LLM connection is attempted.
			if _, err := persona.Load(workspace, *personaFlag); err != nil {
				fmt.Fprintf(os.Stderr, "Batch run failed: persona %q not found\n", *personaFlag)
				os.Exit(1)
			}
			runCfg.ActivePersona = *personaFlag
		}

		// Create debug recorder for HTTP trace capture even in batch mode
		debugRecorder := debug.NewRecorder(0) // default capacity 20

		// Create persister for trace persistence in batch mode
		persister, pErr := persist.New("")
		if pErr != nil {
			slog.Warn("failed to create persister for batch mode", slog.Any("error", pErr))
			persister = nil
		}
		if persister != nil {
			p := persister
			debugRecorder.OnComplete = func(trace *debug.HTTPTrace) {
				if err := p.SaveTrace(trace.SessionID, trace); err != nil {
					slog.Warn("failed to save trace", slog.String("trace_id", string(trace.ID)), slog.Any("error", err))
				}
			}
		}

		runSvc := runner.NewRunService(runner.RunServiceDeps{
			DebugRecorder:    debugRecorder,
			Persister:        persister,
			CalibrationStore: calStore,
		})
		runSvc.SetPersistAuth(nil)
		if _, err := runSvc.BatchRun(ctx, *batchPrompt, runCfg, os.Stdout); err != nil {
			if !errors.Is(err, context.Canceled) {
				fmt.Fprintf(os.Stderr, "Batch run failed: %v\n", err)

				// Capture conversation context from the runner before writing the dump
				convCtx := runSvc.LastBatchConversationContext()

				dumpDir, dumpErr := debug.WriteCrashDump(debug.DumpOptions{
					Error:         err.Error(),
					ErrorChain:    fmt.Sprintf("%+v", err),
					Stack:         string(runtimeDebug.Stack()),
					Version:       Version,
					ConfigSummary: debug.SanitizeConfig(cfg),
					RuntimeSummary: &debug.RuntimeSummary{
						UpSince:            processStartTime,
						ActiveRunCount:     1, // the batch run itself
						SessionCount:       0, // batch mode has no UI sessions
						RecordedHTTPTraces: debugRecorder.Count(),
					},
					SystemDiagnostics:   debug.CollectSystemDiagnostics(processStartTime),
					ConversationContext: convCtx,
					FailingHTTPTrace:    debugRecorder.LastFailingTrace(),
					Traces:              debugRecorder.List(0, "", ""),
					InFlightTraces:      debugRecorder.InFlight(),
					Logs:                logBuffer.Entries(),
				})
				if dumpErr != nil {
					fmt.Fprintf(os.Stderr, "Failed to write crash dump: %v\n", dumpErr)
				} else {
					fmt.Fprintf(os.Stderr, "Crash dump written to %s\n", dumpDir)
				}

				os.Exit(1)
			}
		}
		return
	}

	addr := os.Getenv("EITRI_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}

	debugRecorder := debug.NewRecorder(0) // default capacity 20

	// Create persister for session snapshot and trace persistence
	persister, pErr := persist.New("")
	if pErr != nil {
		slog.Warn("failed to create persister", slog.Any("error", pErr))
		persister = nil
	}
	if persister != nil {
		p := persister
		debugRecorder.OnComplete = func(trace *debug.HTTPTrace) {
			if err := p.SaveTrace(trace.SessionID, trace); err != nil {
				slog.Warn("failed to save trace", slog.String("trace_id", string(trace.ID)), slog.Any("error", err))
			}
		}
	}

	sessionMgr := session.NewManager(10, workspace)
	historyMgr := history.NewSessionManager(cfg.MaxHistory)

	// Restore persisted data from disk before starting the server
	if persister != nil {
		restored, rErr := persister.Restore()
		if rErr != nil {
			slog.Warn("failed to restore persisted data", slog.Any("error", rErr))
		} else {
			// Sessions are no longer restored into the session manager on startup.
			// They are still written to disk every turn for troubleshooting/debugging.
			slog.Info("restored sessions from disk", slog.Int("count", len(restored.Sessions)))

			// Hydrate history manager
			for id, msgs := range restored.Histories {
				historyMgr.RestoreHistory(id, msgs)
			}
			slog.Info("restored histories from disk", slog.Int("count", len(restored.Histories)))

			// Hydrate debug recorder
			debugRecorder.LoadAll(restored.Traces)
			if len(restored.Traces) > 0 {
				slog.Info("restored HTTP traces from disk", slog.Int("count", len(restored.Traces)))
			}
		}
	}

	runSvc := runner.NewRunService(runner.RunServiceDeps{
		UISessionMgr:      sessionMgr,
		HistorySessionMgr: historyMgr,
		DebugRecorder:     debugRecorder,
		Persister:         persister,
		CalibrationStore:  calStore,
	})

	skillsSvc := skills.NewService()
	if len(cfg.DisabledSkills) > 0 {
		skillsSvc.SetDisabledList(cfg.DisabledSkills, nil)
	}
	runSvc.SetSkillsService(skillsSvc)

	runSvc.SetCrashDumpFunc(func(err error, stack []byte) {
		crashCfg, cfgErr := config.Load(configPath)
		if cfgErr != nil {
			crashCfg = nil
		}
		allSessions := sessionMgr.All()
		var cfgSummary map[string]any
		if crashCfg != nil {
			cfgSummary = debug.SanitizeConfig(crashCfg)
		}
		dumpDir, dumpErr := debug.WriteCrashDump(debug.DumpOptions{
			Error:         err.Error(),
			ErrorChain:    fmt.Sprintf("%+v", err),
			Stack:         string(stack),
			Version:       Version,
			ConfigSummary: cfgSummary,
			RuntimeSummary: &debug.RuntimeSummary{
				UpSince:            processStartTime,
				ActiveRunCount:     runSvc.ActiveRunCount(),
				SessionCount:       len(allSessions),
				RecordedHTTPTraces: debugRecorder.Count(),
			},
			SystemDiagnostics: debug.CollectSystemDiagnostics(processStartTime),
			Sessions:          allSessions,
			FailingHTTPTrace:  debugRecorder.LastFailingTrace(),
			Traces:            debugRecorder.List(0, "", ""),
			InFlightTraces:    debugRecorder.InFlight(),
			Logs:              logBuffer.Entries(),
		})
		if dumpErr != nil {
			slog.Error("Failed to write crash dump", slog.String("error", dumpErr.Error()))
		} else {
			slog.Info("Crash dump written", slog.String("path", dumpDir))
		}
	})
	server := api.NewServer(api.ServerConfig{
		ConfigPath:     configPath,
		Workspace:      workspace,
		SessionManager: sessionMgr,
		RunService:     runSvc,
		SkillsService:  skillsSvc,
		Logger:         slog.Default(),
		Version:        Version,
		DebugRecorder:  debugRecorder,
		Persister:      persister,
		StartTime:      time.Now(),
	})

	err = serve(ctx, serveOptions{
		Addr:      addr,
		Workspace: workspace,
		Handler:   server.Handler(),
		Stdout:    os.Stdout,
		Stderr:    os.Stderr,
		Getenv:    os.Getenv,
		OpenURL:   openBrowserURL,
	})

	// Flush any pending data to disk before shutting down.
	if persister != nil {
		sessions := sessionMgr.All()
		traces := debugRecorder.List(0, "", "")
		inFlight := debugRecorder.InFlight()
		allTraces := append(traces, inFlight...)

		if err := persister.Flush(sessions, allTraces); err != nil {
			slog.Warn("flush on shutdown failed", slog.Any("error", err))
		} else {
			slog.Info("flush on shutdown completed")
		}
	}

	cleanupRuntime(server, runSvc)
	if err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func serve(ctx context.Context, opts serveOptions) error {
	if opts.Handler == nil {
		opts.Handler = http.NewServeMux()
	}
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}
	if opts.Getenv == nil {
		opts.Getenv = os.Getenv
	}
	if opts.OpenURL == nil {
		opts.OpenURL = openBrowserURL
	}

	listener, err := net.Listen("tcp", opts.Addr)
	if err != nil {
		return fmt.Errorf("Cannot bind %s: %v. Try EITRI_ADDR=127.0.0.1:8081 eitri", opts.Addr, err)
	}
	defer listener.Close()

	url := "http://" + listener.Addr().String()
	if isNonLoopbackBind(listener.Addr().String()) {
		fmt.Fprintf(opts.Stderr, "Warning: Eitri has no authentication and can execute host commands. Non-loopback bind exposes your machine.\n")
	}
	fmt.Fprintf(opts.Stdout, "Workspace: %s\n", opts.Workspace)
	fmt.Fprintf(opts.Stdout, "Listening on %s\n", url)

	if shouldOpenBrowser(opts.Getenv) {
		if err := opts.OpenURL(url); err != nil {
			slog.Warn("open browser failed", slog.String("url", url), slog.Any("error", err))
		}
	}

	httpServer := &http.Server{Handler: opts.Handler}
	serveErrCh := make(chan error, 1)
	go func() {
		if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErrCh <- err
		}
		close(serveErrCh)
	}()

	select {
	case err := <-serveErrCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

func shouldOpenBrowser(getenv func(string) string) bool {
	switch getenv("EITRI_OPEN_BROWSER") {
	case "1":
		return true
	case "0":
		return false
	}
	if getenv("CI") == "true" {
		return false
	}
	return getenv("DISPLAY") != "" || getenv("WAYLAND_DISPLAY") != ""
}

func openBrowserURL(url string) error {
	cmd := exec.Command("xdg-open", url)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() {
		_ = cmd.Wait()
	}()
	return nil
}

func isNonLoopbackBind(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return true
	}
	if host == "" {
		return true
	}
	if strings.EqualFold(host, "localhost") {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return true
	}
	return !ip.IsLoopback()
}
