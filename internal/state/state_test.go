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

func TestSQLiteStore(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tinyserve-sqlite-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "state.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Load from empty DB should return new state
	s, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("Load() from empty DB error = %v", err)
	}
	if s.Settings.ComposeProjectName != "tinyserve" {
		t.Error("Load() from empty DB should return default state")
	}

	// Save and reload
	s.Settings.DefaultDomain = "test.example.com"
	s.Settings.Tunnel.Mode = TunnelModeCredentialsFile
	s.Settings.Tunnel.TunnelID = "abc123"
	s.Services = append(s.Services, Service{
		ID:           "test-123",
		Name:         "test",
		Type:         ServiceTypeRegistryImage,
		Image:        "nginx:latest",
		InternalPort: 80,
		Enabled:      true,
		Hostnames:    []string{"test.example.com"},
		Env:          map[string]string{"FOO": "bar"},
		Volumes:      []string{"/data:/data"},
		Healthcheck: &ServiceHealthcheck{
			Command:         []string{"curl", "-f", "http://localhost/"},
			IntervalSeconds: 30,
			Retries:         3,
		},
		Resources: ServiceResources{MemoryLimitMB: 512},
	})

	if err := store.Save(ctx, s); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Reload and verify
	reloaded, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("Load() after Save() error = %v", err)
	}
	if reloaded.Settings.DefaultDomain != "test.example.com" {
		t.Error("Load() did not restore DefaultDomain")
	}
	if reloaded.Settings.Tunnel.Mode != TunnelModeCredentialsFile {
		t.Error("Load() did not restore Tunnel.Mode")
	}
	if reloaded.Settings.Tunnel.TunnelID != "abc123" {
		t.Error("Load() did not restore Tunnel.TunnelID")
	}
	if len(reloaded.Services) != 1 {
		t.Fatalf("Load() restored %d services, want 1", len(reloaded.Services))
	}

	svc := reloaded.Services[0]
	if svc.Name != "test" {
		t.Error("Load() did not restore service name")
	}
	if len(svc.Hostnames) != 1 || svc.Hostnames[0] != "test.example.com" {
		t.Errorf("Load() did not restore hostnames: %v", svc.Hostnames)
	}
	if svc.Env["FOO"] != "bar" {
		t.Error("Load() did not restore env")
	}
	if len(svc.Volumes) != 1 || svc.Volumes[0] != "/data:/data" {
		t.Error("Load() did not restore volumes")
	}
	if svc.Healthcheck == nil || len(svc.Healthcheck.Command) != 3 {
		t.Error("Load() did not restore healthcheck")
	}
	if svc.Resources.MemoryLimitMB != 512 {
		t.Error("Load() did not restore resources")
	}
}

func TestSQLiteStoreServiceDeletion(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tinyserve-sqlite-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "state.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Add two services
	s := NewState()
	s.Services = []Service{
		{ID: "svc1", Name: "one", Image: "img1", InternalPort: 80},
		{ID: "svc2", Name: "two", Image: "img2", InternalPort: 8080},
	}
	if err := store.Save(ctx, s); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Remove one service
	s.Services = []Service{
		{ID: "svc1", Name: "one", Image: "img1", InternalPort: 80},
	}
	if err := store.Save(ctx, s); err != nil {
		t.Fatalf("Save() after delete error = %v", err)
	}

	// Verify only one remains
	reloaded, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(reloaded.Services) != 1 {
		t.Errorf("expected 1 service after deletion, got %d", len(reloaded.Services))
	}
	if reloaded.Services[0].ID != "svc1" {
		t.Error("wrong service remained after deletion")
	}
}

func TestSQLiteStoreConcurrency(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tinyserve-sqlite-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "state.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
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
}

func TestSQLiteStoreValidation(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tinyserve-sqlite-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "state.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Try to save invalid state
	s := State{
		Settings: GlobalSettings{ComposeProjectName: ""},
	}
	err = store.Save(ctx, s)
	if err == nil {
		t.Error("Save() should reject invalid state")
	}
}

func TestSQLiteStoreCloudflareAndRemote(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tinyserve-sqlite-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "state.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Load initial state
	s, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if s.Settings.CloudflareAPIToken != "" {
		t.Error("expected empty CloudflareAPIToken initially")
	}
	if s.Settings.Remote.Enabled {
		t.Error("expected Remote.Enabled to be false initially")
	}

	// Set Cloudflare API token and remote settings
	s.Settings.CloudflareAPIToken = "test-cf-token-123"
	s.Settings.Remote.Enabled = true
	s.Settings.Remote.Hostname = "admin.example.com"
	s.Settings.Remote.BrowserAuth = BrowserAuthSettings{
		Type:       "cloudflare_access",
		TeamDomain: "example.cloudflareaccess.com",
		PolicyAUD:  "aud123",
	}

	if err := store.Save(ctx, s); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Reload and verify
	reloaded, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("Load() after Save() error = %v", err)
	}

	if reloaded.Settings.CloudflareAPIToken != "test-cf-token-123" {
		t.Errorf("CloudflareAPIToken = %q, want %q", reloaded.Settings.CloudflareAPIToken, "test-cf-token-123")
	}
	if !reloaded.Settings.Remote.Enabled {
		t.Error("Remote.Enabled = false, want true")
	}
	if reloaded.Settings.Remote.Hostname != "admin.example.com" {
		t.Errorf("Remote.Hostname = %q, want %q", reloaded.Settings.Remote.Hostname, "admin.example.com")
	}
	if reloaded.Settings.Remote.BrowserAuth.Type != "cloudflare_access" {
		t.Errorf("Remote.BrowserAuth.Type = %q, want %q", reloaded.Settings.Remote.BrowserAuth.Type, "cloudflare_access")
	}
	if reloaded.Settings.Remote.BrowserAuth.TeamDomain != "example.cloudflareaccess.com" {
		t.Errorf("Remote.BrowserAuth.TeamDomain = %q, want %q", reloaded.Settings.Remote.BrowserAuth.TeamDomain, "example.cloudflareaccess.com")
	}
}

func TestSQLiteStoreTokens(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tinyserve-sqlite-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "state.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Load initial state
	s, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(s.Tokens) != 0 {
		t.Errorf("expected 0 tokens, got %d", len(s.Tokens))
	}

	// Add a token
	now := time.Now().UTC().Truncate(time.Microsecond)
	s.Tokens = append(s.Tokens, APIToken{
		ID:        "tok-123",
		Name:      "test-token",
		Hash:      "hash123",
		CreatedAt: now,
	})

	if err := store.Save(ctx, s); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Reload and verify
	reloaded, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("Load() after Save() error = %v", err)
	}
	if len(reloaded.Tokens) != 1 {
		t.Fatalf("expected 1 token, got %d", len(reloaded.Tokens))
	}

	tok := reloaded.Tokens[0]
	if tok.ID != "tok-123" {
		t.Errorf("token ID = %q, want %q", tok.ID, "tok-123")
	}
	if tok.Name != "test-token" {
		t.Errorf("token Name = %q, want %q", tok.Name, "test-token")
	}
	if tok.Hash != "hash123" {
		t.Errorf("token Hash = %q, want %q", tok.Hash, "hash123")
	}

	// Add second token, update first with LastUsed
	lastUsed := now.Add(time.Hour)
	reloaded.Tokens[0].LastUsed = &lastUsed
	reloaded.Tokens = append(reloaded.Tokens, APIToken{
		ID:        "tok-456",
		Name:      "second-token",
		Hash:      "hash456",
		CreatedAt: now,
	})

	if err := store.Save(ctx, reloaded); err != nil {
		t.Fatalf("Save() with 2 tokens error = %v", err)
	}

	// Reload and verify both tokens
	reloaded2, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(reloaded2.Tokens) != 2 {
		t.Fatalf("expected 2 tokens, got %d", len(reloaded2.Tokens))
	}

	// Find first token and check LastUsed was updated
	var found bool
	for _, tok := range reloaded2.Tokens {
		if tok.ID == "tok-123" {
			found = true
			if tok.LastUsed == nil {
				t.Error("expected LastUsed to be set")
			}
		}
	}
	if !found {
		t.Error("first token not found after reload")
	}

	// Delete first token
	reloaded2.Tokens = []APIToken{reloaded2.Tokens[1]}
	if err := store.Save(ctx, reloaded2); err != nil {
		t.Fatalf("Save() after deletion error = %v", err)
	}

	// Verify deletion
	reloaded3, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(reloaded3.Tokens) != 1 {
		t.Fatalf("expected 1 token after deletion, got %d", len(reloaded3.Tokens))
	}
	if reloaded3.Tokens[0].ID != "tok-456" {
		t.Errorf("wrong token remaining: %s", reloaded3.Tokens[0].ID)
	}
}
