package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/glemsom/eitri/internal/api"
	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/executor"
	agentrunner "github.com/glemsom/eitri/internal/runner"
	"github.com/glemsom/eitri/internal/session"
	"github.com/glemsom/eitri/internal/skills"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// 1. Tmux audit
	if err := executor.RunAudit(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// 2. Resolve workspace (process CWD)
	workspace, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get workspace: %v\n", err)
		os.Exit(1)
	}

	// 3. Resolve listen address
	addr := os.Getenv("EITRI_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}

	// 4. Print startup info
	fmt.Printf("Workspace: %s\n", workspace)
	fmt.Printf("Listening on http://%s\n", addr)

	// 5. Create config path
	configPath := os.Getenv("EITRI_CONFIG")
	if configPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Cannot determine home directory: %v\n", err)
			os.Exit(1)
		}
		configPath = filepath.Join(home, ".eitri", "config.json")
	}
	// 6. Load config
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Config: provider=%s, model=%s\n", cfg.Provider, cfg.Model)

	// 7. Create session manager
	sessionMgr := session.NewManager(10)

	// 8. Create runner manager + run manager
	runnerMgr := agentrunner.NewManager()
	executorMgr := executor.NewSessionManager(workspace, time.Duration(cfg.CommandTimeout), time.Duration(cfg.SessionTimeout))
	runMgr := api.NewRunManager(runnerMgr, executorMgr)
	runMgr.UpdateProviderConfig(cfg)

	// 9. Create skills service
	skillsSvc := skills.NewService()

	// 9a. Wire skills service to run manager
	runMgr.SetSkillsService(skillsSvc)

	// 10. Create HTTP server
	srvCfg := api.ServerConfig{
		ConfigPath:     configPath,
		Workspace:      workspace,
		SessionManager: sessionMgr,
		RunManager:     runMgr,
		SkillsService:  skillsSvc,
	}
	server := api.NewServer(srvCfg)

	httpServer := &http.Server{
		Addr:    addr,
		Handler: server.Handler(),
	}

	// 8. Start HTTP server in background
	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	// 9. Wait for shutdown signal
	<-ctx.Done()
	fmt.Println("\nShutting down...")

	// 10. Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	defer shutdownCancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("HTTP shutdown error: %v", err)
	}
	fmt.Println("Server stopped.")
}
