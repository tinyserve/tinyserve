package state

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestNewState(t *testing.T) {
	s := NewState()

	if s.Settings.ComposeProjectName != "tinyserve" {
		t.Errorf("expected compose project name 'tinyserve', got %q", s.Settings.ComposeProjectName)
	}
	if s.Settings.UILocalPort != 7070 {
		t.Errorf("expected UI port 7070, got %d", s.Settings.UILocalPort)
	}
	if s.Settings.Tunnel.Mode != TunnelModeToken {
		t.Errorf("expected tunnel mode 'token', got %q", s.Settings.Tunnel.Mode)
	}
	if len(s.Services) != 0 {
		t.Errorf("expected empty services, got %d", len(s.Services))
	}
	if s.CreatedAt.IsZero() {
		t.Error("expected CreatedAt to be set")
	}
}

func TestStateValidate(t *testing.T) {
	tests := []struct {
		name    string
		state   State
		wantErr bool
	}{
		{
			name:    "valid state",
			state:   NewState(),
			wantErr: false,
		},
		{
			name: "missing compose project name",
			state: State{
				Settings: GlobalSettings{ComposeProjectName: ""},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.state.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestStateTouch(t *testing.T) {
	s := NewState()
	original := s.UpdatedAt

	time.Sleep(10 * time.Millisecond)
	s.Touch()

	if !s.UpdatedAt.After(original) {
		t.Error("Touch() should update UpdatedAt to a later time")
	}
}

func TestInMemoryStore(t *testing.T) {
	ctx := context.Background()
	initial := NewState()
	store := NewInMemoryStore(initial)

	// Load should return initial state
	loaded, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.Settings.ComposeProjectName != initial.Settings.ComposeProjectName {
		t.Error("Load() returned different state than initial")
	}

	// Save should update state
	loaded.Settings.DefaultDomain = "example.com"
	if err := store.Save(ctx, loaded); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Load again should reflect changes
	reloaded, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("Load() after Save() error = %v", err)
	}
	if reloaded.Settings.DefaultDomain != "example.com" {
		t.Error("Save() did not persist changes")
	}
}

func TestInMemoryStoreConcurrency(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryStore(NewState())

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = store.Load(ctx)
		}()
		go func(n int) {
			defer wg.Done()
			s, _ := store.Load(ctx)
			s.Settings.UILocalPort = n
			_ = store.Save(ctx, s)
		}(i)
	}
	wg.Wait()
	// If we get here without race detector complaints, concurrency is handled
}

func TestFileStore(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "tinyserve-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	statePath := filepath.Join(tmpDir, "state.json")
	store := NewFileStore(statePath)
	ctx := context.Background()

	// Load from non-existent file should return new state
	s, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("Load() from missing file error = %v", err)
	}
	if s.Settings.ComposeProjectName != "tinyserve" {
		t.Error("Load() from missing file should return default state")
	}

	// Save and reload
	s.Settings.DefaultDomain = "test.example.com"
	s.Services = append(s.Services, Service{
		ID:           "test-123",
		Name:         "test",
		Image:        "nginx:latest",
		InternalPort: 80,
		Enabled:      true,
	})

	if err := store.Save(ctx, s); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(statePath); os.IsNotExist(err) {
		t.Error("Save() did not create state file")
	}

	// Reload and verify
	reloaded, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("Load() after Save() error = %v", err)
	}
	if reloaded.Settings.DefaultDomain != "test.example.com" {
		t.Error("Load() did not restore DefaultDomain")
	}
	if len(reloaded.Services) != 1 || reloaded.Services[0].Name != "test" {
		t.Error("Load() did not restore services")
	}
}

func TestFileStoreAtomicWrite(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tinyserve-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	statePath := filepath.Join(tmpDir, "state.json")
	store := NewFileStore(statePath)
	ctx := context.Background()

	// Initial save
	s := NewState()
	s.Settings.DefaultDomain = "first.com"
	if err := store.Save(ctx, s); err != nil {
		t.Fatalf("first Save() error = %v", err)
	}

	// Second save should not leave temp files
	s.Settings.DefaultDomain = "second.com"
	if err := store.Save(ctx, s); err != nil {
		t.Fatalf("second Save() error = %v", err)
	}

	// Check no .tmp files left behind
	entries, _ := os.ReadDir(tmpDir)
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}

func TestServiceDefaults(t *testing.T) {
	svc := Service{
		Name:         "myapp",
		Image:        "myapp:latest",
		InternalPort: 8080,
	}

	// Check that zero values are as expected
	if svc.Enabled != false {
		t.Error("Service.Enabled should default to false (zero value)")
	}
	if svc.Resources.MemoryLimitMB != 0 {
		t.Error("Service.Resources.MemoryLimitMB should default to 0")
	}
}

func TestTunnelModes(t *testing.T) {
	if TunnelModeToken != "token" {
		t.Errorf("TunnelModeToken = %q, want 'token'", TunnelModeToken)
	}
	if TunnelModeCredentialsFile != "credentials_file" {
		t.Errorf("TunnelModeCredentialsFile = %q, want 'credentials_file'", TunnelModeCredentialsFile)
	}
}
