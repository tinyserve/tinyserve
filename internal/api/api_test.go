package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"tinyserve/internal/state"
)

func newTestHandler(t *testing.T) (*Handler, string) {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "tinyserve-api-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	// Create required subdirs
	os.MkdirAll(filepath.Join(tmpDir, "generated"), 0o755)
	os.MkdirAll(filepath.Join(tmpDir, "backups"), 0o755)
	os.MkdirAll(filepath.Join(tmpDir, "cloudflared"), 0o755)

	store := state.NewInMemoryStore(state.NewState())
	h := NewHandler(
		store,
		filepath.Join(tmpDir, "generated"),
		filepath.Join(tmpDir, "backups"),
		filepath.Join(tmpDir, "state.json"),
		filepath.Join(tmpDir, "cloudflared"),
	)
	return h, tmpDir
}

func TestHandleStatus(t *testing.T) {
	h, tmpDir := newTestHandler(t)
	defer os.RemoveAll(tmpDir)

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	w := httptest.NewRecorder()

	h.handleStatus(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("handleStatus() status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp["status"] != "ok" {
		t.Errorf("handleStatus() status = %v, want 'ok'", resp["status"])
	}
	if _, ok := resp["service_count"]; !ok {
		t.Error("handleStatus() missing service_count")
	}
}

func TestHandleServicesGet(t *testing.T) {
	h, tmpDir := newTestHandler(t)
	defer os.RemoveAll(tmpDir)

	req := httptest.NewRequest(http.MethodGet, "/services", nil)
	w := httptest.NewRecorder()

	h.handleServices(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("handleServices() GET status = %d, want %d", w.Code, http.StatusOK)
	}

	var services []any
	if err := json.Unmarshal(w.Body.Bytes(), &services); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	// Empty initially
	if len(services) != 0 {
		t.Errorf("handleServices() returned %d services, want 0", len(services))
	}
}

func TestHandleAddService(t *testing.T) {
	h, tmpDir := newTestHandler(t)
	defer os.RemoveAll(tmpDir)

	payload := map[string]any{
		"name":          "testapp",
		"image":         "nginx:latest",
		"internal_port": 80,
		"hostnames":     []string{"testapp.example.com"},
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/services", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.handleServices(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("handleAddService() status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var svc map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &svc); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if svc["name"] != "testapp" {
		t.Errorf("service name = %v, want 'testapp'", svc["name"])
	}
	if svc["id"] == nil || svc["id"] == "" {
		t.Error("service should have an ID")
	}
}

func TestHandleAddServiceValidation(t *testing.T) {
	h, tmpDir := newTestHandler(t)
	defer os.RemoveAll(tmpDir)

	tests := []struct {
		name       string
		payload    map[string]any
		wantStatus int
	}{
		{
			name: "missing name",
			payload: map[string]any{
				"image":         "nginx:latest",
				"internal_port": 80,
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "missing image",
			payload: map[string]any{
				"name":          "test",
				"internal_port": 80,
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "missing port",
			payload: map[string]any{
				"name":  "test",
				"image": "nginx:latest",
			},
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(tt.payload)
			req := httptest.NewRequest(http.MethodPost, "/services", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			h.handleServices(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("handleAddService() status = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

func TestHandleAddServiceDuplicateName(t *testing.T) {
	h, tmpDir := newTestHandler(t)
	defer os.RemoveAll(tmpDir)

	payload := map[string]any{
		"name":          "myapp",
		"image":         "nginx:latest",
		"internal_port": 80,
	}
	body, _ := json.Marshal(payload)

	// First add should succeed
	req := httptest.NewRequest(http.MethodPost, "/services", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.handleServices(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("first add failed: %s", w.Body.String())
	}

	// Second add with same name should fail
	body, _ = json.Marshal(payload)
	req = httptest.NewRequest(http.MethodPost, "/services", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.handleServices(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("duplicate name should return 409 Conflict, got %d", w.Code)
	}
}

func TestHandleAddServiceDuplicateHostname(t *testing.T) {
	h, tmpDir := newTestHandler(t)
	defer os.RemoveAll(tmpDir)

	// Add first service
	payload1 := map[string]any{
		"name":          "app1",
		"image":         "nginx:latest",
		"internal_port": 80,
		"hostnames":     []string{"myhost.example.com"},
	}
	body, _ := json.Marshal(payload1)
	req := httptest.NewRequest(http.MethodPost, "/services", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.handleServices(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("first add failed: %s", w.Body.String())
	}

	// Add second service with same hostname
	payload2 := map[string]any{
		"name":          "app2",
		"image":         "nginx:latest",
		"internal_port": 8080,
		"hostnames":     []string{"myhost.example.com"}, // duplicate
	}
	body, _ = json.Marshal(payload2)
	req = httptest.NewRequest(http.MethodPost, "/services", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.handleServices(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("duplicate hostname should return 409 Conflict, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestHandleAddServiceHostnameCaseInsensitive(t *testing.T) {
	h, tmpDir := newTestHandler(t)
	defer os.RemoveAll(tmpDir)

	// Add first service
	payload1 := map[string]any{
		"name":          "app1",
		"image":         "nginx:latest",
		"internal_port": 80,
		"hostnames":     []string{"MyHost.Example.COM"},
	}
	body, _ := json.Marshal(payload1)
	req := httptest.NewRequest(http.MethodPost, "/services", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.handleServices(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("first add failed: %s", w.Body.String())
	}

	// Add second service with same hostname (different case)
	payload2 := map[string]any{
		"name":          "app2",
		"image":         "nginx:latest",
		"internal_port": 8080,
		"hostnames":     []string{"myhost.example.com"}, // same hostname, different case
	}
	body, _ = json.Marshal(payload2)
	req = httptest.NewRequest(http.MethodPost, "/services", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.handleServices(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("case-insensitive hostname duplicate should return 409, got %d", w.Code)
	}
}

func TestHandleDeleteService(t *testing.T) {
	h, tmpDir := newTestHandler(t)
	defer os.RemoveAll(tmpDir)

	// Add a service first
	payload := map[string]any{
		"name":          "todelete",
		"image":         "nginx:latest",
		"internal_port": 80,
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/services", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.handleServices(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("add service failed: %s", w.Body.String())
	}

	// Delete the service
	req = httptest.NewRequest(http.MethodDelete, "/services/todelete", nil)
	w = httptest.NewRecorder()
	h.handleServiceByName(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("delete should return 200, got %d; body: %s", w.Code, w.Body.String())
	}

	// Verify service is gone
	req = httptest.NewRequest(http.MethodGet, "/services", nil)
	w = httptest.NewRecorder()
	h.handleServices(w, req)

	var services []any
	json.Unmarshal(w.Body.Bytes(), &services)
	if len(services) != 0 {
		t.Errorf("service should be deleted, but found %d services", len(services))
	}
}

func TestHandleDeleteServiceNotFound(t *testing.T) {
	h, tmpDir := newTestHandler(t)
	defer os.RemoveAll(tmpDir)

	req := httptest.NewRequest(http.MethodDelete, "/services/nonexistent", nil)
	w := httptest.NewRecorder()
	h.handleServiceByName(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("delete nonexistent should return 404, got %d", w.Code)
	}
}

func TestHandleLogsRequiresService(t *testing.T) {
	h, tmpDir := newTestHandler(t)
	defer os.RemoveAll(tmpDir)

	req := httptest.NewRequest(http.MethodGet, "/logs", nil)
	w := httptest.NewRecorder()
	h.handleLogs(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("logs without service should return 400, got %d", w.Code)
	}
}

func TestSanitizeName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"MyApp", "myapp"},
		{"my app", "my-app"},
		{"  test  ", "test"},
		{"TEST-APP", "test-app"},
	}

	for _, tt := range tests {
		result := sanitizeName(tt.input)
		if result != tt.expected {
			t.Errorf("sanitizeName(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestPruneBackups(t *testing.T) {
	h, tmpDir := newTestHandler(t)
	defer os.RemoveAll(tmpDir)

	// Create 15 backup directories
	for i := 0; i < 15; i++ {
		dir := filepath.Join(h.BackupsDir, "backup-2024010"+string(rune('0'+i/10))+string(rune('0'+i%10))+"-120000")
		os.MkdirAll(dir, 0o755)
	}

	// Create 15 state backup files
	for i := 0; i < 15; i++ {
		file := filepath.Join(h.BackupsDir, "state-2024010"+string(rune('0'+i/10))+string(rune('0'+i%10))+"-120000.json")
		os.WriteFile(file, []byte("{}"), 0o644)
	}

	// Prune to keep 10
	err := h.pruneBackups(10)
	if err != nil {
		t.Fatalf("pruneBackups() error = %v", err)
	}

	// Count remaining
	entries, _ := os.ReadDir(h.BackupsDir)
	dirCount := 0
	fileCount := 0
	for _, e := range entries {
		if e.IsDir() {
			dirCount++
		} else {
			fileCount++
		}
	}

	if dirCount != 10 {
		t.Errorf("pruneBackups() left %d directories, want 10", dirCount)
	}
	if fileCount != 10 {
		t.Errorf("pruneBackups() left %d files, want 10", fileCount)
	}
}

func TestPruneBackupsEmpty(t *testing.T) {
	h, tmpDir := newTestHandler(t)
	defer os.RemoveAll(tmpDir)

	// Should not error on empty dir
	err := h.pruneBackups(10)
	if err != nil {
		t.Errorf("pruneBackups() on empty dir error = %v", err)
	}
}

func TestPruneBackupsUnderLimit(t *testing.T) {
	h, tmpDir := newTestHandler(t)
	defer os.RemoveAll(tmpDir)

	// Create 5 backups
	for i := 0; i < 5; i++ {
		dir := filepath.Join(h.BackupsDir, "backup-2024010"+string(rune('0'+i))+"-120000")
		os.MkdirAll(dir, 0o755)
	}

	err := h.pruneBackups(10)
	if err != nil {
		t.Fatalf("pruneBackups() error = %v", err)
	}

	// Should keep all 5
	entries, _ := os.ReadDir(h.BackupsDir)
	if len(entries) != 5 {
		t.Errorf("pruneBackups() should keep all %d backups when under limit", len(entries))
	}
}

func TestMethodNotAllowed(t *testing.T) {
	h, tmpDir := newTestHandler(t)
	defer os.RemoveAll(tmpDir)

	tests := []struct {
		path   string
		method string
	}{
		{"/deploy", http.MethodGet},
		{"/rollback", http.MethodGet},
		{"/logs", http.MethodPost},
		{"/init", http.MethodGet},
	}

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)

			if w.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s %s should return 405, got %d", tt.method, tt.path, w.Code)
			}
		})
	}
}
