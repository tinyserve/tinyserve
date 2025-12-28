package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"tinyserve/internal/api"
	"tinyserve/internal/state"
	"tinyserve/webui"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("tinyserved exited: %v", err)
	}
}

func run() error {
	ctx := context.Background()

	dataDir, err := ensureDataDir()
	if err != nil {
		return err
	}

	store := state.NewFileStore(filepath.Join(dataDir, "state.json"))
	initialState, err := store.Load(ctx)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}
	if err := store.Save(ctx, initialState); err != nil {
		return fmt.Errorf("init state: %w", err)
	}

	generatedRoot := filepath.Join(dataDir, "generated")
	backupsDir := filepath.Join(dataDir, "backups")

	handler := api.NewHandler(store, generatedRoot, backupsDir, filepath.Join(dataDir, "state.json"))
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	mux.Handle("/", webui.Handler())

	server := &http.Server{
		Addr:    "127.0.0.1:7070",
		Handler: mux,
	}

	log.Printf("tinyserved listening on %s (state: %s)", server.Addr, filepath.Join(dataDir, "state.json"))

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
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
