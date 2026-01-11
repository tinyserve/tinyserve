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

	"tinyserve/internal/api"
	"tinyserve/internal/state"
	"tinyserve/internal/version"
	"tinyserve/webui"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "--version", "-v":
			fmt.Println(version.String())
			return
		}
	}
	if err := run(); err != nil {
		log.Fatalf("tinyserved exited: %v", err)
	}
}

func run() error {
	log.Printf("starting %s", version.String())

	// Set up signal handling for graceful shutdown
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	dataDir, err := ensureDataDir()
	if err != nil {
		return err
	}

	store, err := state.NewSQLiteStore(filepath.Join(dataDir, "state.db"))
	if err != nil {
		return fmt.Errorf("open state db: %w", err)
	}
	defer store.Close()

	initialState, err := store.Load(ctx)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}
	if err := store.Save(ctx, initialState); err != nil {
		return fmt.Errorf("init state: %w", err)
	}

	generatedRoot := filepath.Join(dataDir, "generated")
	backupsDir := filepath.Join(dataDir, "backups")
	cloudflaredDir := filepath.Join(dataDir, "cloudflared")

	handler := api.NewHandler(store, generatedRoot, backupsDir, filepath.Join(dataDir, "state.db"), cloudflaredDir)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	mux.Handle("/", webui.Handler())

	server := &http.Server{
		Addr:    "127.0.0.1:7070",
		Handler: mux,
	}

	// Start server in goroutine
	errChan := make(chan error, 1)
	go func() {
		log.Printf("tinyserved listening on %s (state: %s)", server.Addr, filepath.Join(dataDir, "state.db"))
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errChan <- err
		}
		close(errChan)
	}()

	// Wait for shutdown signal or server error
	select {
	case <-ctx.Done():
		log.Println("shutting down gracefully...")
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer shutdownCancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
		log.Println("shutdown complete")
		return nil
	case err := <-errChan:
		return err
	}
}

func ensureDataDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dataDir := filepath.Join(home, "Library", "Application Support", "tinyserve")

	subdirs := []string{
		"generated",
		"backups",
		"logs",
		"services",
		"traefik",
		"cloudflared",
	}

	for _, dir := range subdirs {
		if err := os.MkdirAll(filepath.Join(dataDir, dir), 0o755); err != nil {
			return "", err
		}
	}

	return dataDir, nil
}
