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

	// Restore calibration data from disk so per-model chars-per-token ratios
	// survive restarts. An absent or empty file falls back to current defaults.
	calPath := calibrationStorePath()
	if calPath != "" {
		if err := calStore.Load(calPath); err != nil {
			slog.Warn("failed to load calibration data", slog.String("path", calPath), slog.Any("error", err))
		} else if calStore.Count() > 0 {
			slog.Info("restored calibration data", slog.Int("models", calStore.Count()), slog.String("path", calPath))
		}
	}

	if *batchPrompt != "" {
		// Batch mode: headless, no UI session manager
		cmdTimeout := time.Duration(cfg.CommandTimeout)
		runCfg := runner.FromConfig(cfg, workspace, cmdTimeout)
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
		persister, pErr := persist.New(os.Getenv("EITRI_DIR"))
		if pErr != nil {
			slog.Warn("failed to create persister for batch mode", slog.Any("error", pErr))
			persister = nil
		}
		if persister != nil {
			p := persister
			debugRecorder.OnComplete = func(trace *debug.HTTPTrace) {
				p.SaveTraceAsync(trace.SessionID, trace)
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

				// Preserve any calibration observations collected during the
				// failed batch run before exiting.
				saveCalibration(calStore, calPath)

				// Drain the async trace-persistence queue before exiting so
				// failure-path traces reach disk under the batch session's
				// traces/ instead of being dropped (issue #1039). Today the
				// failure path exits without draining, silently losing any
				// queued traces.
				if persister != nil {
					_ = persister.Flush(nil, nil)
				}
				os.Exit(1)
			}
		}

		// Drain the async trace-persistence queue so batch-mode traces reach
		// disk before the process exits.
		if persister != nil {
			_ = persister.Flush(nil, nil)
		}
		saveCalibration(calStore, calPath)
		return
	}

	addr := os.Getenv("EITRI_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}

	debugRecorder := debug.NewRecorder(0) // default capacity 20

	// Create persister for session snapshot and trace persistence
	persister, pErr := persist.New(os.Getenv("EITRI_DIR"))
	if pErr != nil {
		slog.Warn("failed to create persister", slog.Any("error", pErr))
		persister = nil
	}
	if persister != nil {
		p := persister
		debugRecorder.OnComplete = func(trace *debug.HTTPTrace) {
			p.SaveTraceAsync(trace.SessionID, trace)
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

	// Persist calibration observations collected during this run so the
	// per-model chars-per-token ratios survive the next restart.
	saveCalibration(calStore, calPath)

	cleanupRuntime(server, runSvc)
	if err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// HTTP server hardening constants (issue #966).
const (
	// serverReadHeaderTimeout bounds how long the server waits for a request's
	// headers before closing the connection. A client that connects and sends a
	// partial header (slow-loris) is reaped within this window instead of
	// pinning a goroutine and socket indefinitely.
	serverReadHeaderTimeout = 5 * time.Second
	// serverIdleTimeout bounds how long a keep-alive connection may sit idle
	// between requests before being closed, so dead connections are reaped
	// instead of slowly accumulating.
	serverIdleTimeout = 60 * time.Second
	// serverMaxHeaderBytes caps the size of a single request header block.
	serverMaxHeaderBytes = 1 << 20 // 1 MiB
)

// newHTTPServer builds the HTTP server used for the main listener with sane
// connection limits so stalled or hostile clients are closed promptly while
// long-lived SSE streams keep working exactly as before.
//
// ReadTimeout and WriteTimeout are deliberately left at zero: SSE streams (the
// chat stream and the browser events stream) legitimately outlive a single
// request body read or response write. A WriteTimeout would kill a
// slow-consumer's stream, and a ReadTimeout would pin the connection's read
// deadline for the whole streaming lifetime. ReadHeaderTimeout, IdleTimeout and
// MaxHeaderBytes cover the slow-loris and dead-keepalive cases without
// touching streaming responses, which instead rely on their own keep-alive
// tickers and the request context.
func newHTTPServer(handler http.Handler) *http.Server {
	return &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: serverReadHeaderTimeout,
		IdleTimeout:       serverIdleTimeout,
		MaxHeaderBytes:    serverMaxHeaderBytes,
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

	return serveWithListener(ctx, opts, listener)
}

// serveWithListener runs the HTTP server on an already-bound listener. Split
// out of serve so tests can pre-bind a listener and know the exact address.
func serveWithListener(ctx context.Context, opts serveOptions, listener net.Listener) error {
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

	httpServer := newHTTPServer(opts.Handler)
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

// calibrationStorePath returns the on-disk location for calibration data under
// the Eitri data dir (EITRI_DIR, defaulting to ~/.eitri), mirroring the
// persister's root-dir resolution. Returns "" when the home directory cannot
// be determined, in which case calibration persistence is skipped.
func calibrationStorePath() string {
	dir := os.Getenv("EITRI_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".eitri")
	}
	return filepath.Join(dir, "calibration.json")
}

// saveCalibration persists the calibration store to disk. It is called on
// shutdown (server mode) and at the end of batch runs so per-model
// chars-per-token observations survive restarts. Failures are logged, never
// fatal.
func saveCalibration(store *tokenizer.CalibrationStore, path string) {
	if store == nil || path == "" {
		return
	}
	if err := store.Save(path); err != nil {
		slog.Warn("failed to save calibration data", slog.String("path", path), slog.Any("error", err))
		return
	}
	slog.Info("calibration data saved", slog.String("path", path))
}

func openBrowserURL(url string) error {
	cmd := exec.Command("xdg-open", url)
	// Detach xdg-open into its own process group so a SIGINT/SIGTERM to the
	// foreground group (e.g. Ctrl+C) doesn't kill the freshly-spawned browser.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
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
