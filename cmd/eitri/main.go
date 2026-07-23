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
	"github.com/glemsom/eitri/internal/history"
	"github.com/glemsom/eitri/internal/debug"

	runner "github.com/glemsom/eitri/internal/runner"
	"github.com/glemsom/eitri/internal/session"
	"github.com/glemsom/eitri/internal/skills"
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

	if *batchPrompt != "" {
		// Batch mode: headless, no UI session manager
		cmdTimeout := time.Duration(cfg.CommandTimeout)
		runCfg := runner.FromConfig(cfg, workspace, cmdTimeout)

		// Create debug recorder for HTTP trace capture even in batch mode
		debugRecorder := debug.NewRecorder(0) // default capacity 20

		runSvc := runner.NewRunService(runner.RunServiceDeps{
			DebugRecorder: debugRecorder,
		})
		runSvc.SetPersistAuth(nil)
		if _, err := runSvc.BatchRun(ctx, *batchPrompt, runCfg, os.Stdout); err != nil {
			if !errors.Is(err, context.Canceled) {
				fmt.Fprintf(os.Stderr, "Batch run failed: %v\n", err)

				// Capture conversation context from the runner before writing the dump
				convCtx := runSvc.LastBatchConversationContext()

				dumpDir, dumpErr := debug.WriteCrashDump(debug.DumpOptions{
					Error:       err.Error(),
					ErrorChain:  fmt.Sprintf("%+v", err),
					Stack:       string(runtimeDebug.Stack()),
					Version:     Version,
					ConfigSummary: debug.SanitizeConfig(cfg),
					RuntimeSummary: &debug.RuntimeSummary{
						UpSince:            processStartTime,
						ActiveRunCount:     1, // the batch run itself
						SessionCount:       0, // batch mode has no UI sessions
						RecordedHTTPTraces: debugRecorder.Count(),
					},
					ConversationContext: convCtx,
					FailingHTTPTrace:    debugRecorder.LastFailingTrace(),
					Traces:         debugRecorder.List(0, "", ""),
					InFlightTraces: debugRecorder.InFlight(),
					Logs:           logBuffer.Entries(),
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
	sessionMgr := session.NewManager(10)
	historyMgr := history.NewSessionManager(cfg.MaxHistory)
	runSvc := runner.NewRunService(runner.RunServiceDeps{
		UISessionMgr:      sessionMgr,
		HistorySessionMgr: historyMgr,
		DebugRecorder:     debugRecorder,
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
		var cfgSummary map[string]interface{}
		if crashCfg != nil {
			cfgSummary = debug.SanitizeConfig(crashCfg)
		}
		dumpDir, dumpErr := debug.WriteCrashDump(debug.DumpOptions{
			Error:       err.Error(),
			ErrorChain:  fmt.Sprintf("%+v", err),
			Stack:       string(stack),
			Version:     Version,
			ConfigSummary: cfgSummary,
			RuntimeSummary: &debug.RuntimeSummary{
				UpSince:            processStartTime,
				ActiveRunCount:     runSvc.ActiveRunCount(),
				SessionCount:       len(allSessions),
				RecordedHTTPTraces: debugRecorder.Count(),
			},
			Sessions:       allSessions,
			FailingHTTPTrace:    debugRecorder.LastFailingTrace(),
			Traces:         debugRecorder.List(0, "", ""),
			InFlightTraces: debugRecorder.InFlight(),
			Logs:           logBuffer.Entries(),
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
